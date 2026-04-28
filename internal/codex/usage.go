package codex

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
