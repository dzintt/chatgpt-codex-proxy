package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/config"
)

func TestHandleAdminAccountUsageReturnsQuotaOnlyFields(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	cooldown := now.Add(5 * time.Minute)
	resetAt := now.Add(10 * time.Minute)
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:            "acct_usage",
		AccountID:     "upstream_usage",
		UserID:        "user_123",
		Status:        accounts.StatusActive,
		LastError:     "rate limited",
		CooldownUntil: &cooldown,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CachedQuota: &accounts.QuotaSnapshot{
			Source:    "usage_endpoint",
			FetchedAt: now,
			RateLimit: accounts.RateLimitWindow{
				Allowed:      true,
				LimitReached: false,
				ResetAt:      &resetAt,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:   accountsSvc,
		accountMgr: codex.NewAccountManager(config.Config{}, accountsSvc, nil, nil),
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/admin/accounts/acct_usage/usage?cached=true", nil)
	ctx.Params = gin.Params{{Key: "account_id", Value: "acct_usage"}}

	app.handleAdminAccountUsage(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, ok := body["local_usage"]; ok {
		t.Fatalf("response contains local_usage: %s", recorder.Body.String())
	}
	if _, ok := body["block_state"]; ok {
		t.Fatalf("response contains block_state: %s", recorder.Body.String())
	}
	if _, ok := body["eligible_now"]; !ok {
		t.Fatalf("response missing eligible_now: %s", recorder.Body.String())
	}
	if _, ok := body["cooldown_until"]; !ok {
		t.Fatalf("response missing cooldown_until: %s", recorder.Body.String())
	}
}
