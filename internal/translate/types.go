package translate

import "chatgpt-codex-proxy/internal/codex"

type Endpoint string

const (
	EndpointChat      Endpoint = "chat_completions"
	EndpointResponses Endpoint = "responses"
)

type CompatibilityWarning struct {
	Field    string
	Endpoint Endpoint
	Behavior string
	Detail   string
}

type NormalizedRequest struct {
	Endpoint              Endpoint
	Model                 string
	Instructions          string
	Input                 []codex.InputItem
	Stream                bool
	Tools                 []codex.Tool
	ToolChoice            any
	Text                  *codex.TextConfig
	Reasoning             *codex.Reasoning
	ServiceTier           string
	PreviousResponseID    string
	PromptCacheKey        string
	Include               []string
	TupleSchema           map[string]any
	CompatibilityWarnings []CompatibilityWarning
}

func (n NormalizedRequest) ToCodexRequest() codex.Request {
	return codex.Request{
		Model:              n.Model,
		Instructions:       n.Instructions,
		Input:              n.Input,
		Stream:             n.Stream,
		Store:              false,
		Tools:              n.Tools,
		ToolChoice:         n.ToolChoice,
		Text:               n.Text,
		Reasoning:          n.Reasoning,
		ServiceTier:        n.ServiceTier,
		PreviousResponseID: n.PreviousResponseID,
		PromptCacheKey:     n.PromptCacheKey,
		Include:            append([]string(nil), n.Include...),
	}
}

func (n NormalizedRequest) ToCodexWSCreatePayload() map[string]any {
	payload := map[string]any{
		"type":         "response.create",
		"model":        n.Model,
		"input":        n.Input,
		"instructions": n.Instructions,
	}
	if len(n.Tools) > 0 {
		payload["tools"] = n.Tools
	}
	if n.ToolChoice != nil {
		payload["tool_choice"] = n.ToolChoice
	}
	if n.Text != nil {
		payload["text"] = n.Text
	}
	if n.Reasoning != nil {
		payload["reasoning"] = n.Reasoning
	}
	if n.PreviousResponseID != "" {
		payload["previous_response_id"] = n.PreviousResponseID
	}
	if n.PromptCacheKey != "" {
		payload["prompt_cache_key"] = n.PromptCacheKey
	}
	if len(n.Include) > 0 {
		payload["include"] = append([]string(nil), n.Include...)
	}
	return payload
}
