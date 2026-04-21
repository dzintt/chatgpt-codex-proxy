package accounts

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

type memoryStore struct {
	state State
}

func (m *memoryStore) Load() (State, error) {
	return m.state, nil
}

func (m *memoryStore) Save(state State) error {
	m.state = state
	return nil
}

func TestLeastUsedPrefersLowerPrimaryUsedPercent(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, RotationLeastUsed,
		recordWithQuota("acct_busy", 80, nil, nil),
		recordWithQuota("acct_light", 20, nil, nil),
	)

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_light" {
		t.Fatalf("Acquire() = %q, want acct_light", record.ID)
	}
}

func TestLeastUsedUsesSecondaryUsedPercentAsTieBreaker(t *testing.T) {
	t.Parallel()

	secondaryHigh := 70.0
	secondaryLow := 10.0
	svc := newTestService(t, RotationLeastUsed,
		recordWithQuota("acct_high_secondary", 40, &secondaryHigh, nil),
		recordWithQuota("acct_low_secondary", 40, &secondaryLow, nil),
	)

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_low_secondary" {
		t.Fatalf("Acquire() = %q, want acct_low_secondary", record.ID)
	}
}

func TestLeastUsedFallsBackToRoundRobinWhenQuotaMissing(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, RotationLeastUsed,
		recordWithID("acct_a"),
		recordWithID("acct_b"),
	)

	first, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	second, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire(second) error = %v", err)
	}

	if first.ID != "acct_a" || second.ID != "acct_b" {
		t.Fatalf("round-robin fallback order = %q, %q; want acct_a, acct_b", first.ID, second.ID)
	}
}

func TestLeastUsedSortsUnknownQuotaBehindKnownQuota(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, RotationLeastUsed,
		recordWithID("acct_unknown"),
		recordWithQuota("acct_known", 15, nil, nil),
	)

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_known" {
		t.Fatalf("Acquire() = %q, want acct_known", record.ID)
	}
}

func TestStickyReusesLastSuccessfulEligibleAccount(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, RotationSticky,
		recordWithQuota("acct_a", 10, nil, nil),
		recordWithQuota("acct_b", 20, nil, nil),
	)
	svc.NoteSuccess("acct_b")

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_b" {
		t.Fatalf("Acquire() = %q, want acct_b", record.ID)
	}
}

