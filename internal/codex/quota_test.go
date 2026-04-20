package codex

import (
	"net/http"
	"testing"
	"time"
)

func TestParseQuotaFromHeadersIncludesSecondaryAndCredits(t *testing.T) {
	t.Parallel()

	headers := http.Header{}
	headers.Set("X-Codex-Primary-Used-Percent", "82.5")
	headers.Set("X-Codex-Primary-Window-Minutes", "300")
	headers.Set("X-Codex-Primary-Reset-At", "4102444800")
	headers.Set("X-Codex-Secondary-Used-Percent", "12")
	headers.Set("X-Codex-Secondary-Window-Minutes", "10080")
	headers.Set("X-Codex-Secondary-Reset-At", "4103049600")
	headers.Set("X-Codex-Credits-Has-Credits", "true")
	headers.Set("X-Codex-Credits-Unlimited", "false")
	headers.Set("X-Codex-Credits-Balance", "19.5")
	headers.Set("X-Codex-Active-Limit", "plus")

	snapshot := ParseQuotaFromHeaders(headers)
	if snapshot == nil {
		t.Fatal("ParseQuotaFromHeaders() = nil")
	}
	if snapshot.SecondaryRateLimit == nil {
		t.Fatal("expected secondary rate limit")
	}
	if snapshot.Credits == nil {
		t.Fatal("expected credits snapshot")
	}
	if !snapshot.Credits.HasCredits || snapshot.Credits.Unlimited {
		t.Fatalf("credits flags = %#v", snapshot.Credits)
	}
	if snapshot.Credits.Balance == nil || *snapshot.Credits.Balance != 19.5 {
		t.Fatalf("balance = %#v, want 19.5", snapshot.Credits.Balance)
	}
	if snapshot.Credits.ActiveLimit != "plus" {
		t.Fatalf("active_limit = %q, want plus", snapshot.Credits.ActiveLimit)
	}
}

func TestParseQuotaFromHeadersIgnoresCreditsOnly(t *testing.T) {
	t.Parallel()

	headers := http.Header{}
	headers.Set("X-Codex-Credits-Has-Credits", "true")
	headers.Set("X-Codex-Credits-Unlimited", "false")
	headers.Set("X-Codex-Credits-Balance", "19.5")
	headers.Set("X-Codex-Active-Limit", "plus")

	snapshot := ParseQuotaFromHeaders(headers)
	if snapshot != nil {
		t.Fatalf("ParseQuotaFromHeaders() = %#v, want nil for credits-only headers", snapshot)
	}
}

func TestParseQuotaFromEvent(t *testing.T) {
	t.Parallel()

	event := &StreamEvent{
		Type: "codex.rate_limits",
		Raw: map[string]any{
			"rate_limits": map[string]any{
				"primary": map[string]any{
					"used_percent":   100.0,
					"window_minutes": 300.0,
					"reset_at":       float64(4102444800),
				},
				"secondary": map[string]any{
					"used_percent":   45.0,
					"window_minutes": 10080.0,
					"reset_at":       float64(4103049600),
				},
				"code_review": map[string]any{
					"used_percent":         100.0,
					"limit_window_seconds": 86400.0,
					"reset_at":             float64(4103136000),
				},
			},
		},
	}

	snapshot := ParseQuotaFromEvent(event, "plus")
	if snapshot == nil {
		t.Fatal("ParseQuotaFromEvent() = nil")
	}
	if snapshot.Source != "response_event" {
		t.Fatalf("source = %q, want response_event", snapshot.Source)
	}
	if snapshot.RateLimit.UsedPercent == nil || *snapshot.RateLimit.UsedPercent != 100 {
		t.Fatalf("primary used_percent = %#v, want 100", snapshot.RateLimit.UsedPercent)
	}
	if !snapshot.RateLimit.LimitReached {
		t.Fatal("expected primary limit_reached")
	}
	if snapshot.SecondaryRateLimit == nil || snapshot.SecondaryRateLimit.UsedPercent == nil || *snapshot.SecondaryRateLimit.UsedPercent != 45 {
		t.Fatalf("secondary = %#v, want used_percent 45", snapshot.SecondaryRateLimit)
	}
	if snapshot.CodeReviewRateLimit == nil || snapshot.CodeReviewRateLimit.UsedPercent == nil || *snapshot.CodeReviewRateLimit.UsedPercent != 100 {
		t.Fatalf("code_review = %#v, want used_percent 100", snapshot.CodeReviewRateLimit)
	}
	if snapshot.CodeReviewRateLimit.LimitWindowSeconds == nil || *snapshot.CodeReviewRateLimit.LimitWindowSeconds != 86400 {
		t.Fatalf("code_review limit_window_seconds = %#v, want 86400", snapshot.CodeReviewRateLimit.LimitWindowSeconds)
	}
}

func TestQuotaFromUsageResponseIncludesCodeReviewRateLimit(t *testing.T) {
	t.Parallel()

	payload := UsageResponse{PlanType: "plus"}
	payload.RateLimit.Allowed = true
	payload.RateLimit.PrimaryWindow = &UsageWindow{
		UsedPercent:        25,
		LimitWindowSeconds: 3600,
		ResetAt:            time.Now().UTC().Add(time.Hour).Unix(),
	}
	payload.CodeReviewRateLimit = &struct {
		Allowed       bool         `json:"allowed"`
		LimitReached  bool         `json:"limit_reached"`
		PrimaryWindow *UsageWindow `json:"primary_window"`
	}{
		Allowed:      true,
		LimitReached: true,
		PrimaryWindow: &UsageWindow{
			UsedPercent:        100,
			LimitWindowSeconds: 86400,
			ResetAt:            time.Now().UTC().Add(24 * time.Hour).Unix(),
		},
	}

	snapshot := QuotaFromUsageResponse(payload)
	if snapshot.CodeReviewRateLimit == nil {
		t.Fatal("expected code review rate limit")
	}
	if !snapshot.CodeReviewRateLimit.LimitReached {
		t.Fatal("expected code review limit_reached")
	}
}
