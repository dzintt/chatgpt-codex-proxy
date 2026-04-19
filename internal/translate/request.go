package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/openai"
)

func ChatCompletions(req openai.ChatCompletionsRequest, defaultModel string) (NormalizedRequest, error) {
	model, reasoning, serviceTier := normalizeModel(req.Model, defaultModel, req.ReasoningEffort, req.ServiceTier)
	out := NormalizedRequest{
		Endpoint:    EndpointChat,
		Model:       model,
		Stream:      req.Stream,
		Tools:       normalizeTools(req.Tools),
		Reasoning:   reasoning,
		ServiceTier: serviceTier,
	}
	if len(req.Tools) > 0 {
		out.ToolChoice = normalizeToolChoice(req.ToolChoice)
	}
	if req.ResponseFormat != nil {
		text, err := normalizeChatResponseFormat(req.ResponseFormat)
		if err != nil {
			return NormalizedRequest{}, err
		}
		out.Text = text
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

	out.Instructions = strings.TrimSpace(strings.Join(instructions, "\n\n"))
	return out, nil
}

func Responses(req openai.ResponsesRequest, defaultModel string) (NormalizedRequest, error) {
	model, reasoning, serviceTier := normalizeModel(req.Model, defaultModel, "", req.ServiceTier)
	if req.Reasoning != nil && reasoning == nil {
		reasoning = &codex.Reasoning{
			Effort:  req.Reasoning.Effort,
			Summary: req.Reasoning.Summary,
		}
	}

	out := NormalizedRequest{
		Endpoint:           EndpointResponses,
		Model:              model,
		Instructions:       strings.TrimSpace(req.Instructions),
		Stream:             req.Stream,
		Tools:              normalizeTools(req.Tools),
		ToolChoice:         normalizeToolChoice(req.ToolChoice),
		Reasoning:          reasoning,
		ServiceTier:        serviceTier,
		PreviousResponseID: strings.TrimSpace(req.PreviousResponseID),
	}

	if req.Text != nil && req.Text.Format != nil {
		out.Text = &codex.TextConfig{
			Format: codex.TextFormat{
				Type:   req.Text.Format.Type,
				Name:   req.Text.Format.Name,
				Schema: req.Text.Format.Schema,
				Strict: req.Text.Format.Strict,
			},
		}
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

func normalizeTools(tools []openai.ToolDefinition) []codex.Tool {
	if len(tools) == 0 {
		return nil
	}

	result := make([]codex.Tool, 0, len(tools))
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			if tool.Function == nil {
				continue
			}
			result = append(result, codex.Tool{
				Type:        "function",
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
				Strict:      tool.Function.Strict,
			})
		case "web_search", "web_search_preview":
			result = append(result, codex.Tool{
				Type:              tool.Type,
				SearchContextSize: tool.SearchContextSize,
				UserLocation:      tool.UserLocation,
			})
		}
	}
	return result
}

func normalizeToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw)
	}
	return decoded
}

func normalizeChatResponseFormat(format *openai.ResponseFormat) (*codex.TextConfig, error) {
	if format == nil {
		return nil, nil
	}
	switch format.Type {
	case "", "text":
		return nil, nil
	case "json_schema":
		if format.JSONSchema == nil {
			return nil, fmt.Errorf("response_format.json_schema is required")
		}
		return &codex.TextConfig{
			Format: codex.TextFormat{
				Type:   "json_schema",
				Name:   format.JSONSchema.Name,
				Schema: format.JSONSchema.Schema,
				Strict: format.JSONSchema.Strict,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported response_format %q", format.Type)
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
		case "", "text", "input_text":
			out = append(out, codex.ContentPart{
				Type: "input_text",
				Text: part.Text,
			})
		case "image_url", "input_image":
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return nil, fmt.Errorf("image_url.url is required")
			}
			out = append(out, codex.ContentPart{
				Type:     "input_image",
				ImageURL: strings.TrimSpace(part.ImageURL.URL),
			})
		default:
			return nil, fmt.Errorf("unsupported_content_part: %s", part.Type)
		}
	}
	return out, nil
}

func flattenContent(content openai.MessageContent) (string, error) {
	var parts []string
	for _, part := range content {
		switch part.Type {
		case "", "text", "input_text":
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
		model = defaultModel
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
		reasoning = &codex.Reasoning{Effort: effort}
	}
	if model == "" || model == "codex" {
		model = defaultModel
	}
	return model, reasoning, strings.TrimSpace(serviceTier)
}
