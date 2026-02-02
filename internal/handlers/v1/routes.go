package v1

import (
	uihandlers "brian-nunez/bcode/internal/handlers/v1/ui"
	"github.com/labstack/echo/v4"
)

func RegisterRoutes(e *echo.Echo) {
	e.GET("/", uihandlers.HomeHandler)
	e.GET("/scrape", uihandlers.ScrapePageHandler)
	e.GET("/describe", uihandlers.DescribePageHandler)
	e.GET("/ai-actions", uihandlers.AIActionsPageHandler)
	e.POST("/execute", uihandlers.ExecuteJobHandler)

	v1Group := e.Group("/api/v1")
	v1Group.GET("/health", HealthHandler)
}
