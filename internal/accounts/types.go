package accounts

import "time"

type Status string

const (
	StatusActive         Status = "active"
	StatusDisabled       Status = "disabled"
	StatusExpired        Status = "expired"
	StatusRateLimited    Status = "rate_limited"
	StatusQuotaExhausted Status = "quota_exhausted"
)

type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type RateLimitWindow struct {
	Allowed            bool       `json:"allowed"`
	LimitReached       bool       `json:"limit_reached"`
	UsedPercent        *float64   `json:"used_percent,omitempty"`
	ResetAt            *time.Time `json:"reset_at,omitempty"`
	LimitWindowSeconds *int       `json:"limit_window_seconds,omitempty"`
}

type QuotaSnapshot struct {
	PlanType           string           `json:"plan_type"`
	RateLimit          RateLimitWindow  `json:"rate_limit"`
	SecondaryRateLimit *RateLimitWindow `json:"secondary_rate_limit,omitempty"`
	Source             string           `json:"source"`
	FetchedAt          time.Time        `json:"fetched_at"`
}

type LocalUsage struct {
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	RequestCount int64      `json:"request_count"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

type Record struct {
	ID          string            `json:"id"`
	AccountID   string            `json:"account_id"`
	Label       string            `json:"label,omitempty"`
	Status      Status            `json:"status"`
	LastError   string            `json:"last_error,omitempty"`
	Token       OAuthToken        `json:"token"`
	Cookies     map[string]string `json:"cookies,omitempty"`
	CachedQuota *QuotaSnapshot    `json:"cached_quota,omitempty"`
	LocalUsage  LocalUsage        `json:"local_usage"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type ContinuationRecord struct {
	ResponseID   string
	AccountID    string
	UpstreamID   string
	TurnState    string
	Instructions string
	Model        string
	ExpiresAt    time.Time
}

type DeviceLoginRecord struct {
	LoginID   string    `json:"login_id"`
	AuthURL   string    `json:"auth_url"`
	UserCode  string    `json:"user_code"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RotationStrategy string

const (
	RotationLeastUsed  RotationStrategy = "least_used"
	RotationRoundRobin RotationStrategy = "round_robin"
	RotationSticky     RotationStrategy = "sticky"
)
