package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/playwright-community/playwright-go"
)

type JobPayload struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	Target string `json:"target,omitempty"`
}

type JobResult struct {
	Success bool   `json:"success"`
	Data    string `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

func main() {
	// Support for Docker build-time installation
	if os.Getenv("INSTALL_ONLY") == "true" {
		if err := playwright.Install(); err != nil {
			log.Fatalf("could not install playwright: %v", err)
		}
		fmt.Println("Playwright dependencies installed.")
		return
	}

	payloadStr := os.Getenv("JOB_PAYLOAD")
	if payloadStr == "" {
		log.Fatal("JOB_PAYLOAD environment variable is required")
	}

	var payload JobPayload
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		log.Fatalf("failed to unmarshal payload: %v", err)
	}

	err := playwright.Install()
	if err != nil {
		log.Fatalf("could not install playwright: %v", err)
	}

	pw, err := playwright.Run()
	if err != nil {
		log.Fatalf("could not start playwright: %v", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch()
	if err != nil {
		log.Fatalf("could not launch browser: %v", err)
	}
	defer browser.Close()

	page, err := browser.NewPage()
	if err != nil {
		log.Fatalf("could not create page: %v", err)
	}

	var result JobResult
	switch payload.Action {
	case "scrape":
		if _, err = page.Goto(payload.URL); err != nil {
			result.Error = fmt.Sprintf("could not goto: %v", err)
		} else {
			content, err := page.Content()
			if err != nil {
				result.Error = fmt.Sprintf("could not get content: %v", err)
			} else {
				result.Success = true
				result.Data = content
			}
		}
	default:
		result.Error = fmt.Sprintf("unknown action: %s", payload.Action)
	}

	output, _ := json.Marshal(result)
	fmt.Println(string(output))
}
