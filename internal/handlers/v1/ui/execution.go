package uihandlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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

		// 1. Check for Live Updates (Screenshots)
		const updatePrefix = "JOB_UPDATE:"
		if idx := strings.Index(line, updatePrefix); idx != -1 {
			jsonPart := line[idx+len(updatePrefix):]
			var update struct {
				Image string `json:"image"`
			}
			if err := json.Unmarshal([]byte(jsonPart), &update); err == nil && update.Image != "" {
				// Protocol: IMG: <data>
				fmt.Fprintf(c.Response().Writer, "IMG: %s\n", update.Image)
				c.Response().Flush()
				continue
			}
		}

		// 2. Check for Final Result
		const resultPrefix = "JOB_RESULT:"
		if idx := strings.Index(line, resultPrefix); idx != -1 {
			jsonPart := line[idx+len(resultPrefix):]
			var attemptResult struct {
				Success bool   `json:"success"`
				Data    string `json:"data"`
				Image   string `json:"image"`
				Error   string `json:"error"`
			}

			if err := json.Unmarshal([]byte(jsonPart), &attemptResult); err == nil {
				// Render the result component to a buffer/string
				resultBuf := bytes.NewBuffer(nil)
				execution.JobResultView(attemptResult.Data, attemptResult.Image).Render(context.Background(), resultBuf)

				// Protocol: END: <html>
				cleanHTML := strings.ReplaceAll(resultBuf.String(), "\n", " ")
				fmt.Fprintf(c.Response().Writer, "END: %s\n", cleanHTML)
				c.Response().Flush()
				continue
			}
		}

		// Otherwise just print the line as a log
		// Protocol: LOG: <html>
		fmt.Fprintf(c.Response().Writer, "LOG: <div class='text-xs text-gray-400 font-mono'>%s</div>\n", line)
		c.Response().Flush()
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(c.Response().Writer, "LOG: <div class='text-red-500'>Error reading logs: %v</div>\n", err)
		c.Response().Flush()
	}

	return nil
}
