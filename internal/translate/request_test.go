package translate

import (
	"encoding/json"
	"testing"

	"chatgpt-codex-proxy/internal/openai"
)

func TestChatCompletionsTranslation(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model:           "gpt-5.4",
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

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if normalized.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want explicit model passthrough", normalized.Model)
	}
	if normalized.Reasoning == nil || normalized.Reasoning.Effort != "high" {
		t.Fatalf("reasoning = %#v, want explicit effort override", normalized.Reasoning)
	}
	if len(normalized.Include) != 1 || normalized.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want reasoning.encrypted_content only", normalized.Include)
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
		Model:              "gpt-5.4",
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
					Type:       "function_call_output",
					CallID:     "call_1",
					OutputText: `{"ok":true}`,
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

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if normalized.Model != "gpt-5.4" {
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
	if len(normalized.Include) != 0 {
		t.Fatalf("include = %#v, want empty when reasoning disabled", normalized.Include)
	}
}

func TestResponsesTranslationUsesReasoningObject(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "gpt-5.4",
		Reasoning: &openai.Reasoning{
			Effort: "high",
		},
		Input: openai.ResponsesInput{
			String: "Think carefully",
		},
	}

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if normalized.Reasoning == nil {
		t.Fatalf("reasoning = nil, want explicit reasoning object to populate it")
	}
	if normalized.Reasoning.Effort != "high" {
		t.Fatalf("reasoning effort = %q, want high", normalized.Reasoning.Effort)
	}
	if normalized.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning summary = %q, want auto", normalized.Reasoning.Summary)
	}
	if len(normalized.Include) != 1 || normalized.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want reasoning.encrypted_content only", normalized.Include)
	}
}

func TestResponsesTranslationExtractsInstructionRolesFromInput(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model:        "gpt-5.4",
		Instructions: "Top-level instructions",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{
				{
					Role: "system",
					Content: openai.MessageContent{{
						Type: "text",
						Text: "System rules",
					}},
				},
				{
					Role: "developer",
					Content: openai.MessageContent{{
						Type: "text",
						Text: "Developer rules",
					}},
				},
				{
					Role: "user",
					Content: openai.MessageContent{{
						Type: "text",
						Text: "What does this project do?",
					}},
				},
			},
		},
	}

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if normalized.Instructions != "Top-level instructions\n\nSystem rules\n\nDeveloper rules" {
		t.Fatalf("instructions = %q", normalized.Instructions)
	}
	if len(normalized.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(normalized.Input))
	}
	if normalized.Input[0].Role != "user" {
		t.Fatalf("input role = %q, want user", normalized.Input[0].Role)
	}
}

func TestResponsesTranslationAcceptsModernFunctionToolShape(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "gpt-5.4",
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

	normalized, err := Responses(request, "gpt-5.4")
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

func TestChatCompletionsTranslationPreservesCustomToolShape(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{{
			Role:    "user",
			Content: openai.MessageContent{{Type: "text", Text: "Patch the file"}},
		}},
		Tools: []openai.ToolDefinition{{
			Type:        "custom",
			Name:        "ApplyPatch",
			Description: "Patch a file",
			Format: map[string]any{
				"type":       "grammar",
				"definition": "start: item+",
			},
			ExtraFields: map[string]any{
				"metadata": map[string]any{"origin": "cursor"},
			},
		}},
		ToolChoice: json.RawMessage(`{"type":"custom","name":"ApplyPatch"}`),
	}

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if len(normalized.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(normalized.Tools))
	}
	if normalized.Tools[0].Type != "custom" {
		t.Fatalf("tool type = %q, want custom", normalized.Tools[0].Type)
	}
	if normalized.Tools[0].Name != "ApplyPatch" {
		t.Fatalf("tool name = %q, want ApplyPatch", normalized.Tools[0].Name)
	}
	if got := normalized.Tools[0].Format["type"]; got != "grammar" {
		t.Fatalf("tool format type = %#v, want grammar", got)
	}
	metadata, _ := normalized.Tools[0].ExtraFields["metadata"].(map[string]any)
	if got := metadata["origin"]; got != "cursor" {
		t.Fatalf("tool metadata origin = %#v, want cursor", got)
	}
	if string(normalized.ToolChoice) != `{"type":"custom","name":"ApplyPatch"}` {
		t.Fatalf("tool choice = %s, want custom tool choice passthrough", string(normalized.ToolChoice))
	}
}

