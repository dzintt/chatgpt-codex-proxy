//go:build live

package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type liveConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

func TestLiveChatCompletionsCustomToolStreamingRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := loadLiveConfig(t)
	tool := applyPatchToolDefinition()
	userPrompt := "Use the ApplyPatch tool to create test.txt with the exact contents hello. Do not answer in natural language before calling the tool."

	streamBody := postJSON(t, cfg, "/chat/completions", map[string]any{
		"model":  cfg.Model,
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": userPrompt},
		},
		"tools": []map[string]any{tool},
		"tool_choice": map[string]any{
			"type": "custom",
			"name": "ApplyPatch",
		},
	})

	callID, toolName, arguments := extractStreamedToolCall(t, streamBody)
	if callID == "" {
		t.Fatal("stream did not include a tool call id")
	}
	if toolName != "ApplyPatch" {
		t.Fatalf("streamed tool name = %q, want ApplyPatch", toolName)
	}
	if !strings.Contains(arguments, "test.txt") {
		t.Fatalf("streamed tool arguments = %q, want patch content mentioning test.txt", arguments)
	}
	if !strings.Contains(arguments, "*** Begin Patch") {
		t.Fatalf("streamed tool arguments = %q, want ApplyPatch input", arguments)
	}

	followUpBody := postJSON(t, cfg, "/chat/completions", map[string]any{
		"model":  cfg.Model,
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": "Use the ApplyPatch tool to create test.txt with the exact contents hello. After the tool returns, respond with exactly DONE."},
			{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{
					{
						"id":   callID,
						"type": "function",
						"function": map[string]any{
							"name":      toolName,
							"arguments": arguments,
						},
					},
				},
			},
			{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      "Patch applied successfully.",
			},
		},
		"tools":       []map[string]any{tool},
		"tool_choice": "none",
	})

	message, toolCalls := extractChatCompletionMessage(t, followUpBody)
	if len(toolCalls) != 0 {
		t.Fatalf("follow-up completion emitted unexpected tool calls: %#v", toolCalls)
	}
	if strings.TrimSpace(message) != "DONE" {
		t.Fatalf("follow-up assistant message = %q, want DONE", message)
	}
}

func TestLiveChatCompletionsCustomToolNonStreamProbe(t *testing.T) {
	t.Parallel()

	cfg := loadLiveConfig(t)
	body := postJSON(t, cfg, "/chat/completions", map[string]any{
		"model":  cfg.Model,
		"stream": false,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": "Use the ApplyPatch tool to create test.txt with the exact contents hello. Do not answer in natural language before calling the tool.",
			},
		},
		"tools": []map[string]any{applyPatchToolDefinition()},
		"tool_choice": map[string]any{
			"type": "custom",
			"name": "ApplyPatch",
		},
	})

	message, toolCalls := extractChatCompletionMessage(t, body)
	if len(toolCalls) == 0 {
		t.Fatalf("non-stream completion returned no tool calls; message=%q body=%s", message, string(body))
	}

	first := toolCalls[0]
	toolType, _ := first["type"].(string)
	name := extractToolCallName(first)
	if name != "ApplyPatch" {
		t.Fatalf("non-stream tool name = %q, want ApplyPatch", name)
	}
	if extractToolCallArguments(first) == "" {
		t.Fatalf("non-stream tool call arguments were empty: %#v", first)
	}

	t.Logf("non-stream tool call type=%q name=%q", toolType, name)
	if toolType != "function" {
		t.Logf("non-stream response is not using the streaming compatibility shim; this may still be incompatible with Cursor if it ever relies on stream=false chat completions")
	}
}

func loadLiveConfig(t *testing.T) liveConfig {
	t.Helper()

	cfg := liveConfig{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		Model:   os.Getenv("OPENAI_MODEL"),
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "change-me-to-a-long-random-string"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:8080/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-5.2"
	}

	return cfg
}

