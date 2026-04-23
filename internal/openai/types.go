package openai

import (
	"bytes"
	"encoding/json"

	"chatgpt-codex-proxy/internal/conversation"
)

type ChatCompletionsRequest struct {
	Model              string                     `json:"model"`
	Messages           []ChatMessage              `json:"messages"`
	Stream             bool                       `json:"stream"`
	ReasoningEffort    string                     `json:"reasoning_effort,omitempty"`
	ServiceTier        string                     `json:"service_tier,omitempty"`
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Tools              []ToolDefinition           `json:"tools,omitempty"`
	ToolChoice         json.RawMessage            `json:"tool_choice,omitempty"`
	ResponseFormat     *ResponseFormat            `json:"response_format,omitempty"`
	Functions          []LegacyFunctionDefinition `json:"functions,omitempty"`
	FunctionCall       *LegacyFunctionCallChoice  `json:"function_call,omitempty"`
	N                  *int                       `json:"n,omitempty"`
	Temperature        *float64                   `json:"temperature,omitempty"`
	TopP               *float64                   `json:"top_p,omitempty"`
	MaxTokens          *int                       `json:"max_tokens,omitempty"`
	PresencePenalty    *float64                   `json:"presence_penalty,omitempty"`
	FrequencyPenalty   *float64                   `json:"frequency_penalty,omitempty"`
	Stop               json.RawMessage            `json:"stop,omitempty"`
	User               *string                    `json:"user,omitempty"`
	ParallelToolCalls  *bool                      `json:"parallel_tool_calls,omitempty"`
	StreamOptions      json.RawMessage            `json:"stream_options,omitempty"`
}

type ChatMessage struct {
	Role         string           `json:"role"`
	Content      MessageContent   `json:"content"`
	Name         string           `json:"name,omitempty"`
	ToolCalls    []ToolCall       `json:"tool_calls,omitempty"`
	ToolCallID   string           `json:"tool_call_id,omitempty"`
	FunctionCall *FunctionPayload `json:"function_call,omitempty"`
}

type FunctionPayload struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type CustomToolPayload struct {
	Name  string `json:"name"`
	Input string `json:"input"`
}

type LegacyFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type LegacyFunctionCallChoice struct {
	Mode string
	Name string
}

func (l *LegacyFunctionCallChoice) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &l.Mode)
	}
	var raw struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.Name = raw.Name
	return nil
}

func (l *LegacyFunctionCallChoice) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	if l.Name != "" {
		return json.Marshal(map[string]string{"name": l.Name})
	}
	return json.Marshal(l.Mode)
}

func (l *LegacyFunctionCallChoice) IsZero() bool {
	return l == nil || (l.Mode == "" && l.Name == "")
}

type MessageContent []ContentPart

func (m *MessageContent) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = nil
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*m = MessageContent{{Type: "text", Text: text}}
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	*m = parts
	return nil
}

type ContentPart struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	ImageURL *ImageURLValue `json:"image_url,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	FileURL  string         `json:"file_url,omitempty"`
	FileData string         `json:"file_data,omitempty"`
	FileID   string         `json:"file_id,omitempty"`
	Filename string         `json:"filename,omitempty"`
}

type ImageURLValue struct {
	URL string `json:"url"`
}

func (i *ImageURLValue) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &i.URL)
	}
	type imageURLAlias ImageURLValue
	var alias imageURLAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*i = ImageURLValue(alias)
	return nil
}

type ToolDefinition struct {
	Type              string                     `json:"type"`
	Function          *FunctionTool              `json:"function,omitempty"`
	Name              string                     `json:"name,omitempty"`
	Description       string                     `json:"description,omitempty"`
	Parameters        map[string]any             `json:"parameters,omitempty"`
	Format            map[string]any             `json:"format,omitempty"`
	Strict            bool                       `json:"strict,omitempty"`
	SearchContextSize string                     `json:"search_context_size,omitempty"`
	UserLocation      map[string]any             `json:"user_location,omitempty"`
	ExtraFields       map[string]json.RawMessage `json:"-"`
}

func (t *ToolDefinition) UnmarshalJSON(data []byte) error {
	type alias ToolDefinition
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	delete(raw, "type")
	delete(raw, "function")
	delete(raw, "name")
	delete(raw, "description")
	delete(raw, "parameters")
	delete(raw, "format")
	delete(raw, "strict")
	delete(raw, "search_context_size")
	delete(raw, "user_location")

	extra := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		extra[key] = append(json.RawMessage(nil), value...)
	}
	decoded.ExtraFields = extra
	*t = ToolDefinition(decoded)
	return nil
}

func (t ToolDefinition) MarshalJSON() ([]byte, error) {
	type alias ToolDefinition
	base, err := json.Marshal(alias(t))
	if err != nil {
		return nil, err
	}
	if len(t.ExtraFields) == 0 {
		return base, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(base, &payload); err != nil {
		return nil, err
	}
	for key, value := range t.ExtraFields {
		if _, exists := payload[key]; exists {
			continue
		}
		payload[key] = append(json.RawMessage(nil), value...)
	}
	return json.Marshal(payload)
}

type FunctionTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

type ToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function FunctionPayload    `json:"function,omitempty"`
	Custom   *CustomToolPayload `json:"custom,omitempty"`
}

type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *JSONSchemaSpec `json:"json_schema,omitempty"`
}

type JSONSchemaSpec struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict,omitempty"`
}