func TestChatCompletionsTranslationPreservesCustomToolCallsAndOutputs(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{
			{
				Role: "assistant",
				ToolCalls: []openai.ToolCall{{
					ID:   "call_patch",
					Type: "custom",
					Custom: &openai.CustomToolPayload{
						Name:  "ApplyPatch",
						Input: "*** Begin Patch\n*** Add File: dummy.txt\n+hello\n*** End Patch\n",
					},
				}},
			},
			{
				Role:       "tool",
				ToolCallID: "call_patch",
				Content:    openai.MessageContent{{Type: "text", Text: "patched"}},
			},
		},
	}

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if len(normalized.Input) != 2 {
		t.Fatalf("input len = %d, want 2", len(normalized.Input))
	}
	if normalized.Input[0].Type != "custom_tool_call" {
		t.Fatalf("input[0].Type = %q, want custom_tool_call", normalized.Input[0].Type)
	}
	if normalized.Input[0].Input == "" {
		t.Fatal("expected custom tool input to be preserved")
	}
	if normalized.Input[1].Type != "custom_tool_call_output" {
		t.Fatalf("input[1].Type = %q, want custom_tool_call_output", normalized.Input[1].Type)
	}
	if normalized.Input[1].OutputText != "patched" {
		t.Fatalf("input[1].OutputText = %q, want patched", normalized.Input[1].OutputText)
	}
}

func TestChatCompletionsTranslationSupportsWebSearchVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tool       openai.ToolDefinition
		toolChoice json.RawMessage
		assertTool func(*testing.T, openai.ToolDefinition, NormalizedRequest)
	}{
		{
			name: "native web_search tool",
			tool: openai.ToolDefinition{
				Type: "web_search",
			},
			toolChoice: json.RawMessage(`{"type":"web_search"}`),
			assertTool: func(t *testing.T, _ openai.ToolDefinition, normalized NormalizedRequest) {
				t.Helper()

				if normalized.Tools[0].Type != "web_search" {
					t.Fatalf("tool type = %q, want web_search", normalized.Tools[0].Type)
				}
			},
		},
		{
			name: "web_search_preview alias",
			tool: openai.ToolDefinition{
				Type:              "web_search_preview",
				SearchContextSize: "high",
				UserLocation: map[string]any{
					"type":    "approximate",
					"country": "US",
				},
			},
			toolChoice: json.RawMessage(`{"type":"web_search_preview"}`),
			assertTool: func(t *testing.T, _ openai.ToolDefinition, normalized NormalizedRequest) {
				t.Helper()

				if normalized.Tools[0].Type != "web_search" {
					t.Fatalf("tool type = %q, want web_search", normalized.Tools[0].Type)
				}
				if normalized.Tools[0].SearchContextSize != "high" {
					t.Fatalf("search_context_size = %q, want high", normalized.Tools[0].SearchContextSize)
				}
				if country := normalized.Tools[0].UserLocation["country"]; country != "US" {
					t.Fatalf("user_location.country = %#v, want US", country)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			request := openai.ChatCompletionsRequest{
				Model: "gpt-5.4",
				Messages: []openai.ChatMessage{{
					Role:    "user",
					Content: openai.MessageContent{{Type: "text", Text: "Search the web"}},
				}},
				Tools:      []openai.ToolDefinition{tc.tool},
				ToolChoice: tc.toolChoice,
			}

			normalized, err := ChatCompletions(request, "gpt-5.4")
			if err != nil {
				t.Fatalf("ChatCompletions() error = %v", err)
			}

			if len(normalized.Tools) != 1 {
				t.Fatalf("tools len = %d, want 1", len(normalized.Tools))
			}
			tc.assertTool(t, tc.tool, normalized)
			if string(normalized.ToolChoice) != `{"type":"web_search"}` {
				t.Fatalf("tool choice = %s, want web_search", string(normalized.ToolChoice))
			}
		})
	}
}

func TestResponsesTranslationAcceptsAssistantOutputTextReplay(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "gpt-5.4",
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

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if len(normalized.Input) != 2 {
		t.Fatalf("input len = %d, want 2", len(normalized.Input))
	}
	parts := normalized.Input[0].Content
	if len(parts) != 1 {
		t.Fatalf("assistant replay content = %#v", normalized.Input[0].Content)
	}
	if parts[0].Type != "output_text" {
		t.Fatalf("assistant replay part type = %q, want output_text", parts[0].Type)
	}
}

