package translate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/jsonutil"
	"chatgpt-codex-proxy/internal/openai"
)

const defaultInstructions = "You are a helpful assistant."

func ChatCompletions(req openai.ChatCompletionsRequest, defaultModel string) (NormalizedRequest, error) {
	model, reasoning, serviceTier := normalizeModel(req.Model, defaultModel, req.ReasoningEffort, req.ServiceTier)
	tools := req.Tools
	if len(tools) == 0 && len(req.Functions) > 0 {
		tools = legacyFunctionsAsTools(req.Functions)
	}
	out := NormalizedRequest{
		Endpoint:              EndpointChat,
		Model:                 model,
		Stream:                req.Stream,
		Tools:                 normalizeTools(tools),
		Reasoning:             reasoning,
		ServiceTier:           serviceTier,
		Include:               []string{"reasoning.encrypted_content"},
		CompatibilityWarnings: collectChatCompatibilityWarnings(req),
	}
	if len(req.Tools) > 0 {
		out.ToolChoice = normalizeToolChoice(req.ToolChoice)
	} else if choice := normalizeLegacyFunctionChoice(req.FunctionCall); choice != nil {
		out.ToolChoice = choice
	}
	if req.ResponseFormat != nil {
		text, tupleSchema, err := normalizeChatResponseFormat(req.ResponseFormat)
		if err != nil {
			return NormalizedRequest{}, err
		}
		out.Text = text
		out.TupleSchema = tupleSchema
	}

	var instructions []string
	for _, message := range req.Messages {
		switch message.Role {
		case "system", "developer":
			text, err := flattenContent(message.Content)
			if err != nil {
				return NormalizedRequest{}, err
			}
			if strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
		case "user", "assistant":
			if len(message.ToolCalls) > 0 {
				if parts := normalizeContentParts(message.Content); len(parts) > 0 {
					out.Input = append(out.Input, codex.InputItem{
						Role:    message.Role,
						Content: parts,
					})
				}
				for _, call := range message.ToolCalls {
					out.Input = append(out.Input, codex.InputItem{
						Type:      "function_call",
						CallID:    call.ID,
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					})
				}
				continue
			}
			if message.FunctionCall != nil {
				out.Input = append(out.Input, codex.InputItem{
					Type:      "function_call",
					Name:      message.FunctionCall.Name,
					Arguments: message.FunctionCall.Arguments,
				})
				continue
			}
			parts, err := normalizeContentPartsChecked(message.Content)
			if err != nil {
				return NormalizedRequest{}, err
			}
			out.Input = append(out.Input, codex.InputItem{
				Role:    message.Role,
				Content: parts,
			})
		case "tool":
			text, err := flattenContent(message.Content)
			if err != nil {
				return NormalizedRequest{}, err
			}
			out.Input = append(out.Input, codex.InputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: text,
			})
		default:
			return NormalizedRequest{}, fmt.Errorf("unsupported role %q", message.Role)
		}
	}

	out.Instructions = jsonutil.FirstNonEmpty(strings.TrimSpace(strings.Join(instructions, "\n\n")), defaultInstructions)
	return out, nil
}