func TestStickyFallsBackWhenLastSuccessfulBecomesIneligible(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(10 * time.Minute)
	svc := newTestService(t, RotationSticky,
		recordWithQuota("acct_a", 10, nil, nil),
		&Record{
			ID:        "acct_b",
			AccountID: "upstream_acct_b",
			Status:    StatusActive,
			Token:     makeTestOAuthToken(t, nil),
			CachedQuota: &QuotaSnapshot{
				RateLimit: RateLimitWindow{
					Allowed:      false,
					LimitReached: false,
					ResetAt:      &resetAt,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
	)
	svc.NoteSuccess("acct_b")

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_a" {
		t.Fatalf("Acquire() = %q, want acct_a fallback", record.ID)
	}
}

func TestRoundRobinRotatesOverEligibleAccountsOnly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	cooldown := now.Add(10 * time.Minute)
	svc := newTestService(t, RotationRoundRobin,
		recordWithID("acct_a"),
		recordWithID("acct_b"),
		&Record{
			ID:            "acct_cooldown",
			AccountID:     "upstream_cooldown",
			Status:        StatusActive,
			Token:         makeTestOAuthToken(t, nil),
			CooldownUntil: &cooldown,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	)

	first, _ := svc.Acquire("")
	second, _ := svc.Acquire("")
	third, _ := svc.Acquire("")

	if first.ID != "acct_a" || second.ID != "acct_b" || third.ID != "acct_a" {
		t.Fatalf("round robin eligible order = %q, %q, %q; want acct_a, acct_b, acct_a", first.ID, second.ID, third.ID)
	}
}

func TestCooldownExcludesUntilExpiry(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	cooldown := now.Add(2 * time.Minute)
	svc := newTestService(t, RotationLeastUsed,
		&Record{
			ID:            "acct_cooldown",
			AccountID:     "upstream_cooldown",
			Status:        StatusActive,
			Token:         makeTestOAuthToken(t, nil),
			CooldownUntil: &cooldown,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		recordWithID("acct_ok"),
	)

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_ok" {
		t.Fatalf("Acquire() = %q, want acct_ok", record.ID)
	}
}

func TestRecoveredQuotaClearsCooldown(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	cooldown := now.Add(5 * time.Minute)
	svc := newTestService(t, RotationLeastUsed, &Record{
		ID:            "acct_recover",
		AccountID:     "upstream_recover",
		Status:        StatusActive,
		Token:         makeTestOAuthToken(t, nil),
		CooldownUntil: &cooldown,
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	usedPercent := 25.0
	resetAt := now.Add(10 * time.Minute)
	if err := svc.ObserveQuota("acct_recover", &QuotaSnapshot{
		FetchedAt: now,
		RateLimit: RateLimitWindow{
			Allowed:      true,
			LimitReached: false,
			UsedPercent:  &usedPercent,
			ResetAt:      &resetAt,
		},
	}); err != nil {
		t.Fatalf("ObserveQuota() error = %v", err)
	}

	record, ok := svc.Get("acct_recover")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.CooldownUntil != nil {
		t.Fatalf("cooldown_until = %v, want nil", record.CooldownUntil)
	}
}

func TestPrimaryAllowedFalseBlocksRouting(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	svc := newTestService(t, RotationLeastUsed,
		&Record{
			ID:        "acct_blocked",
			AccountID: "upstream_blocked",
			Status:    StatusActive,
			Token:     makeTestOAuthToken(t, nil),
			CachedQuota: &QuotaSnapshot{
				Source: "usage_endpoint",
				RateLimit: RateLimitWindow{
					Allowed: false,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		recordWithID("acct_ok"),
	)

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_ok" {
		t.Fatalf("Acquire() = %q, want acct_ok", record.ID)
	}
}

func TestPrimaryAndSecondaryExhaustionBlockRouting(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(10 * time.Minute)
	svc := newTestService(t, RotationLeastUsed,
		&Record{
			ID:        "acct_primary",
			AccountID: "upstream_primary",
			Status:    StatusActive,
			Token:     makeTestOAuthToken(t, nil),
			CachedQuota: &QuotaSnapshot{
				RateLimit: RateLimitWindow{
					Allowed:      true,
					LimitReached: true,
					ResetAt:      &resetAt,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		&Record{
			ID:        "acct_secondary",
			AccountID: "upstream_secondary",
			Status:    StatusActive,
			Token:     makeTestOAuthToken(t, nil),
			CachedQuota: &QuotaSnapshot{
				RateLimit: RateLimitWindow{Allowed: true},
				SecondaryRateLimit: &RateLimitWindow{
					Allowed:      true,
					LimitReached: true,
					ResetAt:      &resetAt,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		recordWithID("acct_ok"),
	)

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_ok" {
		t.Fatalf("Acquire() = %q, want acct_ok", record.ID)
	}
}

func TestCodeReviewRateLimitDoesNotBlockRouting(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(10 * time.Minute)
	svc := newTestService(t, RotationLeastUsed, &Record{
		ID:        "acct_code_review",
		AccountID: "upstream_code_review",
		Status:    StatusActive,
		Token:     makeTestOAuthToken(t, nil),
		CachedQuota: &QuotaSnapshot{
			RateLimit: RateLimitWindow{
				Allowed: true,
			},
			CodeReviewRateLimit: &RateLimitWindow{
				Allowed:      true,
				LimitReached: true,
				ResetAt:      &resetAt,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_code_review" {
		t.Fatalf("Acquire() = %q, want acct_code_review", record.ID)
	}
}

func TestPatchActiveClearsCooldownLastErrorAndQuota(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	cooldown := now.Add(10 * time.Minute)
	svc := newTestService(t, RotationLeastUsed, &Record{
		ID:            "acct_patch",
		AccountID:     "upstream_patch",
		Status:        StatusActive,
		Token:         makeTestOAuthToken(t, nil),
		CooldownUntil: &cooldown,
		LastError:     "rate limited",
		CachedQuota:   &QuotaSnapshot{RateLimit: RateLimitWindow{Allowed: true}},
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	active := StatusActive
	record, err := svc.Patch("acct_patch", nil, &active)
	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if record.CooldownUntil != nil {
		t.Fatalf("cooldown_until = %v, want nil", record.CooldownUntil)
	}
	if record.LastError != "" {
		t.Fatalf("last_error = %q, want empty", record.LastError)
	}
	if record.CachedQuota != nil {
		t.Fatalf("cached_quota = %#v, want nil", record.CachedQuota)
	}
}

func TestUpsertFromTokenReusesAccountWhenUserIDMissing(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	svc := newTestService(t, RotationLeastUsed, &Record{
		ID:        "acct_existing",
		AccountID: "upstream_same",
		Status:    StatusActive,
		Token:     makeTestOAuthToken(t, map[string]any{"email": "old@example.com"}),
		Cookies:   map[string]string{},
		CreatedAt: now,
		UpdatedAt: now,
	})

	record, err := svc.UpsertFromToken("upstream_same", makeTestOAuthToken(t, map[string]any{
		"email": "new@example.com",
	}))
	if err != nil {
		t.Fatalf("UpsertFromToken() error = %v", err)
	}
	if record.ID != "acct_existing" {
		t.Fatalf("UpsertFromToken() returned %q, want acct_existing", record.ID)
	}

	records := svc.List()
	if len(records) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(records))
	}
	if records[0].Email != "new@example.com" {
		t.Fatalf("email = %q, want new@example.com", records[0].Email)
	}
}

func TestUpsertFromTokenFillsMissingStoredUserID(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	svc := newTestService(t, RotationLeastUsed, &Record{
		ID:        "acct_existing",
		AccountID: "upstream_same",
		Status:    StatusActive,
		Token:     makeTestOAuthToken(t, map[string]any{"email": "old@example.com"}),
		Cookies:   map[string]string{},
		CreatedAt: now,
		UpdatedAt: now,
	})

	record, err := svc.UpsertFromToken("upstream_same", makeTestOAuthToken(t, map[string]any{
		"email":             "new@example.com",
		"chatgpt_user_id":   "user_123",
		"chatgpt_plan_type": "plus",
	}))
	if err != nil {
		t.Fatalf("UpsertFromToken() error = %v", err)
	}
	if record.ID != "acct_existing" {
		t.Fatalf("UpsertFromToken() returned %q, want acct_existing", record.ID)
	}
	if record.UserID != "user_123" {
		t.Fatalf("user_id = %q, want user_123", record.UserID)
	}

	records := svc.List()
	if len(records) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(records))
	}
	if records[0].PlanType != "plus" {
		t.Fatalf("plan_type = %q, want plus", records[0].PlanType)
	}
}

func newTestService(t *testing.T, strategy RotationStrategy, records ...*Record) *Service {
	t.Helper()

	store := &memoryStore{state: State{
		Records:          records,
		RotationStrategy: strategy,
	}}
	svc, err := NewService(store, strategy, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return svc
}

func recordWithID(id string) *Record {
	now := time.Now().UTC()
	return &Record{
		ID:        id,
		AccountID: "upstream_" + id,
		Status:    StatusActive,
		Token: OAuthToken{
			AccessToken: "token-" + id,
			ExpiresAt:   now.Add(time.Hour),
		},
		Cookies:   map[string]string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func recordWithQuota(id string, primary float64, secondary *float64, primaryReset *time.Time) *Record {
	record := recordWithID(id)
	record.CachedQuota = &QuotaSnapshot{
		RateLimit: RateLimitWindow{
			Allowed:     true,
			UsedPercent: floatPtr(primary),
			ResetAt:     primaryReset,
		},
	}
	if secondary != nil {
		record.CachedQuota.SecondaryRateLimit = &RateLimitWindow{
			Allowed:     true,
			UsedPercent: secondary,
		}
	}
	return record
}

func floatPtr(value float64) *float64 {
	return &value
}

func makeTestOAuthToken(t *testing.T, claims map[string]any) OAuthToken {
	t.Helper()

	if claims == nil {
		claims = map[string]any{}
	}
	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	return OAuthToken{
		AccessToken: base64.RawURLEncoding.EncodeToString(header) + "." +
			base64.RawURLEncoding.EncodeToString(payload) + ".sig",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}
