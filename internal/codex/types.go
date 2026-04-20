package codex

import "time"

type Request struct {
	Model              string      `json:"model"`
	Instructions       string      `json:"instructions,omitempty"`
	Input              []InputItem `json:"input"`
	Stream             bool        `json:"stream"`
	Store              bool        `json:"store"`
	Tools              []Tool      `json:"tools,omitempty"`
	ToolChoice         any         `json:"tool_choice,omitempty"`
	Text               *TextConfig `json:"text,omitempty"`
	Reasoning          *Reasoning  `json:"reasoning,omitempty"`
	ServiceTier        string      `json:"service_tier,omitempty"`
	PreviousResponseID string      `json:"previous_response_id,omitempty"`
	PromptCacheKey     string      `json:"prompt_cache_key,omitempty"`
	Include            []string    `json:"include,omitempty"`
}

type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type TextConfig struct {
	Format TextFormat `json:"format"`
}

type TextFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
	Strict bool           `json:"strict,omitempty"`
}

type Tool struct {
	Type              string         `json:"type"`
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	Strict            bool           `json:"strict,omitempty"`
	SearchContextSize string         `json:"search_context_size,omitempty"`
	UserLocation      map[string]any `json:"user_location,omitempty"`
}

type InputItem struct {
	Role      string `json:"role,omitempty"`
	Type      string `json:"type,omitempty"`
	Content   any    `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	ID        string `json:"id,omitempty"`
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

type ResponseEnvelope struct {
	ID     string           `json:"id"`
	Object string           `json:"object"`
	Model  string           `json:"model"`
	Output []map[string]any `json:"output,omitempty"`
	Usage  *Usage           `json:"usage,omitempty"`
	Status string           `json:"status,omitempty"`
}

type UsageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Allowed         bool         `json:"allowed"`
		LimitReached    bool         `json:"limit_reached"`
		PrimaryWindow   *UsageWindow `json:"primary_window"`
		SecondaryWindow *UsageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	CodeReviewRateLimit *struct {
		Allowed       bool         `json:"allowed"`
		LimitReached  bool         `json:"limit_reached"`
		PrimaryWindow *UsageWindow `json:"primary_window"`
	} `json:"code_review_rate_limit"`
}

type UsageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAfterSeconds  int     `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type ParsedRateLimits struct {
	Primary   *RateWindow
	Secondary *RateWindow
}

type RateWindow struct {
	UsedPercent        float64
	LimitWindowSeconds int
	ResetAt            time.Time
}