func Responses(req openai.ResponsesRequest, defaultModel string) (NormalizedRequest, error) {
	model, reasoning, serviceTier := normalizeModel(req.Model, defaultModel, "", req.ServiceTier)
	if req.Reasoning != nil && reasoning == nil {
		reasoning = &codex.Reasoning{
			Effort:  req.Reasoning.Effort,
			Summary: req.Reasoning.Summary,
		}
		if reasoning.Effort != "" && reasoning.Summary == "" {
			reasoning.Summary = "auto"
		}
	}

	out := NormalizedRequest{
		Endpoint:              EndpointResponses,
		Model:                 model,
		Instructions:          jsonutil.FirstNonEmpty(strings.TrimSpace(req.Instructions), defaultInstructions),
		Stream:                req.Stream,
		Tools:                 normalizeTools(req.Tools),
		ToolChoice:            normalizeToolChoice(req.ToolChoice),
		Reasoning:             reasoning,
		ServiceTier:           serviceTier,
		PreviousResponseID:    strings.TrimSpace(req.PreviousResponseID),
		Include:               []string{"reasoning.encrypted_content"},
		CompatibilityWarnings: collectResponsesCompatibilityWarnings(req),
	}

	if req.Text != nil && req.Text.Format != nil {
		var tupleSchema map[string]any
		format := codex.TextFormat{
			Type: req.Text.Format.Type,
			Name: req.Text.Format.Name,
		}
		if req.Text.Format.Type == "json_schema" {
			prepared, original := PrepareSchema(req.Text.Format.Schema)
			format.Schema = prepared
			format.Strict = req.Text.Format.Strict
			tupleSchema = original
		} else {
			format.Schema = req.Text.Format.Schema
			format.Strict = req.Text.Format.Strict
		}
		out.Text = &codex.TextConfig{
			Format: format,
		}
		out.TupleSchema = tupleSchema
	}

	if req.Input.String != "" {
		out.Input = append(out.Input, codex.InputItem{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: req.Input.String,
			}},
		})
	}

	for _, item := range req.Input.Items {
		switch {
		case item.Type == "function_call":
			out.Input = append(out.Input, codex.InputItem{
				Type:      "function_call",
				CallID:    item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		case item.Type == "function_call_output":
			out.Input = append(out.Input, codex.InputItem{
				Type:   "function_call_output",
				CallID: item.CallID,
				Output: item.Output,
			})
		default:
			parts, err := normalizeContentPartsChecked(item.Content)
			if err != nil {
				return NormalizedRequest{}, err
			}
			role := item.Role
			if role == "" {
				role = "user"
			}
			out.Input = append(out.Input, codex.InputItem{
				Role:    role,
				Content: parts,
			})
		}
	}

	return out, nil
}

func legacyFunctionsAsTools(functions []openai.LegacyFunctionDefinition) []openai.ToolDefinition {
	if len(functions) == 0 {
		return nil
	}
	tools := make([]openai.ToolDefinition, 0, len(functions))
	for _, function := range functions {
		tools = append(tools, openai.ToolDefinition{
			Type: "function",
			Function: &openai.FunctionTool{
				Name:        function.Name,
				Description: function.Description,
				Parameters:  function.Parameters,
			},
		})
	}
	return tools
}

func normalizeTools(tools []openai.ToolDefinition) []codex.Tool {
	if len(tools) == 0 {
		return nil
	}

	result := make([]codex.Tool, 0, len(tools))
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			function := tool.Function
			if function == nil && strings.TrimSpace(tool.Name) != "" {
				function = &openai.FunctionTool{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
					Strict:      tool.Strict,
				}
			}
			if function == nil {
				continue
			}
			result = append(result, codex.Tool{
				Type:        "function",
				Name:        function.Name,
				Description: function.Description,
				Parameters:  NormalizeSchema(function.Parameters),
				Strict:      function.Strict,
			})
		case "web_search", "web_search_preview":
			result = append(result, codex.Tool{
				Type:              "web_search",
				SearchContextSize: tool.SearchContextSize,
				UserLocation:      tool.UserLocation,
			})
		}
	}
	return result
}

func normalizeToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		return mustJSONString(mode)
	}
	var choice struct {
		Type     string `json:"type"`
		Name     string `json:"name,omitempty"`
		Function *struct {
			Name string `json:"name"`
		} `json:"function,omitempty"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	switch strings.TrimSpace(choice.Type) {
	case "function":
		name := strings.TrimSpace(choice.Name)
		if name == "" && choice.Function != nil {
			name = strings.TrimSpace(choice.Function.Name)
		}
		if name != "" {
			data, _ := json.Marshal(struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}{
				Type: "function",
				Name: name,
			})
			return data
		}
	case "web_search", "web_search_preview":
		data, _ := json.Marshal(struct {
			Type string `json:"type"`
		}{
			Type: "web_search",
		})
		return data
	}
	return append(json.RawMessage(nil), raw...)
}

func normalizeLegacyFunctionChoice(choice *openai.LegacyFunctionCallChoice) json.RawMessage {
	if choice == nil || choice.IsZero() {
		return nil
	}
	switch strings.TrimSpace(choice.Mode) {
	case "none", "auto":
		return mustJSONString(choice.Mode)
	}
	if name := strings.TrimSpace(choice.Name); name != "" {
		data, _ := json.Marshal(struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}{
			Type: "function",
			Name: name,
		})
		return data
	}
	return nil
}

func normalizeChatResponseFormat(format *openai.ResponseFormat) (*codex.TextConfig, map[string]any, error) {
	if format == nil {
		return nil, nil, nil
	}
	switch format.Type {
	case "", "text":
		return nil, nil, nil
	case "json_object":
		return &codex.TextConfig{
			Format: codex.TextFormat{Type: "json_object"},
		}, nil, nil
	case "json_schema":
		if format.JSONSchema == nil {
			return nil, nil, fmt.Errorf("response_format.json_schema is required")
		}
		prepared, tupleSchema := PrepareSchema(format.JSONSchema.Schema)
		return &codex.TextConfig{
			Format: codex.TextFormat{
				Type:   "json_schema",
				Name:   format.JSONSchema.Name,
				Schema: prepared,
				Strict: format.JSONSchema.Strict,
			},
		}, tupleSchema, nil
	default:
		return nil, nil, fmt.Errorf("unsupported response_format %q", format.Type)
	}
}

func normalizeContentParts(parts openai.MessageContent) []codex.ContentPart {
	out, _ := normalizeContentPartsChecked(parts)
	return out
}

func normalizeContentPartsChecked(parts openai.MessageContent) ([]codex.ContentPart, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]codex.ContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "", "text", "input_text", "output_text":
			contentType := "input_text"
			if part.Type == "output_text" {
				contentType = "output_text"
			}
			out = append(out, codex.ContentPart{
				Type: contentType,
				Text: part.Text,
			})
		case "image_url", "input_image":
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return nil, fmt.Errorf("image_url.url is required")
			}
			out = append(out, codex.ContentPart{
				Type:     "input_image",
				ImageURL: strings.TrimSpace(part.ImageURL.URL),
				Detail:   strings.TrimSpace(part.Detail),
			})
		case "input_file":
			if strings.TrimSpace(part.FileData) == "" && strings.TrimSpace(part.FileURL) == "" && strings.TrimSpace(part.FileID) == "" {
				return nil, fmt.Errorf("input_file requires file_data, file_url, or file_id")
			}
			out = append(out, codex.ContentPart{
				Type:     "input_file",
				Detail:   strings.TrimSpace(part.Detail),
				FileURL:  strings.TrimSpace(part.FileURL),
				FileData: strings.TrimSpace(part.FileData),
				FileID:   strings.TrimSpace(part.FileID),
				Filename: strings.TrimSpace(part.Filename),
			})
		default:
			return nil, fmt.Errorf("unsupported_content_part: %s", part.Type)
		}
	}
	return out, nil
}

func mustJSONString(value string) json.RawMessage {
	return json.RawMessage(strconv.Quote(value))
}

func flattenContent(content openai.MessageContent) (string, error) {
	var parts []string
	for _, part := range content {
		switch part.Type {
		case "", "text", "input_text", "output_text":
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		case "image_url", "input_image":
			return "", fmt.Errorf("unsupported_content_part: %s", part.Type)
		default:
			return "", fmt.Errorf("unsupported_content_part: %s", part.Type)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func normalizeModel(rawModel, defaultModel, reasoningEffort, serviceTier string) (string, *codex.Reasoning, string) {
	model := strings.TrimSpace(rawModel)
	if model == "" || model == "codex" {
		model = openai.ResolveDefaultModel(defaultModel)
	}

	effort := strings.TrimSpace(reasoningEffort)
	switch {
	case strings.HasSuffix(model, "-xhigh"):
		model = strings.TrimSuffix(model, "-xhigh")
		if effort == "" {
			effort = "high"
		}
	case strings.HasSuffix(model, "-high"):
		model = strings.TrimSuffix(model, "-high")
		if effort == "" {
			effort = "high"
		}
	case strings.HasSuffix(model, "-medium"):
		model = strings.TrimSuffix(model, "-medium")
		if effort == "" {
			effort = "medium"
		}
	case strings.HasSuffix(model, "-low"):
		model = strings.TrimSuffix(model, "-low")
		if effort == "" {
			effort = "low"
		}
	}

	var reasoning *codex.Reasoning
	if effort != "" {
		reasoning = &codex.Reasoning{Effort: effort, Summary: "auto"}
	}
	if model == "" || model == "codex" {
		model = openai.ResolveDefaultModel(defaultModel)
	}
	return model, reasoning, strings.TrimSpace(serviceTier)
}

func collectChatCompatibilityWarnings(req openai.ChatCompletionsRequest) []CompatibilityWarning {
	var warnings []CompatibilityWarning
	if req.N != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "n"))
	}
	if req.Temperature != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "temperature"))
	}
	if req.TopP != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "top_p"))
	}
	if req.MaxTokens != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "max_tokens"))
	}
	if req.PresencePenalty != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "presence_penalty"))
	}
	if req.FrequencyPenalty != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "frequency_penalty"))
	}
	if len(req.Stop) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "stop"))
	}
	if req.User != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "user"))
	}
	if req.ParallelToolCalls != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "parallel_tool_calls"))
	}
	if len(req.StreamOptions) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "stream_options"))
	}
	return warnings
}

func collectResponsesCompatibilityWarnings(req openai.ResponsesRequest) []CompatibilityWarning {
	var warnings []CompatibilityWarning
	if req.Temperature != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "temperature"))
	}
	if req.TopP != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "top_p"))
	}
	if req.MaxOutputTokens != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "max_output_tokens"))
	}
	if req.ParallelToolCalls != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "parallel_tool_calls"))
	}
	if req.Store != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "store"))
	}
	if req.Background != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "background"))
	}
	if req.User != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "user"))
	}
	if len(req.Metadata) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "metadata"))
	}
	if len(req.StreamOptions) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "stream_options"))
	}
	return warnings
}

func unsupportedFieldWarning(endpoint Endpoint, field string) CompatibilityWarning {
	return CompatibilityWarning{
		Field:    field,
		Endpoint: endpoint,
		Behavior: "ignored_with_warning",
		Detail:   "field is accepted for compatibility but not applied in this proxy",
	}
}
