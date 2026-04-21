package openai

import "encoding/json"

type ChatCompletionsRequest struct {
	Model             string                     `json:"model"`
	Messages          []ChatMessage              `json:"messages"`
	Stream            bool                       `json:"stream"`
	ReasoningEffort   string                     `json:"reasoning_effort,omitempty"`
	ServiceTier       string                     `json:"service_tier,omitempty"`
	Tools             []ToolDefinition           `json:"tools,omitempty"`
	ToolChoice        json.RawMessage            `json:"tool_choice,omitempty"`
	ResponseFormat    *ResponseFormat            `json:"response_format,omitempty"`
	Functions         []LegacyFunctionDefinition `json:"functions,omitempty"`
	FunctionCall      *LegacyFunctionCallChoice  `json:"function_call,omitempty"`
	N                 *int                       `json:"n,omitempty"`
	Temperature       *float64                   `json:"temperature,omitempty"`
	TopP              *float64                   `json:"top_p,omitempty"`
	MaxTokens         *int                       `json:"max_tokens,omitempty"`
	PresencePenalty   *float64                   `json:"presence_penalty,omitempty"`
	FrequencyPenalty  *float64                   `json:"frequency_penalty,omitempty"`
	Stop              json.RawMessage            `json:"stop,omitempty"`
	User              *string                    `json:"user,omitempty"`
	ParallelToolCalls *bool                      `json:"parallel_tool_calls,omitempty"`
	StreamOptions     json.RawMessage            `json:"stream_options,omitempty"`
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
	Type              string         `json:"type"`
	Function          *FunctionTool  `json:"function,omitempty"`
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	Strict            bool           `json:"strict,omitempty"`
	SearchContextSize string         `json:"search_context_size,omitempty"`
	UserLocation      map[string]any `json:"user_location,omitempty"`
}

type FunctionTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

type ToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function FunctionPayload `json:"function"`
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
	if data[0] == '"' {
		return json.Unmarshal(data, &r.String)
	}
	return json.Unmarshal(data, &r.Items)
}

type ResponsesInputItem struct {
	Type      string         `json:"type,omitempty"`
	Role      string         `json:"role,omitempty"`
	Content   MessageContent `json:"content,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Output    string         `json:"output,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
}
