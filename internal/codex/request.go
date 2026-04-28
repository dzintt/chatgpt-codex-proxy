package codex

import (
	"encoding/json"
	"strings"

	"chatgpt-codex-proxy/internal/conversation"
	"chatgpt-codex-proxy/internal/openai"
)

type Reasoning = openai.Reasoning
type TextFormat = openai.ResponsesTextFormat

// Tool shares the OpenAI tool definition schema.
type Tool = openai.ToolDefinition

type Request struct {
	Model              string          `json:"model"`
	Instructions       string          `json:"instructions,omitempty"`
	Input              []InputItem     `json:"input"`
	Stream             bool            `json:"stream"`
	Store              bool            `json:"store"`
	Tools              []Tool          `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	Text               *TextConfig     `json:"text,omitempty"`
	Reasoning          *Reasoning      `json:"reasoning,omitempty"`
	ServiceTier        string          `json:"service_tier,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	PromptCacheKey     string          `json:"prompt_cache_key,omitempty"`
	Include            []string        `json:"include,omitempty"`
}

type CompactRequest struct {
	Model        string      `json:"model"`
	Instructions string      `json:"instructions,omitempty"`
	Input        []InputItem `json:"input"`
	Text         *TextConfig `json:"text,omitempty"`
	Reasoning    *Reasoning  `json:"reasoning,omitempty"`
}

type CompactResponse struct {
	ID        string           `json:"id,omitempty"`
	Object    string           `json:"object,omitempty"`
	CreatedAt int64            `json:"created_at,omitempty"`
	Output    []map[string]any `json:"output,omitempty"`
	Usage     map[string]any   `json:"usage,omitempty"`
}

type TextConfig struct {
	Format TextFormat `json:"format"`
}

type InputItem struct {
	Role             string                 `json:"role,omitempty"`
	Type             string                 `json:"type,omitempty"`
	Phase            string                 `json:"phase,omitempty"`
	Content          []ContentPart          `json:"content,omitempty"`
	CallID           string                 `json:"call_id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Input            string                 `json:"input,omitempty"`
	Arguments        string                 `json:"arguments,omitempty"`
	OutputText       string                 `json:"-"`
	OutputContent    []ContentPart          `json:"-"`
	ID               string                 `json:"id,omitempty"`
	Status           string                 `json:"status,omitempty"`
	Summary          []openai.ReasoningPart `json:"summary,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
}

func (i InputItem) MarshalJSON() ([]byte, error) {
	payload := map[string]any{}
	if i.Role != "" {
		payload["role"] = i.Role
	}
	if i.Type != "" {
		payload["type"] = i.Type
	}
	if i.Phase != "" {
		payload["phase"] = i.Phase
	}
	appendInputItemContent(payload, i)
	if i.CallID != "" {
		payload["call_id"] = i.CallID
	}
	if i.Name != "" {
		payload["name"] = i.Name
	}
	if i.Input != "" {
		payload["input"] = i.Input
	}
	if i.Arguments != "" {
		payload["arguments"] = i.Arguments
	}
	appendInputItemOutput(payload, i)
	if i.ID != "" {
		payload["id"] = i.ID
	}
	if i.Status != "" {
		payload["status"] = i.Status
	}
	if i.Type == "reasoning" {
		payload["summary"] = append(make([]openai.ReasoningPart, 0, len(i.Summary)), i.Summary...)
	} else if len(i.Summary) > 0 {
		payload["summary"] = i.Summary
	}
	if i.EncryptedContent != "" {
		payload["encrypted_content"] = i.EncryptedContent
	}
	return json.Marshal(payload)
}

func appendInputItemContent(payload map[string]any, item InputItem) {
	if len(item.Content) == 0 {
		return
	}
	if item.Role == "" {
		payload["content"] = item.Content
		return
	}

	textParts := make([]string, 0, len(item.Content))
	for _, part := range item.Content {
		switch part.Type {
		case "", "text", "input_text", "output_text", "reasoning_text":
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		default:
			payload["content"] = item.Content
			return
		}
	}
	payload["content"] = strings.Join(textParts, "\n")
}

func appendInputItemOutput(payload map[string]any, item InputItem) {
	if len(item.OutputContent) > 0 {
		payload["output"] = item.OutputContent
		return
	}
	if item.OutputText != "" {
		payload["output"] = item.OutputText
		return
	}
	if item.Type == "function_call_output" || item.Type == "custom_tool_call_output" {
		payload["output"] = ""
	}
}

type ContentPart = conversation.ContentPart

type Usage struct {
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CachedTokens    *int64 `json:"cached_tokens,omitempty"`
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
}

type StreamEvent struct {
	Type string
	Raw  map[string]any
}