func TestResponsesTranslationAcceptsInputFilePart(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "gpt-5.4",
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

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if len(normalized.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(normalized.Input))
	}
	parts := normalized.Input[0].Content
	if len(parts) != 2 {
		t.Fatalf("file input content = %#v", normalized.Input[0].Content)
	}
	if parts[1].Type != "input_file" {
		t.Fatalf("file part type = %q, want input_file", parts[1].Type)
	}
	if parts[1].FileData == "" || parts[1].Filename != "sample.pdf" {
		t.Fatalf("file part = %#v", parts[1])
	}
}

func TestResponsesTranslationAcceptsReasoningItemReplay(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "gpt-5.4",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{{
				Type:             "reasoning",
				ID:               "rs_123",
				Status:           "completed",
				EncryptedContent: "encrypted-reasoning",
				Summary: []openai.ReasoningPart{{
					Type: "summary_text",
					Text: "Summarized reasoning",
				}},
				Content: openai.MessageContent{{
					Type: "reasoning_text",
					Text: "Full reasoning text",
				}},
			}},
		},
	}

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if len(normalized.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(normalized.Input))
	}
	item := normalized.Input[0]
	if item.Type != "reasoning" {
		t.Fatalf("item.Type = %q, want reasoning", item.Type)
	}
	if item.ID != "rs_123" {
		t.Fatalf("item.ID = %q, want rs_123", item.ID)
	}
	if item.Status != "completed" {
		t.Fatalf("item.Status = %q, want completed", item.Status)
	}
	if item.EncryptedContent != "encrypted-reasoning" {
		t.Fatalf("item.EncryptedContent = %q, want encrypted-reasoning", item.EncryptedContent)
	}
	if len(item.Summary) != 1 || item.Summary[0].Text != "Summarized reasoning" {
		t.Fatalf("item.Summary = %#v", item.Summary)
	}
	if len(item.Content) != 1 || item.Content[0].Type != "reasoning_text" {
		t.Fatalf("item.Content = %#v", item.Content)
	}
}

func TestUnsupportedContentPartRejected(t *testing.T) {
	t.Parallel()

	_, err := ChatCompletions(openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{{
			Role: "user",
			Content: openai.MessageContent{{
				Type: "audio",
			}},
		}},
	}, "gpt-5.4")
	if err == nil {
		t.Fatal("expected unsupported content part error")
	}
}

func TestUnsupportedModelRejected(t *testing.T) {
	t.Parallel()

	_, err := ChatCompletions(openai.ChatCompletionsRequest{
		Model: "codex",
		Messages: []openai.ChatMessage{{
			Role:    "user",
			Content: openai.MessageContent{{Type: "text", Text: "hello"}},
		}},
	}, "gpt-5.4")
	if err == nil {
		t.Fatal("expected unsupported model error")
	}
}

func TestChatCompletionsTranslationAcceptsLegacyFunctionsAndChoice(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{{
			Role:    "user",
			Content: openai.MessageContent{{Type: "text", Text: "Call the function"}},
		}},
		Functions: []openai.LegacyFunctionDefinition{{
			Name:       "lookup_weather",
			Parameters: map[string]any{"type": "object"},
		}},
		FunctionCall: &openai.LegacyFunctionCallChoice{Name: "lookup_weather"},
	}

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if len(normalized.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(normalized.Tools))
	}
	if normalized.Tools[0].Name != "lookup_weather" {
		t.Fatalf("tool name = %q, want lookup_weather", normalized.Tools[0].Name)
	}
	if string(normalized.ToolChoice) != `{"type":"function","name":"lookup_weather"}` {
		t.Fatalf("tool choice = %s", string(normalized.ToolChoice))
	}
}

