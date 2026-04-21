package codex

import (
	"encoding/json"
	"time"

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

type TextConfig struct {
	Format TextFormat `json:"format"`
}

type InputItem struct {
	Role      string        `json:"role,omitempty"`
	Type      string        `json:"type,omitempty"`
	Content   []ContentPart `json:"content,omitempty"`
	CallID    string        `json:"call_id,omitempty"`
	Name      string        `json:"name,omitempty"`
	Arguments string        `json:"arguments,omitempty"`
	Output    string        `json:"output,omitempty"`
	ID        string        `json:"id,omitempty"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CachedTokens    int64 `json:"cached_tokens,omitempty"`
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
}

type StreamEvent struct {
	Type string
	Raw  map[string]any
}

type UsageResponseRateLimit struct {
	Allowed         bool         `json:"allowed"`
	LimitReached    bool         `json:"limit_reached"`
	PrimaryWindow   *UsageWindow `json:"primary_window"`
	SecondaryWindow *UsageWindow `json:"secondary_window,omitempty"`
}

type UsageResponseCodeReviewRateLimit struct {
	Allowed       bool         `json:"allowed"`
	LimitReached  bool         `json:"limit_reached"`
	PrimaryWindow *UsageWindow `json:"primary_window"`
}

type UsageResponseCredits struct {
	HasCredits  *bool    `json:"has_credits,omitempty"`
	Unlimited   *bool    `json:"unlimited,omitempty"`
	Balance     *float64 `json:"balance,omitempty"`
	ActiveLimit *string  `json:"active_limit,omitempty"`
}

type UsageResponse struct {
	PlanType            string                            `json:"plan_type"`
	RateLimit           UsageResponseRateLimit            `json:"rate_limit"`
	CodeReviewRateLimit *UsageResponseCodeReviewRateLimit `json:"code_review_rate_limit,omitempty"`
	Credits             *UsageResponseCredits             `json:"credits,omitempty"`
}

type UsageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAfterSeconds  int     `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type RateWindow struct {
	UsedPercent        float64
	LimitWindowSeconds int
	ResetAt            time.Time
}
