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
	"regexp"
	"strings"

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
	case "ai_action":
		if _, err = page.Goto(payload.URL); err != nil {
			result.Error = fmt.Sprintf("could not goto: %v", err)
			break
		}

		// 1. Analyze Page (Get Clean Text)
		cleanText, err := page.Evaluate(`() => {
			const clone = document.body.cloneNode(true);
			const selectors = ['script', 'style', 'svg', 'noscript', 'iframe', 'link', 'meta'];
			selectors.forEach(s => {
				const elements = clone.querySelectorAll(s);
				elements.forEach(e => e.remove());
			});
			// Get inputs specifically to help the AI find selectors
			const inputs = Array.from(document.querySelectorAll('input, button, select, textarea, a.btn')).map(el => {
				let ident = el.tagName.toLowerCase();
				if (el.id) ident += '#' + el.id;
				if (el.name) ident += '[name="' + el.name + '"]';
				if (el.innerText) ident += ' (text: "' + el.innerText.trim().slice(0, 20) + '")';
				return ident;
			}).join('\n');
			
			return clone.innerText.replace(/\s+/g, ' ').trim() + "\n\n*** DETECTED INTERACTIVE ELEMENTS ***\n" + inputs;
		}`)
		if err != nil {
			result.Error = fmt.Sprintf("could not analyze page: %v", err)
			break
		}
		
		textStr, _ := cleanText.(string)
		if len(textStr) > 8000 {
			textStr = textStr[:8000] + "...(truncated)"
		}

		// 2. Take initial screenshot for context
		screenshot, err := page.Screenshot(playwright.PageScreenshotOptions{Type: playwright.ScreenshotTypeJpeg})
		if err != nil {
			result.Error = fmt.Sprintf("could not take screenshot: %v", err)
			break
		}
		encodedImage := base64.StdEncoding.EncodeToString(screenshot)

		userInstruction := payload.Target
		if userInstruction == "" {
			userInstruction = "Do something on this page."
		}

		// 3. Ask Ollama for a plan
		// SANDWICH STRATEGY
		prompt := fmt.Sprintf(`*** SYSTEM INSTRUCTIONS ***
You are a Browser Automation Agent.
Your goal is to convert the User Request into a sequence of JSON steps.

OUTPUT FORMAT:
Return ONLY a JSON array of objects. Do not write explanations.
Supported Actions:
1. {"type": "fill", "selector": "css_selector", "value": "text_to_type"}
2. {"type": "click", "selector": "css_selector"}
3. {"type": "press", "key": "Enter"}

*** WEBPAGE CONTEXT ***
%s

*** USER REQUEST ***
%s

*** FINAL COMMAND ***
Generate the JSON array of steps to fulfill the request.
Use the "DETECTED INTERACTIVE ELEMENTS" list to pick the best selector (prefer ID or Name).
Output JSON ONLY.
Response:`, textStr, userInstruction)

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

		var ollamaResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&ollamaResp)
		aiResponse, _ := ollamaResp["response"].(string)

		// Log raw response for debugging (visible to user in stream)
		fmt.Printf("\nAI RAW RESPONSE:\n%s\n", aiResponse)

		// 4. Parse AI Response (JSON Extraction)
		// Models often wrap JSON in markdown code blocks, so we extract it
		jsonRegex := regexp.MustCompile(`(?s)\[.*\]`)
		match := jsonRegex.FindString(aiResponse)
		if match == "" {
			// Fallback: try to find start and end brackets manually
			start := strings.Index(aiResponse, "[")
			end := strings.LastIndex(aiResponse, "]")
			if start != -1 && end != -1 && end > start {
				match = aiResponse[start : end+1]
			}
		}

		if match == "" {
			result.Error = "AI failed to generate valid JSON steps. See raw response in logs."
			break
		}

		type ActionStep struct {
			Type     string `json:"type"`
			Selector string `json:"selector,omitempty"`
			Value    string `json:"value,omitempty"`
			Key      string `json:"key,omitempty"`
		}
		var steps []ActionStep
		if err := json.Unmarshal([]byte(match), &steps); err != nil {
			result.Error = fmt.Sprintf("failed to parse AI steps: %v", err)
			break
		}

		// 5. Execute Steps
		actionLog := []string{}
		for i, step := range steps {
			fmt.Printf("Executing Step %d: %v\n", i+1, step) // Log to stdout for the user to see
			
			switch step.Type {
			case "fill":
				// Attempt to wait for selector visibility
				element, err := page.WaitForSelector(step.Selector, playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(2000)})
				if err == nil && element != nil {
					// Only try filling if found
					if err := page.Fill(step.Selector, step.Value); err != nil {
						actionLog = append(actionLog, fmt.Sprintf("❌ Fill %s failed: %v", step.Selector, err))
					} else {
						actionLog = append(actionLog, fmt.Sprintf("✅ Filled %s with '%s'", step.Selector, step.Value))
					}
				} else {
					actionLog = append(actionLog, fmt.Sprintf("⚠️ Could not find selector: %s", step.Selector))
				}
			case "click":
				page.WaitForSelector(step.Selector, playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(2000)})
				if err := page.Click(step.Selector); err != nil {
					actionLog = append(actionLog, fmt.Sprintf("❌ Click %s failed: %v", step.Selector, err))
				} else {
					actionLog = append(actionLog, fmt.Sprintf("✅ Clicked %s", step.Selector))
				}
			case "press":
				// If key is missing, default to Enter? Or skip?
				k := step.Key
				if k == "" { k = "Enter" }
				if err := page.Keyboard().Press(k); err != nil {
					actionLog = append(actionLog, fmt.Sprintf("❌ Press %s failed: %v", k, err))
				} else {
					actionLog = append(actionLog, fmt.Sprintf("✅ Pressed %s", k))
				}
			}
			// Small delay for stability
			page.WaitForTimeout(500)
		}

		// 6. Capture Final State
		// Wait a bit for navigation if it happened
		page.WaitForTimeout(2000)
		
		finalScreenshot, _ := page.Screenshot(playwright.PageScreenshotOptions{Type: playwright.ScreenshotTypeJpeg})
		result.Image = base64.StdEncoding.EncodeToString(finalScreenshot)
		result.Success = true
		result.Data = fmt.Sprintf("Executed %d steps.\n\nLog:\n%s", len(steps), strings.Join(actionLog, "\n"))

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

			// Get Cleaned Text content (innerText) to remove HTML noise
			// We use Evaluate to run JS in the browser context
			cleanText, err := page.Evaluate(`() => {
				// Clone the body to not affect the screenshot (though screenshot is already taken)
				const clone = document.body.cloneNode(true);
				
				// Remove noise
				const selectors = ['script', 'style', 'svg', 'noscript', 'iframe', 'link', 'meta'];
				selectors.forEach(s => {
					const elements = clone.querySelectorAll(s);
					elements.forEach(e => e.remove());
				});
				
				// Return plain text, collapsing whitespace
				return clone.innerText.replace(/\s+/g, ' ').trim();
			}`)
			if err != nil {
				result.Error = fmt.Sprintf("could not clean page content: %v", err)
				break
			}
			
			textStr, ok := cleanText.(string)
			if !ok {
				textStr = "Unable to retrieve text"
			}

			// Truncate text to prevent context overflow
			// Text is much denser than HTML, so 15k chars of text is A LOT of content.
			// 5000 chars is usually enough for the main content of a page.
			if len(textStr) > 5000 {
				textStr = textStr[:5000] + "...(truncated)"
			}

			// Prepare request to Ollama
			encodedImage := base64.StdEncoding.EncodeToString(screenshot)
			
			userInstruction := payload.Target
			if userInstruction == "" {
				userInstruction = "Explain what this page is."
			}

			// SANDWICH STRATEGY: Instructions BEFORE and AFTER the context.
			prompt := fmt.Sprintf(`*** SYSTEM INSTRUCTIONS ***
You are a generic web analyst AI.
1. MANDATORY: You must ALWAYS respond in ENGLISH.
2. Ignore the language of the webpage content for your response language.
3. Be concise and professional.
4. Do NOT hallucinate HTML tags.

*** WEBPAGE TEXT CONTENT ***
%s

*** USER REQUEST ***
%s

*** FINAL COMMAND ***
Based on the image and text above, answer the user's request.
Ensure your entire response is in English.
Response:`, textStr, userInstruction)

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
