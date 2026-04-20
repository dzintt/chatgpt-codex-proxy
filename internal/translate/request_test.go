package translate

import (
	"encoding/json"
	"testing"

	"chatgpt-codex-proxy/internal/codex"
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

func TestResponsesTranslationAcceptsModernFunctionToolShape(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "codex",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{{
				Role: "user",
				Content: openai.MessageContent{{
					Type: "text",
					Text: "Use the tool",
				}},
			}},
		},
		Tools: []openai.ToolDefinition{{
			Type:        "function",
			Name:        "ping_tool",
			Description: "Echo a message",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			Strict: true,
		}},
	}

	normalized, err := Responses(request, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if len(normalized.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(normalized.Tools))
	}
	if normalized.Tools[0].Name != "ping_tool" {
		t.Fatalf("tool name = %q, want ping_tool", normalized.Tools[0].Name)
	}
	if !normalized.Tools[0].Strict {
		t.Fatal("expected strict function tool passthrough")
	}
}

func TestResponsesTranslationAcceptsAssistantOutputTextReplay(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "codex",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{
				{
					Role: "assistant",
					Content: openai.MessageContent{{
						Type: "output_text",
						Text: "remembered response",
					}},
				},
				{
					Role: "user",
					Content: openai.MessageContent{{
						Type: "text",
						Text: "follow up",
					}},
				},
			},
		},
	}

	normalized, err := Responses(request, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if len(normalized.Input) != 2 {
		t.Fatalf("input len = %d, want 2", len(normalized.Input))
	}
	parts, ok := normalized.Input[0].Content.([]codex.ContentPart)
	if !ok || len(parts) != 1 {
		t.Fatalf("assistant replay content = %#v", normalized.Input[0].Content)
	}
	if parts[0].Type != "output_text" {
		t.Fatalf("assistant replay part type = %q, want output_text", parts[0].Type)
	}
}

func TestResponsesTranslationAcceptsInputFilePart(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "codex",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{{
				Role: "user",
				Content: openai.MessageContent{
					{Type: "text", Text: "Read the attachment"},
					{
						Type:     "input_file",
						FileData: "data:application/pdf;base64,SGVsbG8=",
						Filename: "sample.pdf",
					},
				},
			}},
		},
	}

	normalized, err := Responses(request, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if len(normalized.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(normalized.Input))
	}
	parts, ok := normalized.Input[0].Content.([]codex.ContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("file input content = %#v", normalized.Input[0].Content)
	}
	if parts[1].Type != "input_file" {
		t.Fatalf("file part type = %q, want input_file", parts[1].Type)
	}
	if parts[1].FileData == "" || parts[1].Filename != "sample.pdf" {
		t.Fatalf("file part = %#v", parts[1])
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
