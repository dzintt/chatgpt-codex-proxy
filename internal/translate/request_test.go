package translate

import (
	"encoding/json"
	"testing"

	"chatgpt-codex-proxy/internal/openai"
)

func TestChatCompletionsTranslation(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model:           "codex-low",
		ReasoningEffort: "high",
		Messages: []openai.ChatMessage{
			{
				Role:    "system",
				Content: openai.MessageContent{{Type: "text", Text: "System rules"}},
			},
			{
				Role:    "developer",
				Content: openai.MessageContent{{Type: "text", Text: "Developer rules"}},
			},
			{
				Role: "user",
				Content: openai.MessageContent{
					{Type: "text", Text: "Hello"},
					{Type: "image_url", ImageURL: &openai.ImageURLValue{URL: "https://example.com/image.png"}},
				},
			},
			{
				Role: "assistant",
				ToolCalls: []openai.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: openai.FunctionPayload{
						Name:      "lookup_weather",
						Arguments: `{"city":"SF"}`,
					},
				}},
			},
			{
				Role:       "tool",
				ToolCallID: "call_1",
				Content:    openai.MessageContent{{Type: "text", Text: `{"temp":72}`}},
			},
		},
		Tools: []openai.ToolDefinition{{
			Type: "function",
			Function: &openai.FunctionTool{
				Name:       "lookup_weather",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	}

	normalized, err := ChatCompletions(request, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if normalized.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q, want default alias expansion", normalized.Model)
	}
	if normalized.Reasoning == nil || normalized.Reasoning.Effort != "high" {
		t.Fatalf("reasoning = %#v, want explicit effort override", normalized.Reasoning)
	}
	if normalized.Instructions != "System rules\n\nDeveloper rules" {
		t.Fatalf("instructions = %q", normalized.Instructions)
	}
	if len(normalized.Input) != 3 {
		t.Fatalf("input len = %d, want 3", len(normalized.Input))
	}
	if normalized.Input[0].Role != "user" {
		t.Fatalf("first input role = %q, want user", normalized.Input[0].Role)
	}
	if normalized.Input[1].Type != "function_call" {
		t.Fatalf("second input type = %q, want function_call", normalized.Input[1].Type)
	}
	if normalized.Input[2].Type != "function_call_output" {
		t.Fatalf("third input type = %q, want function_call_output", normalized.Input[2].Type)
	}
	if len(normalized.Tools) != 1 || normalized.Tools[0].Type != "function" {
		t.Fatalf("tools = %#v", normalized.Tools)
	}
}

func TestResponsesTranslation(t *testing.T) {
	t.Parallel()

	toolChoice, _ := json.Marshal(map[string]any{"type": "function", "name": "lookup"})
	request := openai.ResponsesRequest{
		Model:              "codex",
		PreviousResponseID: "resp_prev",
		Instructions:       "Be terse",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{
				{
					Role: "user",
					Content: openai.MessageContent{
						{Type: "text", Text: "Summarize this"},
					},
				},
				{
					Type:   "function_call_output",
					CallID: "call_1",
					Output: `{"ok":true}`,
				},
			},
		},
		ToolChoice: toolChoice,
		Text: &openai.ResponsesText{
			Format: &openai.ResponsesTextFormat{
				Type:   "json_schema",
				Name:   "summary",
				Schema: map[string]any{"type": "object"},
				Strict: true,
			},
		},
	}

	normalized, err := Responses(request, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if normalized.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q", normalized.Model)
	}
	if normalized.PreviousResponseID != "resp_prev" {
		t.Fatalf("previous_response_id = %q", normalized.PreviousResponseID)
	}
	if normalized.Text == nil || normalized.Text.Format.Type != "json_schema" {
		t.Fatalf("text format = %#v", normalized.Text)
	}
	if len(normalized.Input) != 2 {
		t.Fatalf("input len = %d", len(normalized.Input))
	}
}

func TestUnsupportedContentPartRejected(t *testing.T) {
	t.Parallel()

	_, err := ChatCompletions(openai.ChatCompletionsRequest{
		Model: "codex",
		Messages: []openai.ChatMessage{{
			Role: "user",
			Content: openai.MessageContent{{
				Type: "audio",
			}},
		}},
	}, "gpt-5.3-codex")
	if err == nil {
		t.Fatal("expected unsupported content part error")
	}
}
