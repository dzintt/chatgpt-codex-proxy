package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/middleware"
	"chatgpt-codex-proxy/internal/translate"
)

type fakeEventStream struct {
	headers http.Header
	events  []*codex.StreamEvent
	index   int
	tailErr error
}

type memoryAccountsStore struct {
	state accounts.State
}

func (m *memoryAccountsStore) Load() (accounts.State, error) {
	return m.state, nil
}

func (m *memoryAccountsStore) Save(state accounts.State) error {
	m.state = state
	return nil
}

func (f *fakeEventStream) NextEvent() (*codex.StreamEvent, error) {
	if f.index >= len(f.events) {
		if f.tailErr != nil {
			err := f.tailErr
			f.tailErr = nil
			return nil, err
		}
		return nil, io.EOF
	}
	event := f.events[f.index]
	f.index++
	return event, nil
}

func (f *fakeEventStream) Close() error { return nil }

func (f *fakeEventStream) Headers() http.Header {
	if f.headers == nil {
		return http.Header{}
	}
	return f.headers
}

func TestObserveQuotaSnapshotUpdatesCachedQuota(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_headers",
		AccountID: "upstream_headers",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	pct := 35.0
	resetAt := now.Add(15 * time.Minute)
	app.observeQuotaSnapshot("acct_headers", &accounts.QuotaSnapshot{
		Source:    "response_headers",
		FetchedAt: now,
		RateLimit: accounts.RateLimitWindow{
			Allowed:      true,
			LimitReached: false,
			UsedPercent:  &pct,
			ResetAt:      &resetAt,
		},
	})

	record, ok := accountsSvc.Get("acct_headers")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.CachedQuota == nil || record.CachedQuota.RateLimit.UsedPercent == nil {
		t.Fatal("cached quota missing used percent")
	}
	if *record.CachedQuota.RateLimit.UsedPercent != 35.0 {
		t.Fatalf("used_percent = %v, want 35", *record.CachedQuota.RateLimit.UsedPercent)
	}
}

func TestObserveQuotaEventUpdatesCachedQuota(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_event",
		AccountID: "upstream_event",
		Status:    accounts.StatusActive,
		PlanType:  "plus",
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	handled := app.observeQuotaEvent(accounts.Record{ID: "acct_event", PlanType: "plus"}, &codex.StreamEvent{
		Type: "codex.rate_limits",
		Raw: map[string]any{
			"rate_limits": map[string]any{
				"primary": map[string]any{
					"used_percent": 42.0,
					"reset_at":     float64(now.Add(30 * time.Minute).Unix()),
				},
			},
		},
	})
	if !handled {
		t.Fatal("observeQuotaEvent() = false, want true")
	}

	record, ok := accountsSvc.Get("acct_event")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if record.CachedQuota == nil || record.CachedQuota.RateLimit.UsedPercent == nil {
		t.Fatal("cached quota missing after event")
	}
	if *record.CachedQuota.RateLimit.UsedPercent != 42.0 {
		t.Fatalf("used_percent = %v, want 42", *record.CachedQuota.RateLimit.UsedPercent)
	}
}

func TestStreamChatCompletionClassifiesStructuredRateLimitFailureAndSetsCooldown(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_rate_limit",
		AccountID: "upstream_rate_limit",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
			ContinuationTTL:   time.Minute,
		},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_rate_limit")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{{
			Type: "error",
			Raw: map[string]any{
				"error": map[string]any{
					"code":              "rate_limited",
					"message":           "Too many requests",
					"resets_in_seconds": 12,
					"usage": map[string]any{
						"input_tokens":  11,
						"output_tokens": 7,
					},
				},
			},
		}},
	}

	app.streamChatCompletion(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointChat,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	if !strings.Contains(recorder.Body.String(), "upstream account rate limited") {
		t.Fatalf("stream body = %s, want rate limited message", recorder.Body.String())
	}

	updated, _ := accountsSvc.Get("acct_rate_limit")
	if updated.CooldownUntil == nil {
		t.Fatal("cooldown_until = nil, want cooldown")
	}
	if updated.Status != accounts.StatusActive {
		t.Fatalf("status = %q, want active", updated.Status)
	}
}

