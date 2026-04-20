package accounts

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

type memoryStore struct {
	state     State
	saveCount int
}

func (m *memoryStore) Load() (State, error) {
	return m.state, nil
}

func (m *memoryStore) Save(state State) error {
	m.state = state
	m.saveCount++
	return nil
}

func makeTestOAuthToken(t *testing.T, claims map[string]any) OAuthToken {
	t.Helper()

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
	}
}

func TestNewServiceBackfillsUserIDFromStoredToken(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	token := makeTestOAuthToken(t, map[string]any{
		"email":             "legacy@example.com",
		"chatgpt_plan_type": "plus",
		"chatgpt_user_id":   "user_legacy",
	})
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{{
			ID:        "acct_legacy",
			AccountID: "upstream_legacy",
			Status:    StatusActive,
			Token:     token,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, ok := svc.Get("acct_legacy")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.UserID != "user_legacy" {
		t.Fatalf("user_id = %q, want user_legacy", record.UserID)
	}
	if record.Email != "legacy@example.com" {
		t.Fatalf("email = %q, want legacy@example.com", record.Email)
	}
	if record.PlanType != "plus" {
		t.Fatalf("plan_type = %q, want plus", record.PlanType)
	}

	updated, err := svc.UpsertFromToken("upstream_legacy", token)
	if err != nil {
		t.Fatalf("UpsertFromToken() error = %v", err)
	}
	if updated.ID != "acct_legacy" {
		t.Fatalf("UpsertFromToken() returned %q, want acct_legacy", updated.ID)
	}
	if len(svc.List()) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(svc.List()))
	}
}

func TestAcquireUsesConfiguredRotationStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func() State
		verify func(*testing.T, *Service)
	}{
		{
			name: "round robin",
			setup: func() State {
				return State{
					Records: []*Record{
						recordWithID("acct_a"),
						recordWithID("acct_b"),
					},
					RotationStrategy: RotationRoundRobin,
				}
			},
			verify: func(t *testing.T, svc *Service) {
				t.Helper()

				first, _ := svc.Acquire("")
				second, _ := svc.Acquire("")
				third, _ := svc.Acquire("")

				if first.ID != "acct_a" || second.ID != "acct_b" || third.ID != "acct_a" {
					t.Fatalf("round robin order = %q, %q, %q", first.ID, second.ID, third.ID)
				}
			},
		},
		{
			name: "least used",
			setup: func() State {
				usedPercent := 75.0
				lastUsed := time.Now().UTC()
				return State{
					Records: []*Record{
						{
							ID:        "acct_busy",
							AccountID: "chatgpt_busy",
							Status:    StatusActive,
							CachedQuota: &QuotaSnapshot{
								RateLimit: RateLimitWindow{UsedPercent: &usedPercent},
							},
							LocalUsage: LocalUsage{RequestCount: 10, LastUsedAt: &lastUsed},
						},
						recordWithID("acct_free"),
					},
					RotationStrategy: RotationLeastUsed,
				}
			},
			verify: func(t *testing.T, svc *Service) {
				t.Helper()

				record, err := svc.Acquire("")
				if err != nil {
					t.Fatalf("Acquire() error = %v", err)
				}
				if record.ID != "acct_free" {
					t.Fatalf("Acquire() = %q, want acct_free", record.ID)
				}
			},
		},
		{
			name: "sticky",
			setup: func() State {
				now := time.Now().UTC()
				earlier := now.Add(-time.Hour)
				return State{
					Records: []*Record{
						{
							ID:         "acct_old",
							AccountID:  "chatgpt_old",
							Status:     StatusActive,
							LocalUsage: LocalUsage{LastUsedAt: &earlier},
						},
						{
							ID:         "acct_recent",
							AccountID:  "chatgpt_recent",
							Status:     StatusActive,
							LocalUsage: LocalUsage{LastUsedAt: &now},
						},
					},
					RotationStrategy: RotationSticky,
				}
			},
			verify: func(t *testing.T, svc *Service) {
				t.Helper()

				record, err := svc.Acquire("")
				if err != nil {
					t.Fatalf("Acquire() error = %v", err)
				}
				if record.ID != "acct_recent" {
					t.Fatalf("Acquire() = %q, want acct_recent", record.ID)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, err := NewService(&memoryStore{state: tc.setup()}, RotationRoundRobin, ServiceOptions{})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			tc.verify(t, svc)
		})
	}
}

