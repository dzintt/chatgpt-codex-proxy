package accounts

import (
	"testing"
	"time"
)

func TestPrimaryRateLimitReset(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(15 * time.Minute)

	if got := PrimaryRateLimitReset(&QuotaSnapshot{
		RateLimit: RateLimitWindow{
			Allowed:      false,
			LimitReached: false,
			ResetAt:      &resetAt,
		},
	}, now); got == nil || !got.Equal(resetAt.UTC()) {
		t.Fatalf("PrimaryRateLimitReset() = %v, want %v", got, resetAt.UTC())
	}
}

func TestQuotaResetUsesSecondaryWindowWhenPrimaryIsAvailable(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(30 * time.Minute)

	got := QuotaReset(&QuotaSnapshot{
		RateLimit: RateLimitWindow{
			Allowed: true,
		},
		SecondaryRateLimit: &RateLimitWindow{
			Allowed:      true,
			LimitReached: true,
			ResetAt:      &resetAt,
		},
	}, now)

	if got == nil || !got.Equal(resetAt.UTC()) {
		t.Fatalf("QuotaReset() = %v, want %v", got, resetAt.UTC())
	}
}

func TestQuotaResetIgnoresExpiredWindows(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(-time.Minute)

	if got := QuotaReset(&QuotaSnapshot{
		RateLimit: RateLimitWindow{
			Allowed:      true,
			LimitReached: true,
			ResetAt:      &resetAt,
		},
	}, now); got != nil {
		t.Fatalf("QuotaReset() = %v, want nil", got)
	}
}
