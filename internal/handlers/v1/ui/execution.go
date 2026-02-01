package uihandlers

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"brian-nunez/bcode/internal/orchestrator"
	"brian-nunez/bcode/views/execution"
	"github.com/labstack/echo/v4"
)

func ExecutionPageHandler(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return execution.Execution().Render(context.Background(), c.Response().Writer)
}

func ExecuteJobHandler(c echo.Context) error {
	url := c.FormValue("url")
	action := c.FormValue("action")
	instruction := c.FormValue("instruction")

	// Escape quotes in the instruction to prevent JSON breakage
	// In a real app, we should use a proper JSON struct marshal
	reqBody := map[string]string{
		"url":    url,
		"action": action,
		"target": instruction,
	}
	jsonPayload, _ := json.Marshal(reqBody)

	payload := orchestrator.JobRequest{
		Payload: string(jsonPayload),
	}

	logs, err := orchestrator.RunJob(c.Request().Context(), payload)
	if err != nil {
		return c.String(http.StatusInternalServerError, fmt.Sprintf("Failed to run job: %v", err))
	}
	defer logs.Close()

	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	c.Response().WriteHeader(http.StatusOK)

	// Stream logs line by line
	pr, pw := io.Pipe()
	
	// Start a goroutine to strip Docker headers and write clean logs to the pipe
	go func() {
		defer pw.Close()
		header := make([]byte, 8)
		for {
			_, err := io.ReadFull(logs, header)
			if err != nil {
				return // EOF or error
			}
			
			// Parse payload size (bytes 4-7, big endian)
			size := binary.BigEndian.Uint32(header[4:8])
			
			// Copy the payload to the pipe
			if _, err := io.CopyN(pw, logs, int64(size)); err != nil {
				return
			}
		}
	}()

	scanner := bufio.NewScanner(pr)
	// Increase buffer size to handle large base64 images (5MB)
	const maxCapacity = 5 * 1024 * 1024
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)
	
	for scanner.Scan() {
		line := scanner.Text()
		
		// Look for our specific prefix
		const prefix = "JOB_RESULT:"
		foundIdx := -1
		
		// Simple string search to handle Docker header noise
		for i := 0; i <= len(line)-len(prefix); i++ {
			if line[i:i+len(prefix)] == prefix {
				foundIdx = i
				break
			}
		}

		if foundIdx != -1 {
			jsonPart := line[foundIdx+len(prefix):]
			var attemptResult struct {
				Success bool   `json:"success"`
				Data    string `json:"data"`
				Image   string `json:"image"`
				Error   string `json:"error"`
			}
			
			if err := json.Unmarshal([]byte(jsonPart), &attemptResult); err == nil {
				execution.JobResultView(attemptResult.Data, attemptResult.Image).Render(context.Background(), c.Response().Writer)
				c.Response().Flush()
				continue 
			} else {
				// Debug output if JSON parsing fails
				fmt.Fprintf(c.Response().Writer, "<div class='text-xs text-red-500 font-mono'>Failed to parse result JSON: %v</div>", err)
			}
		}

		// Otherwise just print the line as a log
		fmt.Fprintf(c.Response().Writer, "<div class='text-xs text-gray-500 font-mono'>%s</div>", line)
		c.Response().Flush()
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(c.Response().Writer, "<div class='text-red-500'>Error reading logs: %v</div>", err)
	}

	return nil
}