func TestAcquireSkipsBlockedAccountsAndReenablesAfterExpiry(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_blocked",
				AccountID: "upstream_blocked",
				Status:    StatusActive,
				BlockState: BlockState{
					Reason: BlockRateLimit,
					Until:  &expired,
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
			recordWithID("acct_healthy"),
		},
		RotationStrategy: RotationLeastUsed,
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_blocked" {
		t.Fatalf("Acquire() = %q, want acct_blocked after expiry", record.ID)
	}
}

func TestAcquireKeepsQuotaFallbackBlockUntilExpiry(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	until := now.Add(5 * time.Minute)
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_blocked",
				AccountID: "upstream_blocked",
				Status:    StatusActive,
				BlockState: BlockState{
					Reason:     BlockQuotaPrimary,
					Until:      &until,
					ObservedAt: &now,
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
			recordWithID("acct_healthy"),
		},
		RotationStrategy: RotationLeastUsed,
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_healthy" {
		t.Fatalf("Acquire() = %q, want acct_healthy while quota fallback block is active", record.ID)
	}

	blocked, ok := svc.Get("acct_blocked")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if blocked.BlockState.Reason != BlockQuotaPrimary {
		t.Fatalf("block_reason = %q, want quota_primary", blocked.BlockState.Reason)
	}
	if blocked.BlockState.Until == nil || !blocked.BlockState.Until.Equal(until) {
		t.Fatalf("block_until = %v, want %v", blocked.BlockState.Until, until)
	}
}

func TestObserveQuotaClearsSnapshotManagedBlockWhenFreshSnapshotRecovers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		id     string
		reason BlockReason
	}{
		{
			name:   "quota fallback block",
			id:     "acct_blocked",
			reason: BlockQuotaPrimary,
		},
		{
			name:   "rate limit fallback block",
			id:     "acct_rate_limited",
			reason: BlockRateLimit,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now().UTC()
			until := now.Add(5 * time.Minute)
			observedAt := now.Add(-time.Minute)
			resetAt := now.Add(10 * time.Minute)
			usedPercent := 35.0
			svc, err := NewService(&memoryStore{state: State{
				Records: []*Record{
					{
						ID:        tc.id,
						AccountID: "upstream_" + tc.id,
						Status:    StatusActive,
						BlockState: BlockState{
							Reason:     tc.reason,
							Until:      &until,
							ObservedAt: &observedAt,
						},
						CreatedAt: now,
						UpdatedAt: now,
					},
				},
			}}, RotationLeastUsed, ServiceOptions{})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			if err := svc.ObserveQuota(tc.id, &QuotaSnapshot{
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

			record, ok := svc.Get(tc.id)
			if !ok {
				t.Fatal("Get() returned false")
			}
			if record.BlockState.Reason != BlockNone {
				t.Fatalf("block_reason = %q, want none after recovered quota snapshot", record.BlockState.Reason)
			}
		})
	}
}

func TestObserveQuotaUsesStickyBlockWhenLimitReachedWithoutReset(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	usedPercent := 100.0
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_no_reset",
				AccountID: "upstream_no_reset",
				Status:    StatusActive,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}, RotationLeastUsed, ServiceOptions{QuotaFallback: 5 * time.Minute})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := svc.ObserveQuota("acct_no_reset", &QuotaSnapshot{
		FetchedAt: now,
		RateLimit: RateLimitWindow{
			Allowed:      true,
			LimitReached: true,
			UsedPercent:  &usedPercent,
		},
	}); err != nil {
		t.Fatalf("ObserveQuota() error = %v", err)
	}

	record, ok := svc.Get("acct_no_reset")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.BlockState.Reason != BlockQuotaPrimary {
		t.Fatalf("block_reason = %q, want quota_primary", record.BlockState.Reason)
	}
	if record.BlockState.Until != nil {
		t.Fatalf("block_until = %v, want nil for sticky quota block", record.BlockState.Until)
	}
}

