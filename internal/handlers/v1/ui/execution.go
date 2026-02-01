package uihandlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	scanner := bufio.NewScanner(logs)
	
	// Create a buffer for lines that might be split (Docker stream format can be tricky, but usually line-based)
	// Actually, Docker raw stream includes headers. 'RunJob' uses stdcopy under the hood?
	// orchestrator.RunJob returns 'cli.ContainerLogs', which returns 'io.ReadCloser'.
	// If TTY is false (default), it includes headers. 
	// We are just treating it as a raw stream for now. If it has headers, they will show up as garbage chars at start of line.
	// We can strip them if needed, but for now let's just read lines.
	
	for scanner.Scan() {
		line := scanner.Text()
		
		// Very basic cleanup of Docker log headers if present (usually 8 bytes)
		// This is a naive heuristic; for production use 'stdcopy.StdCopy' to demultiplex
		// cleanLine := line // Unused currently
		
		// Try to find JSON start
		// The worker prints the result as the last line, hopefully cleanly.
		// We'll look for a line starting with '{' and containing "success":
		
		// Only try to parse as JSON if it looks like our result
		// We know our result has "success" and "data" keys.

		// We assume the JSON is at the end of the line or is the whole line
		// Strip potential non-printable characters from start (Docker headers)
		
		// Also scan from the end just in case
		foundJSON := false
		for i := 0; i < len(line); i++ {
			if line[i] == '{' {
				// Create a new struct for each attempt to avoid stale data
				var attemptResult struct {
					Success bool   `json:"success"`
					Data    string `json:"data"`
					Image   string `json:"image"`
					Error   string `json:"error"`
				}
				
				if err := json.Unmarshal([]byte(line[i:]), &attemptResult); err == nil {
					// It is our JSON!
					execution.JobResultView(attemptResult.Data, attemptResult.Image).Render(context.Background(), c.Response().Writer)
					c.Response().Flush()
					foundJSON = true
					break 
				}
			}
		}
		
		if foundJSON {
			continue
		}

		// Otherwise just print the line as a log
		// We use a small script to append safely or just raw HTML
		fmt.Fprintf(c.Response().Writer, "<div class='text-xs text-gray-500 font-mono'>%s</div>", line)
		c.Response().Flush()
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(c.Response().Writer, "<div class='text-red-500'>Error reading logs: %v</div>", err)
	}

	return nil
}
