package codex

import (
	"context"
	"testing"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/config"
)

type memoryStore struct {
	state accounts.State
}

func (m *memoryStore) Load() (accounts.State, error) {
	return m.state, nil
}

func (m *memoryStore) Save(state accounts.State) error {
	m.state = state
	return nil
}

func TestGetUsageCachedBypassesEnsureReady(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().UTC().Add(30 * time.Minute)
	accountsSvc, err := accounts.NewService(&memoryStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_disabled",
			AccountID: "upstream_disabled",
			Status:    accounts.StatusDisabled,
			CachedQuota: &accounts.QuotaSnapshot{
				PlanType:  "plus",
				Source:    "usage_endpoint",
				FetchedAt: time.Now().UTC(),
				RateLimit: accounts.RateLimitWindow{
					Allowed:      true,
					LimitReached: false,
					ResetAt:      &resetAt,
				},
			},
			LocalUsage: accounts.LocalUsage{
				RequestCount: 7,
			},
			Token: accounts.OAuthToken{
				AccessToken: "access-token",
				ExpiresAt:   time.Now().UTC().Add(-time.Hour),
			},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("accounts.NewService() error = %v", err)
	}

	manager := NewAccountManager(config.Config{}, accountsSvc, nil, nil)

	record, quota, err := manager.GetUsage(context.Background(), "acct_disabled", true)
	if err != nil {
		t.Fatalf("GetUsage(cached=true) error = %v", err)
	}
	if record.ID != "acct_disabled" {
		t.Fatalf("record.ID = %q, want acct_disabled", record.ID)
	}
	if record.Status != accounts.StatusDisabled {
		t.Fatalf("record.Status = %q, want disabled", record.Status)
	}
	if record.LocalUsage.RequestCount != 7 {
		t.Fatalf("request_count = %d, want 7", record.LocalUsage.RequestCount)
	}
	if quota == nil {
		t.Fatal("quota = nil, want cached quota")
	}
	if quota.PlanType != "plus" {
		t.Fatalf("quota.PlanType = %q, want plus", quota.PlanType)
	}
}