func TestPatchActiveClearsTransientBlock(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	until := now.Add(time.Hour)
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_patch_active",
				AccountID: "upstream_patch_active",
				Status:    StatusActive,
				BlockState: BlockState{
					Reason: BlockRateLimit,
					Until:  &until,
				},
				LastError: "rate limited",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	active := StatusActive
	record, err := svc.Patch("acct_patch_active", nil, &active)
	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if record.BlockState.Reason != BlockNone {
		t.Fatalf("block_reason = %q, want none", record.BlockState.Reason)
	}
	if record.LastError != "" {
		t.Fatalf("last_error = %q, want empty", record.LastError)
	}

	acquired, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if acquired.ID != "acct_patch_active" {
		t.Fatalf("Acquire() = %q, want acct_patch_active", acquired.ID)
	}
}

func TestLeastUsedPrefersEarlierResetWhenPressureMatches(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	pct := 90.0
	earlyReset := now.Add(5 * time.Minute)
	lateReset := now.Add(30 * time.Minute)

	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_late",
				AccountID: "upstream_late",
				Status:    StatusActive,
				CachedQuota: &QuotaSnapshot{
					RateLimit: RateLimitWindow{UsedPercent: &pct, ResetAt: &lateReset},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "acct_early",
				AccountID: "upstream_early",
				Status:    StatusActive,
				CachedQuota: &QuotaSnapshot{
					RateLimit: RateLimitWindow{UsedPercent: &pct, ResetAt: &earlyReset},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		RotationStrategy: RotationLeastUsed,
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if record.ID != "acct_early" {
		t.Fatalf("Acquire() = %q, want acct_early", record.ID)
	}
}

func TestLeastUsedRotatesAmongEqualBestCandidates(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_a",
				AccountID: "upstream_a",
				Status:    StatusActive,
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "acct_b",
				AccountID: "upstream_b",
				Status:    StatusActive,
				CreatedAt: now.Add(time.Second),
				UpdatedAt: now.Add(time.Second),
			},
		},
		RotationStrategy: RotationLeastUsed,
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	first, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	second, err := svc.Acquire("")
	if err != nil {
		t.Fatalf("Acquire(second) error = %v", err)
	}

	if first.ID == second.ID {
		t.Fatalf("least_used picked %q twice for equal candidates", first.ID)
	}
}

func TestRecordUsageUpdatesLifetimeAndWindowCountersOnce(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(10 * time.Minute)
	windowSeconds := 600
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_usage",
				AccountID: "upstream_usage",
				Status:    StatusActive,
				LocalUsage: LocalUsage{
					WindowResetAt:      &resetAt,
					LimitWindowSeconds: &windowSeconds,
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := svc.RecordUsage("acct_usage", 11, 7); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	record, ok := svc.Get("acct_usage")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.LocalUsage.InputTokens != 11 || record.LocalUsage.OutputTokens != 7 {
		t.Fatalf("lifetime tokens = (%d,%d), want (11,7)", record.LocalUsage.InputTokens, record.LocalUsage.OutputTokens)
	}
	if record.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", record.LocalUsage.RequestCount)
	}
	if record.LocalUsage.WindowInputTokens != 11 || record.LocalUsage.WindowOutputTokens != 7 {
		t.Fatalf("window tokens = (%d,%d), want (11,7)", record.LocalUsage.WindowInputTokens, record.LocalUsage.WindowOutputTokens)
	}
	if record.LocalUsage.WindowRequestCount != 1 {
		t.Fatalf("window_request_count = %d, want 1", record.LocalUsage.WindowRequestCount)
	}
}

