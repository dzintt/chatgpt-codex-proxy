package accounts

import "time"

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusExpired  Status = "expired"
	StatusBanned   Status = "banned"
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

type CreditsSnapshot struct {
	HasCredits  bool     `json:"has_credits"`
	Unlimited   bool     `json:"unlimited"`
	Balance     *float64 `json:"balance,omitempty"`
	ActiveLimit string   `json:"active_limit,omitempty"`
}

type QuotaSnapshot struct {
	PlanType            string           `json:"plan_type"`
	RateLimit           RateLimitWindow  `json:"rate_limit"`
	SecondaryRateLimit  *RateLimitWindow `json:"secondary_rate_limit,omitempty"`
	CodeReviewRateLimit *RateLimitWindow `json:"code_review_rate_limit,omitempty"`
	Credits             *CreditsSnapshot `json:"credits,omitempty"`
	Source              string           `json:"source"`
	FetchedAt           time.Time        `json:"fetched_at"`
}

type Record struct {
	ID            string            `json:"id"`
	AccountID     string            `json:"account_id"`
	UserID        string            `json:"user_id,omitempty"`
	Email         string            `json:"email,omitempty"`
	PlanType      string            `json:"plan_type,omitempty"`
	Label         string            `json:"label,omitempty"`
	Status        Status            `json:"status"`
	LastError     string            `json:"last_error,omitempty"`
	Token         OAuthToken        `json:"token"`
	Cookies       map[string]string `json:"cookies,omitempty"`
	CachedQuota   *QuotaSnapshot    `json:"cached_quota,omitempty"`
	CooldownUntil *time.Time        `json:"cooldown_until,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type ContinuationInputItem struct {
	Role             string                    `json:"role,omitempty"`
	Type             string                    `json:"type,omitempty"`
	Content          []ContinuationContentPart `json:"content,omitempty"`
	CallID           string                    `json:"call_id,omitempty"`
	Name             string                    `json:"name,omitempty"`
	Input            string                    `json:"input,omitempty"`
	Arguments        string                    `json:"arguments,omitempty"`
	OutputText       string                    `json:"output,omitempty"`
	OutputContent    []ContinuationContentPart `json:"output_content,omitempty"`
	ID               string                    `json:"id,omitempty"`
	Status           string                    `json:"status,omitempty"`
	Summary          []ContinuationSummaryPart `json:"summary,omitempty"`
	EncryptedContent string                    `json:"encrypted_content,omitempty"`
}

type ContinuationContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type ContinuationSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ContinuationRecord struct {
	ResponseID   string
	AccountID    string
	UpstreamID   string
	TurnState    string
	Instructions string
	Model        string
	InputHistory []ContinuationInputItem
	ExpiresAt    time.Time
}

type DeviceLoginStatus string

const (
	DeviceLoginPending DeviceLoginStatus = "pending"
	DeviceLoginReady   DeviceLoginStatus = "ready"
	DeviceLoginExpired DeviceLoginStatus = "expired"
	DeviceLoginError   DeviceLoginStatus = "error"
)

type DeviceLoginRecord struct {
	LoginID   string            `json:"login_id"`
	AuthURL   string            `json:"auth_url"`
	UserCode  string            `json:"user_code"`
	Status    DeviceLoginStatus `json:"status"`
	Error     string            `json:"error,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type RotationStrategy string

const (
	RotationLeastUsed  RotationStrategy = "least_used"
	RotationRoundRobin RotationStrategy = "round_robin"
	RotationSticky     RotationStrategy = "sticky"
)
