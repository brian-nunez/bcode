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

		// 1. Wait for load state to prevent white screenshots
		page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateNetworkidle})
		page.WaitForTimeout(2000)

		// Agent Configuration
		const maxIterations = 5
		fmt.Println("🤖 Starting AI Agent Loop (Max 5 steps)...")

		history := []string{}
		
		for i := 1; i <= maxIterations; i++ {
			fmt.Printf("\n--- Iteration %d/%d ---\n", i, maxIterations)

			// 2. Observe (Index Elements)
			pageAnalysis, err := page.Evaluate(`() => {
				const map = {};
				const items = [];
				
				function getSelector(el) {
					if (el.id) return '#' + el.id;
					if (el.name) return el.tagName.toLowerCase() + '[name="' + el.name + '"]';
					if (el.className) {
						const classes = el.className.split(/\s+/).filter(c => c.length > 0).join('.');
						if (classes) return el.tagName.toLowerCase() + '.' + classes;
					}
					return el.tagName.toLowerCase(); 
				}

				const elements = Array.from(document.querySelectorAll('input, button, select, textarea, a.btn, [role="button"], a[href]'));
				const visibleElements = elements.filter(el => {
					return el.offsetWidth > 0 && el.offsetHeight > 0 && window.getComputedStyle(el).visibility !== 'hidden';
				});

				visibleElements.forEach((el, index) => {
					const id = index + 1;
					const selector = getSelector(el);
					let desc = el.tagName.toLowerCase();
					if (el.id) desc += ' id:"' + el.id + '"';
					if (el.name) desc += ' name:"' + el.name + '"';
					if (el.type) desc += ' [type="' + el.type + '"]';
					if (el.innerText) desc += ' text:"' + el.innerText.trim().slice(0, 30) + '"';
					if (el.placeholder) desc += ' placeholder:"' + el.placeholder + '"';
					if (el.ariaLabel) desc += ' aria-label:"' + el.ariaLabel + '"';
					
					// Critical: Read current value so AI knows it's filled
					if ((el.tagName.toLowerCase() === 'input' || el.tagName.toLowerCase() === 'textarea') && el.value) {
						desc += ' current_value:"' + el.value.slice(0, 50) + '"';
					}
					
					map[id] = selector;
					items.push(id + ": " + desc);
				});

				const clone = document.body.cloneNode(true);
				['script', 'style', 'svg'].forEach(s => clone.querySelectorAll(s).forEach(e => e.remove()));
				const text = clone.innerText.replace(/\s+/g, ' ').trim().slice(0, 3000); 

				return { text, items: items.join('\n'), selectorMap: map };
			}`)
			
			if err != nil {
				result.Error = fmt.Sprintf("Analysis failed: %v", err)
				break
			}

			data := pageAnalysis.(map[string]interface{})
			pageText := data["text"].(string)
			elementList := data["items"].(string)
			selectorMapRaw := data["selectorMap"].(map[string]interface{})
			
			fmt.Printf("Available IDs: %v\n", selectorMapRaw)

			screenshot, _ := page.Screenshot(playwright.PageScreenshotOptions{Type: playwright.ScreenshotTypeJpeg})
			encodedImage := base64.StdEncoding.EncodeToString(screenshot)

			// 3. Think (Prompt)
			userRequest := payload.Target
			if userRequest == "" { userRequest = "Interact with the page." }

			historyStr := strings.Join(history, "\n")
			if len(history) > 0 { historyStr = "HISTORY:\n" + historyStr } else { historyStr = "No actions yet." }

			prompt := fmt.Sprintf(`*** AGENT INSTRUCTIONS ***
You are an autonomous browser agent.
Goal: "%s"

CURRENT STATE:
%s

AVAILABLE ELEMENTS (ID: Description):
%s

PAGE TEXT SUMMARY:
%s

RESPONSE FORMAT:
Thought: <Reasoning about the page state and next step>
JSON: [{"action": "fill", "id": 1, "value": "text"}, {"action": "click", "id": 2}]

INSTRUCTIONS:
- CHECK "current_value" in AVAILABLE ELEMENTS. If a field is already filled, DO NOT fill it again.
- You CAN perform multiple actions in one response (e.g., fill username, fill password, click submit).
- Return a JSON ARRAY of commands.
- If the goal is achieved (e.g., logged in), return [{"action": "finish", "result": "Done"}].

Response:`, userRequest, historyStr, elementList, pageText)

			ollamaReq := map[string]interface{}{
				"model":  "gemma3:4b",
				"prompt": prompt,
				"images": []string{encodedImage},
				"stream": false,
				"options": map[string]interface{}{ "temperature": 0.0 },
			}
			reqBody, _ := json.Marshal(ollamaReq)
			
			resp, err := http.Post("http://10.0.0.115:11434/api/generate", "application/json", bytes.NewBuffer(reqBody))
			if err != nil {
				result.Error = fmt.Sprintf("Ollama Error: %v", err)
				break
			}
			defer resp.Body.Close()

			var ollamaResp map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&ollamaResp)
			aiResponse := ollamaResp["response"].(string)
			
			fmt.Printf("AI: %s\n", aiResponse)

			// 4. Parse Thought & JSON
			jsonRegex := regexp.MustCompile(`(?s)(\[.*\]|\{.*\})`) // Match array OR object
			match := jsonRegex.FindString(aiResponse)
			
			if match == "" {
				history = append(history, "Error: No valid JSON found. Please output JSON.")
				continue
			}

			type AgentCommand struct {
				Action string `json:"action"`
				ID     int    `json:"id"`
				Value  string `json:"value"`
				Key    string `json:"key"`
				Result string `json:"result"`
			}
			var cmds []AgentCommand

			// Try parsing as array first
			if err := json.Unmarshal([]byte(match), &cmds); err != nil {
				// If array fails, try parsing as single object
				var singleCmd AgentCommand
				if err2 := json.Unmarshal([]byte(match), &singleCmd); err2 == nil {
					cmds = []AgentCommand{singleCmd}
				} else {
					history = append(history, "Error: Invalid JSON format. Return valid JSON.")
					continue
				}
			}

			// If we have commands, execute them
			// Execute Commands
			for _, cmd := range cmds {
				if cmd.Action == "finish" {
					result.Success = true
					result.Data = fmt.Sprintf("Finished: %s\n\nHistory:\n%s", cmd.Result, strings.Join(history, "\n"))
					result.Image = encodedImage
					goto EndLoop
				}

				// Map ID
				selectorInterface, exists := selectorMapRaw[fmt.Sprintf("%d", cmd.ID)]
				if !exists && cmd.Action != "press" {
					history = append(history, fmt.Sprintf("Error: ID %d not found.", cmd.ID))
					continue
				}
				selector := ""
				if exists { selector = selectorInterface.(string) }

				// Execute
				var execErr error
				switch cmd.Action {
				case "fill":
					execErr = page.Fill(selector, cmd.Value)
				case "click":
					execErr = page.Click(selector)
				case "press":
					execErr = page.Keyboard().Press(cmd.Key)
				default:
					execErr = fmt.Errorf("unknown action: %s", cmd.Action)
				}

				if execErr != nil {
					history = append(history, fmt.Sprintf("Failed to %s ID %d: %v", cmd.Action, cmd.ID, execErr))
				} else {
					history = append(history, fmt.Sprintf("Success: %s ID %d (%s)", cmd.Action, cmd.ID, selector))
					// Small wait between batched actions to ensure stability
					page.WaitForTimeout(500)
				}
			} // End of cmds loop

			// Wait after the batch is done
			page.WaitForTimeout(2000) 
		} // End of maxIterations loop
		
		EndLoop:
		if !result.Success {
			finalScreenshot, _ := page.Screenshot(playwright.PageScreenshotOptions{Type: playwright.ScreenshotTypeJpeg})
			result.Image = base64.StdEncoding.EncodeToString(finalScreenshot)
			result.Success = true
			result.Data = fmt.Sprintf("Stopped after %d steps.\n\nHistory:\n%s", maxIterations, strings.Join(history, "\n"))
		}

	case "describe":
		if _, err = page.Goto(payload.URL); err != nil {
			result.Error = fmt.Sprintf("could not goto: %v", err)
		} else {
			// Fix for white screenshots: Wait for the page to actually load
			page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateNetworkidle})
			page.WaitForTimeout(2000)

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