func TestObserveQuotaResetsWindowCountersWhenBoundaryRolls(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	oldResetAt := now.Add(20 * time.Minute)
	newResetAt := now.Add(2 * time.Hour)
	oldWindowSeconds := 600
	newWindowSeconds := 7200
	usedPercent := 10.0

	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_window_roll",
				AccountID: "upstream_window_roll",
				Status:    StatusActive,
				LocalUsage: LocalUsage{
					WindowResetAt:         &oldResetAt,
					LimitWindowSeconds:    &oldWindowSeconds,
					WindowRequestCount:    7,
					WindowInputTokens:     70,
					WindowOutputTokens:    35,
					WindowCountersResetAt: &now,
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := svc.ObserveQuota("acct_window_roll", &QuotaSnapshot{
		RateLimit: RateLimitWindow{
			Allowed:            true,
			UsedPercent:        &usedPercent,
			ResetAt:            &newResetAt,
			LimitWindowSeconds: &newWindowSeconds,
		},
	}); err != nil {
		t.Fatalf("ObserveQuota() error = %v", err)
	}

	record, ok := svc.Get("acct_window_roll")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.LocalUsage.WindowRequestCount != 0 || record.LocalUsage.WindowInputTokens != 0 || record.LocalUsage.WindowOutputTokens != 0 {
		t.Fatalf("window counters not reset: %#v", record.LocalUsage)
	}
	if record.LocalUsage.WindowResetAt == nil || !record.LocalUsage.WindowResetAt.Equal(newResetAt) {
		t.Fatalf("window_reset_at = %v, want %v", record.LocalUsage.WindowResetAt, newResetAt)
	}
}

func TestNewServiceMigratesLegacyTransientStatuses(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(15 * time.Minute)
	pct := 100.0
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_legacy",
				AccountID: "upstream_legacy",
				Status:    Status("quota_exhausted"),
				CachedQuota: &QuotaSnapshot{
					RateLimit: RateLimitWindow{
						LimitReached: true,
						UsedPercent:  &pct,
						ResetAt:      &resetAt,
					},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, ok := svc.Get("acct_legacy")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.Status != StatusActive {
		t.Fatalf("status = %q, want active", record.Status)
	}
	if record.BlockState.Reason != BlockQuotaPrimary {
		t.Fatalf("block_reason = %q, want quota_primary", record.BlockState.Reason)
	}
	if record.BlockState.Until == nil || !record.BlockState.Until.Equal(resetAt) {
		t.Fatalf("block_until = %v, want %v", record.BlockState.Until, resetAt)
	}
}

func TestNewServiceClearsExpiredBlockStateAndPersistsRepair(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expired := now.Add(-2 * time.Minute)
	store := &memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_expired_block",
				AccountID: "upstream_expired_block",
				Status:    StatusActive,
				BlockState: BlockState{
					Reason: BlockRateLimit,
					Until:  &expired,
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}

	svc, err := NewService(store, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, ok := svc.Get("acct_expired_block")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.BlockState.Reason != BlockNone {
		t.Fatalf("block_reason = %q, want none after startup repair", record.BlockState.Reason)
	}
	if store.saveCount == 0 {
		t.Fatal("expected startup normalization to persist repaired block state")
	}
	if got := store.state.Records[0].BlockState.Reason; got != BlockNone {
		t.Fatalf("persisted block_reason = %q, want none", got)
	}
}

func TestGetClearsExpiredQuotaPressure(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetAt := now.Add(-time.Minute)
	usedPercent := 99.0
	svc, err := NewService(&memoryStore{state: State{
		Records: []*Record{
			{
				ID:        "acct_pressure",
				AccountID: "upstream_pressure",
				Status:    StatusActive,
				CachedQuota: &QuotaSnapshot{
					RateLimit: RateLimitWindow{
						Allowed:      true,
						LimitReached: true,
						UsedPercent:  &usedPercent,
						ResetAt:      &resetAt,
					},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}}, RotationLeastUsed, ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	record, ok := svc.Get("acct_pressure")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.CachedQuota == nil {
		t.Fatal("cached quota = nil")
	}
	if record.CachedQuota.RateLimit.LimitReached {
		t.Fatal("limit_reached = true, want false after reset")
	}
	if record.CachedQuota.RateLimit.ResetAt != nil {
		t.Fatalf("reset_at = %v, want nil", record.CachedQuota.RateLimit.ResetAt)
	}
	if record.CachedQuota.RateLimit.UsedPercent != nil {
		t.Fatalf("used_percent = %v, want nil after reset", record.CachedQuota.RateLimit.UsedPercent)
	}
}

func recordWithID(id string) *Record {
	now := time.Now().UTC()
	return &Record{
		ID:        id,
		AccountID: id + "_upstream",
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