type ResponsesRequest struct {
	Model              string           `json:"model"`
	Input              ResponsesInput   `json:"input"`
	Instructions       string           `json:"instructions,omitempty"`
	Stream             bool             `json:"stream"`
	Tools              []ToolDefinition `json:"tools,omitempty"`
	ToolChoice         json.RawMessage  `json:"tool_choice,omitempty"`
	PreviousResponseID string           `json:"previous_response_id,omitempty"`
	ServiceTier        string           `json:"service_tier,omitempty"`
	Text               *ResponsesText   `json:"text,omitempty"`
	Reasoning          *Reasoning       `json:"reasoning,omitempty"`
	Temperature        *float64         `json:"temperature,omitempty"`
	TopP               *float64         `json:"top_p,omitempty"`
	MaxOutputTokens    *int             `json:"max_output_tokens,omitempty"`
	ParallelToolCalls  *bool            `json:"parallel_tool_calls,omitempty"`
	Store              *bool            `json:"store,omitempty"`
	Background         *bool            `json:"background,omitempty"`
	User               *string          `json:"user,omitempty"`
	Metadata           map[string]any   `json:"metadata,omitempty"`
	StreamOptions      json.RawMessage  `json:"stream_options,omitempty"`
}

type ResponsesCompactRequest struct {
	Model              string         `json:"model"`
	Input              ResponsesInput `json:"input"`
	Instructions       string         `json:"instructions,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Text               *ResponsesText `json:"text,omitempty"`
	Reasoning          *Reasoning     `json:"reasoning,omitempty"`
}

type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type ResponsesText struct {
	Format *ResponsesTextFormat `json:"format,omitempty"`
}

type ResponsesTextFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
	Strict bool           `json:"strict,omitempty"`
}

type ResponsesInput struct {
	String string
	Items  []ResponsesInputItem
}

func (r *ResponsesInput) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case '"':
		return json.Unmarshal(data, &r.String)
	case '{':
		var item ResponsesInputItem
		if err := json.Unmarshal(data, &item); err != nil {
			return err
		}
		r.Items = []ResponsesInputItem{item}
		return nil
	}
	return json.Unmarshal(data, &r.Items)
}

type ResponsesInputItem struct {
	Type             string          `json:"type,omitempty"`
	Role             string          `json:"role,omitempty"`
	Phase            string          `json:"phase,omitempty"`
	Content          MessageContent  `json:"content,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	OutputText       string          `json:"-"`
	OutputContent    MessageContent  `json:"-"`
	Name             string          `json:"name,omitempty"`
	Input            string          `json:"input,omitempty"`
	Arguments        string          `json:"arguments,omitempty"`
	ID               string          `json:"id,omitempty"`
	Status           string          `json:"status,omitempty"`
	Summary          []ReasoningPart `json:"summary,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
}

func (r *ResponsesInputItem) UnmarshalJSON(data []byte) error {
	type alias ResponsesInputItem
	var raw struct {
		alias
		Output json.RawMessage `json:"output,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ResponsesInputItem(raw.alias)

	outputText, outputContent, err := decodeResponsesOutput(raw.Output)
	if err != nil {
		return err
	}
	r.OutputText = outputText
	r.OutputContent = outputContent
	return nil
}

func decodeResponsesOutput(raw json.RawMessage) (string, MessageContent, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil, nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", nil, err
		}
		return text, nil, nil
	}

	if content, ok := parseResponsesOutputContent(trimmed); ok {
		return "", content, nil
	}

	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return "", nil, err
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return "", nil, err
	}
	return string(normalized), nil, nil
}

func parseResponsesOutputContent(raw json.RawMessage) (MessageContent, bool) {
	var content MessageContent
	if err := json.Unmarshal(raw, &content); err == nil && len(content) > 0 {
		return content, true
	}

	var part ContentPart
	if err := json.Unmarshal(raw, &part); err == nil && isResponseOutputContentPart(part) {
		return MessageContent{part}, true
	}

	return nil, false
}

func isResponseOutputContentPart(part ContentPart) bool {
	switch part.Type {
	case "input_text", "output_text", "input_image", "input_file":
		return true
	default:
		return false
	}
}

type ReasoningPart = conversation.ReasoningPart
