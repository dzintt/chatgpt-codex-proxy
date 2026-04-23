package translate

import "chatgpt-codex-proxy/internal/codex"

type Endpoint string

const (
	EndpointChat             Endpoint = "chat_completions"
	EndpointResponses        Endpoint = "responses"
	EndpointResponsesCompact Endpoint = "responses_compact"
)

type CompatibilityWarning struct {
	Field    string
	Endpoint Endpoint
	Behavior string
	Detail   string
}

// NormalizedRequest extends the canonical codex request with translation-only
// metadata that the proxy needs while mapping OpenAI payloads.
type NormalizedRequest struct {
	Endpoint Endpoint
	codex.Request
	ModelExplicit         bool
	TupleSchema           map[string]any
	CompatibilityWarnings []CompatibilityWarning
}

func (n NormalizedRequest) ToCodexRequest() codex.Request {
	return n.Request
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
	if len(n.ToolChoice) > 0 {
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

type NormalizedCompactRequest struct {
	codex.CompactRequest
	ModelExplicit         bool
	PreviousResponseID    string
	TupleSchema           map[string]any
	CompatibilityWarnings []CompatibilityWarning
}

func (n NormalizedCompactRequest) ToCodexCompactRequest() codex.CompactRequest {
	return n.CompactRequest
}