func applyPatchToolDefinition() map[string]any {
	return map[string]any{
		"type":        "custom",
		"name":        "ApplyPatch",
		"description": "Apply a patch to the workspace",
		"format": map[string]any{
			"type":   "grammar",
			"syntax": "lark",
			"definition": strings.Join([]string{
				"start: begin_patch hunk end_patch",
				`begin_patch: "*** Begin Patch" LF`,
				`end_patch: "*** End Patch" LF?`,
				"",
				"hunk: add_hunk | update_hunk",
				`add_hunk: "*** Add File: " filename LF add_line+`,
				`update_hunk: "*** Update File: " filename LF change?`,
				"",
				"filename: /(.+)/",
				`add_line: "+" /(.*)/ LF -> line`,
				"",
				"change: (change_context | change_line)+ eof_line?",
				`change_context: ("@@" | "@@ " /(.+)/) LF`,
				`change_line: ("+" | "-" | " ") /(.*)/ LF`,
				`eof_line: "*** End of File" LF`,
				"",
				"%import common.LF",
			}, "\n"),
		},
	}
}

func postJSON(t *testing.T, cfg liveConfig, path string, payload any) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		t.Fatalf("request %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}

	return respBody
}

func extractStreamedToolCall(t *testing.T, body []byte) (string, string, string) {
	t.Helper()

	var (
		callID       string
		toolName     string
		arguments    strings.Builder
		sawToolCalls bool
		sawFinish    bool
	)

	for _, event := range strings.Split(string(body), "\n\n") {
		event = strings.TrimSpace(event)
		if event == "" || strings.Contains(event, "data: [DONE]") {
			continue
		}

		var dataLines []string
		for _, line := range strings.Split(event, "\n") {
			if strings.HasPrefix(line, "data: ") {
				dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			}
		}
		if len(dataLines) == 0 {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &payload); err != nil {
			t.Fatalf("decode sse payload: %v\npayload=%s", err, strings.Join(dataLines, "\n"))
		}

		choices, _ := payload["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if finish, _ := choice["finish_reason"].(string); finish == "tool_calls" {
			sawFinish = true
		}

		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		rawToolCalls, _ := delta["tool_calls"].([]any)
		if len(rawToolCalls) == 0 {
			continue
		}
		sawToolCalls = true

		first, _ := rawToolCalls[0].(map[string]any)
		if id, _ := first["id"].(string); id != "" {
			callID = id
		}
		function, _ := first["function"].(map[string]any)
		if function == nil {
			continue
		}
		if name, _ := function["name"].(string); name != "" {
			toolName = name
		}
		if deltaArgs, _ := function["arguments"].(string); deltaArgs != "" {
			arguments.WriteString(deltaArgs)
		}
	}

	if !sawToolCalls {
		t.Fatalf("stream body did not contain tool_calls events: %s", string(body))
	}
	if !sawFinish {
		t.Fatalf("stream body did not end with finish_reason=tool_calls: %s", string(body))
	}

	return callID, toolName, arguments.String()
}

func extractChatCompletionMessage(t *testing.T, body []byte) (string, []map[string]any) {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode chat completion response: %v\nbody=%s", err, string(body))
	}

	choices, _ := payload["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("chat completion response had no choices: %s", string(body))
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message == nil {
		t.Fatalf("chat completion choice had no message: %s", string(body))
	}

	content := extractMessageContent(message["content"])
	var toolCalls []map[string]any
	if rawToolCalls, _ := message["tool_calls"].([]any); len(rawToolCalls) > 0 {
		toolCalls = make([]map[string]any, 0, len(rawToolCalls))
		for _, item := range rawToolCalls {
			if m, _ := item.(map[string]any); m != nil {
				toolCalls = append(toolCalls, m)
			}
		}
	}

	return content, toolCalls
}

func extractMessageContent(raw any) string {
	switch content := raw.(type) {
	case string:
		return content
	case []any:
		var parts []string
		for _, item := range content {
			part, _ := item.(map[string]any)
			if part == nil {
				continue
			}
			if text, _ := part["text"].(string); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return fmt.Sprint(raw)
	}
}

func extractToolCallName(toolCall map[string]any) string {
	if function, _ := toolCall["function"].(map[string]any); function != nil {
		if name, _ := function["name"].(string); name != "" {
			return name
		}
	}
	if custom, _ := toolCall["custom"].(map[string]any); custom != nil {
		if name, _ := custom["name"].(string); name != "" {
			return name
		}
	}
	return ""
}

func extractToolCallArguments(toolCall map[string]any) string {
	if function, _ := toolCall["function"].(map[string]any); function != nil {
		if arguments, _ := function["arguments"].(string); arguments != "" {
			return arguments
		}
	}
	if custom, _ := toolCall["custom"].(map[string]any); custom != nil {
		if input, _ := custom["input"].(string); input != "" {
			return input
		}
	}
	return ""
}
