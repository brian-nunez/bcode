package uihandlers

import (
	"context"
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

	payload := orchestrator.JobRequest{
		Payload: fmt.Sprintf(`{"url":"%s", "action":"%s"}`, url, action),
	}

	logs, err := orchestrator.RunJob(c.Request().Context(), payload)
	if err != nil {
		return c.String(http.StatusInternalServerError, fmt.Sprintf("Failed to run job: %v", err))
	}
	defer logs.Close()

	// For HTMX/SSE, we might want to stream this.
	// But for a simple POST with hx-swap=\"beforeend\", we can just return the logs as they come.
	// However, HTMX expects a response when the request is done.
	// If we want real-time, we should use SSE.
	
	// For now, let's just read all logs and return them.
	// In a real scenario, we'd use SSE as suggested in GEMINI.md.
	
	c.Response().Header().Set(echo.HeaderContentType, "text/plain")
	c.Response().WriteHeader(http.StatusOK)

	_, err = io.Copy(c.Response().Writer, logs)
	if err != nil {
		fmt.Printf("Error copying logs: %v\n", err)
	}

	return nil
}
