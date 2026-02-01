package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	Image   string `json:"image,omitempty"`
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

	// Skip installation at runtime if browsers are already present
	// This prevents the verbose "Downloading browsers..." logs and re-validation checks
	// The Dockerfile already installed them to /ms-playwright
	if _, err := os.Stat("/ms-playwright"); os.IsNotExist(err) {
		err := playwright.Install()
		if err != nil {
			log.Fatalf("could not install playwright: %v", err)
		}
	}

	payloadStr := os.Getenv("JOB_PAYLOAD")
	if payloadStr == "" {
		log.Fatal("JOB_PAYLOAD environment variable is required")
	}

	var payload JobPayload
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		log.Fatalf("failed to unmarshal payload: %v", err)
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
	case "describe":
		if _, err = page.Goto(payload.URL); err != nil {
			result.Error = fmt.Sprintf("could not goto: %v", err)
		} else {
			// Take a screenshot
			screenshot, err := page.Screenshot(playwright.PageScreenshotOptions{
				Type: playwright.ScreenshotTypeJpeg,
			})
			if err != nil {
				result.Error = fmt.Sprintf("could not take screenshot: %v", err)
				break
			}

			// Prepare request to Ollama
			encodedImage := base64.StdEncoding.EncodeToString(screenshot)
			
			prompt := payload.Target
			if prompt == "" {
				prompt = "Provide a concise description of this webpage based on the image."
			}

			ollamaReq := map[string]interface{}{
				"model":  "gemma3:4b",
				"prompt": prompt,
				"images": []string{encodedImage},
				"stream": false,
			}
			reqBody, _ := json.Marshal(ollamaReq)

			resp, err := http.Post("http://10.0.0.115:11434/api/generate", "application/json", bytes.NewBuffer(reqBody))
			if err != nil {
				result.Error = fmt.Sprintf("could not contact ollama: %v", err)
				break
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				result.Error = fmt.Sprintf("ollama returned status %d: %s", resp.StatusCode, string(body))
				break
			}

			var ollamaResp map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
				result.Error = fmt.Sprintf("could not decode ollama response: %v", err)
				break
			}

			if responseText, ok := ollamaResp["response"].(string); ok {
				result.Success = true
				result.Data = responseText
				result.Image = encodedImage
			} else {
				result.Error = "ollama response missing 'response' field"
			}
		}
	default:
		result.Error = fmt.Sprintf("unknown action: %s", payload.Action)
	}

	output, _ := json.Marshal(result)
	fmt.Printf("\nJOB_RESULT:%s\n", string(output))
}