func TestStreamResponsesClassifiesStructuredQuotaFailureAndSetsCooldown(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	resetAt := now.Add(20 * time.Minute)
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_quota",
		AccountID: "upstream_quota",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CachedQuota: &accounts.QuotaSnapshot{
			RateLimit: accounts.RateLimitWindow{
				Allowed:      true,
				LimitReached: true,
				ResetAt:      &resetAt,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
			ContinuationTTL:   time.Minute,
		},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_quota")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{{
			Type: "response.failed",
			Raw: map[string]any{
				"response": map[string]any{
					"id": "resp_quota",
					"usage": map[string]any{
						"input_tokens":  13,
						"output_tokens": 2,
					},
					"error": map[string]any{
						"code":    "usage_limit_reached",
						"message": "Billing period exhausted",
					},
				},
			},
		}},
	}

	app.streamResponses(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	if !strings.Contains(recorder.Body.String(), "upstream account quota exhausted") {
		t.Fatalf("stream body = %s, want quota exhausted message", recorder.Body.String())
	}

	updated, _ := accountsSvc.Get("acct_quota")
	if updated.CooldownUntil == nil {
		t.Fatal("cooldown_until = nil, want cooldown")
	}
	if updated.Status != accounts.StatusActive {
		t.Fatalf("status = %q, want active", updated.Status)
	}
}

func TestStreamResponsesClassifiesStructuredUnauthorizedFailure(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_unauthorized",
		AccountID: "upstream_unauthorized",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
			ContinuationTTL:   time.Minute,
		},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_unauthorized")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{{
			Type: "response.failed",
			Raw: map[string]any{
				"response": map[string]any{
					"id": "resp_unauthorized",
					"error": map[string]any{
						"code":    "invalid_token",
						"message": "Session expired",
					},
				},
			},
		}},
	}

	app.streamResponses(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	updated, _ := accountsSvc.Get("acct_unauthorized")
	if updated.Status != accounts.StatusExpired {
		t.Fatalf("status = %q, want expired", updated.Status)
	}
}

func TestClassifyUpstreamErrorBansGeneric403ButNotCloudflare403(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t,
		&accounts.Record{
			ID:        "acct_generic_403",
			AccountID: "upstream_generic_403",
			Status:    accounts.StatusActive,
			Token: accounts.OAuthToken{
				AccessToken: "token",
				ExpiresAt:   now.Add(time.Hour),
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		&accounts.Record{
			ID:        "acct_cloudflare_403",
			AccountID: "upstream_cloudflare_403",
			Status:    accounts.StatusActive,
			Token: accounts.OAuthToken{
				AccessToken: "token",
				ExpiresAt:   now.Add(time.Hour),
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
	)

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
		},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	status, code, _ := app.classifyUpstreamError("acct_generic_403", &codex.UpstreamError{
		Op:         "codex response",
		StatusCode: http.StatusForbidden,
		Body:       `{"error":"access denied"}`,
	})
	if status != http.StatusForbidden || code != "account_banned" {
		t.Fatalf("generic 403 = (%d, %q), want (403, account_banned)", status, code)
	}

	status, code, _ = app.classifyUpstreamError("acct_cloudflare_403", &codex.UpstreamError{
		Op:         "codex response",
		StatusCode: http.StatusForbidden,
		Body:       "<!DOCTYPE html><html><body>cf_chl blocked</body></html>",
	})
	if status != http.StatusForbidden || code != "upstream_error" {
		t.Fatalf("cloudflare 403 = (%d, %q), want (403, upstream_error)", status, code)
	}

	generic, _ := accountsSvc.Get("acct_generic_403")
	if generic.Status != accounts.StatusBanned {
		t.Fatalf("generic status = %q, want banned", generic.Status)
	}
	cloudflare, _ := accountsSvc.Get("acct_cloudflare_403")
	if cloudflare.Status != accounts.StatusActive {
		t.Fatalf("cloudflare status = %q, want active", cloudflare.Status)
	}
}

func newServerAccounts(t *testing.T, records ...*accounts.Record) *accounts.Service {
	t.Helper()

	svc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: records,
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return svc
}