func TestChatCompletionsTranslationPrefersModernToolsAndToolChoice(t *testing.T) {
	t.Parallel()

	rawToolChoice, err := json.Marshal(map[string]any{
		"type": "function",
		"name": "modern_tool",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	request := openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{{
			Role:    "user",
			Content: openai.MessageContent{{Type: "text", Text: "Use the tool"}},
		}},
		Tools: []openai.ToolDefinition{{
			Type: "function",
			Function: &openai.FunctionTool{
				Name:       "modern_tool",
				Parameters: map[string]any{"type": "object"},
			},
		}},
		Functions: []openai.LegacyFunctionDefinition{{
			Name: "legacy_tool",
		}},
		ToolChoice:   rawToolChoice,
		FunctionCall: &openai.LegacyFunctionCallChoice{Name: "legacy_tool"},
	}

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if len(normalized.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(normalized.Tools))
	}
	if normalized.Tools[0].Name != "modern_tool" {
		t.Fatalf("tool name = %q, want modern_tool", normalized.Tools[0].Name)
	}
	if string(normalized.ToolChoice) != `{"type":"function","name":"modern_tool"}` {
		t.Fatalf("tool choice = %s", string(normalized.ToolChoice))
	}
}

func TestChatCompletionsTranslationSupportsJSONObject(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{{
			Role:    "user",
			Content: openai.MessageContent{{Type: "text", Text: "Return JSON"}},
		}},
		ResponseFormat: &openai.ResponseFormat{Type: "json_object"},
	}

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if normalized.Text == nil || normalized.Text.Format.Type != "json_object" {
		t.Fatalf("text format = %#v, want json_object", normalized.Text)
	}
}

func TestChatCompletionsTranslationPreparesSchemaAndWarnings(t *testing.T) {
	t.Parallel()

	request := openai.ChatCompletionsRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatMessage{{
			Role:    "user",
			Content: openai.MessageContent{{Type: "text", Text: "Return structured data"}},
		}},
		ResponseFormat: &openai.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &openai.JSONSchemaSpec{
				Name: "tuple_payload",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"items": map[string]any{
							"type": "array",
							"prefixItems": []any{
								map[string]any{"type": "string"},
								map[string]any{"type": "object"},
							},
						},
						"nested": map[string]any{
							"type": "object",
						},
					},
				},
			},
		},
		Temperature:   ptrFloat(0.2),
		MaxTokens:     ptrInt(42),
		StreamOptions: json.RawMessage(`{"include_usage":true}`),
	}

	normalized, err := ChatCompletions(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if normalized.TupleSchema == nil {
		t.Fatal("expected tuple schema to be preserved")
	}
	schema := normalized.Text.Format.Schema
	rootProps, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema.properties = %#v", schema["properties"])
	}
	nested, _ := rootProps["nested"].(map[string]any)
	if nested["additionalProperties"] != false {
		t.Fatalf("nested additionalProperties = %#v, want false", nested["additionalProperties"])
	}
	items, _ := rootProps["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	if _, ok := itemProps["0"]; !ok {
		t.Fatalf("tuple item properties = %#v, want numeric keys", itemProps)
	}
	if len(normalized.CompatibilityWarnings) != 3 {
		t.Fatalf("warnings len = %d, want 3", len(normalized.CompatibilityWarnings))
	}
}

func TestResponsesTranslationPreparesSchemaAndWarnings(t *testing.T) {
	t.Parallel()

	request := openai.ResponsesRequest{
		Model: "gpt-5.4",
		Input: openai.ResponsesInput{
			Items: []openai.ResponsesInputItem{{
				Role: "user",
				Content: openai.MessageContent{{
					Type: "text",
					Text: "Return structured data",
				}},
			}},
		},
		Text: &openai.ResponsesText{
			Format: &openai.ResponsesTextFormat{
				Type: "json_schema",
				Name: "tuple_payload",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pair": map[string]any{
							"type": "array",
							"prefixItems": []any{
								map[string]any{"type": "string"},
								map[string]any{"type": "number"},
							},
						},
					},
				},
			},
		},
		TopP:              ptrFloat(0.9),
		ParallelToolCalls: ptrBool(true),
		Metadata:          map[string]any{"request_id": "abc"},
	}

	normalized, err := Responses(request, "gpt-5.4")
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}

	if normalized.TupleSchema == nil {
		t.Fatal("expected tuple schema to be preserved")
	}
	pair, _ := normalized.Text.Format.Schema["properties"].(map[string]any)["pair"].(map[string]any)
	if pair["type"] != "object" {
		t.Fatalf("pair.type = %#v, want object", pair["type"])
	}
	if len(normalized.CompatibilityWarnings) != 3 {
		t.Fatalf("warnings len = %d, want 3", len(normalized.CompatibilityWarnings))
	}
}

func ptrBool(value bool) *bool {
	return &value
}

func ptrFloat(value float64) *float64 {
	return &value
}

func ptrInt(value int) *int {
	return &value
}
