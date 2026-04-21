package server

import (
	"encoding/json"
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

func TestStreamResponsesSynthesizesFunctionCallLifecycle(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_stream_tools",
		AccountID: "upstream_stream_tools",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_stream_tools")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.function_call_arguments.delta",
				Raw: map[string]any{
					"response_id":  "resp_tool",
					"item_id":      "fc_1",
					"output_index": 0,
					"name":         "demo__echo",
					"delta":        `{"message":"hel`,
				},
			},
			{
				Type: "response.function_call_arguments.done",
				Raw: map[string]any{
					"response_id":  "resp_tool",
					"item_id":      "fc_1",
					"output_index": 0,
					"name":         "demo__echo",
					"arguments":    `{"message":"hello"}`,
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_tool",
						"model":  "gpt-5.4",
						"status": "completed",
						"output": []any{
							map[string]any{
								"id":     "msg_1",
								"type":   "message",
								"role":   "assistant",
								"status": "completed",
								"content": []any{
									map[string]any{
										"type": "output_text",
										"text": "tool ready",
									},
								},
							},
						},
						"output_text": "tool ready",
					},
				},
			},
		},
	}

	app.streamResponses(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	assertEventTypes(t, events,
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
		"done",
	)

	added := events[0].Data
	if added["response_id"] != "resp_tool" {
		t.Fatalf("added response_id = %#v, want resp_tool", added["response_id"])
	}
	if added["output_index"] != float64(0) {
		t.Fatalf("added output_index = %#v, want 0", added["output_index"])
	}
	item := nestedMapFromAny(added["item"])
	if item["type"] != "function_call" {
		t.Fatalf("added item.type = %#v, want function_call", item["type"])
	}
	if item["status"] != "in_progress" {
		t.Fatalf("added item.status = %#v, want in_progress", item["status"])
	}

	completed := events[4].Data
	response := nestedMapFromAny(completed["response"])
	output := sliceOfMapsFromAny(response["output"])
	if len(output) != 2 {
		t.Fatalf("completed response.output len = %d, want 2", len(output))
	}
	if output[0]["type"] != "function_call" {
		t.Fatalf("completed output[0].type = %#v, want function_call", output[0]["type"])
	}
	if output[0]["call_id"] != "fc_1" {
		t.Fatalf("completed output[0].call_id = %#v, want fc_1", output[0]["call_id"])
	}
	if output[1]["type"] != "message" {
		t.Fatalf("completed output[1].type = %#v, want message", output[1]["type"])
	}
}

func TestStreamResponsesSynthesizesFunctionCallLifecycleWithoutDeltas(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_stream_done_only",
		AccountID: "upstream_stream_done_only",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_stream_done_only")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.function_call_arguments.done",
				Raw: map[string]any{
					"response_id":  "resp_done_only",
					"item_id":      "fc_done",
					"output_index": 0,
					"name":         "demo__echo",
					"arguments":    `{"message":"hello"}`,
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_done_only",
						"model":  "gpt-5.4",
						"status": "completed",
					},
				},
			},
		},
	}

	app.streamResponses(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	assertEventTypes(t, events,
		"response.output_item.added",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
		"done",
	)
}

func TestStreamResponsesTextOnlyPassthroughRemainsUnchanged(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_stream_text",
		AccountID: "upstream_stream_text",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_stream_text")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.output_text.delta",
				Raw: map[string]any{
					"response_id": "resp_text",
					"delta":       "hello",
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":          "resp_text",
						"model":       "gpt-5.4",
						"status":      "completed",
						"output_text": "hello",
					},
				},
			},
		},
	}

	app.streamResponses(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	assertEventTypes(t, events,
		"response.output_text.delta",
		"response.completed",
		"done",
	)
	for _, event := range events {
		if strings.HasPrefix(event.Event, "response.output_item.") {
			t.Fatalf("unexpected tool lifecycle event in text-only stream: %s", event.Event)
		}
	}
}

func TestStreamResponsesWithContinuationUsesSameFunctionCallLifecycle(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_stream_continuation",
		AccountID: "upstream_stream_continuation",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	record, _ := accountsSvc.Get("acct_stream_continuation")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.function_call_arguments.done",
				Raw: map[string]any{
					"response_id":  "resp_continuation",
					"item_id":      "fc_continue",
					"output_index": 0,
					"name":         "demo__echo",
					"arguments":    `{"message":"continued"}`,
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_continuation",
						"model":  "gpt-5.4",
						"status": "completed",
					},
				},
			},
		},
	}

	app.streamResponses(ctx, record, translate.NormalizedRequest{
		Endpoint:           translate.EndpointResponses,
		Model:              "gpt-5.4",
		Stream:             true,
		PreviousResponseID: "resp_previous",
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	assertEventTypes(t, events,
		"response.output_item.added",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
		"done",
	)
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

type sseEvent struct {
	Event string
	Data  map[string]any
	Raw   string
}

func parseSSEEvents(t *testing.T, body string) []sseEvent {
	t.Helper()

	chunks := strings.Split(strings.TrimSpace(body), "\n\n")
	events := make([]sseEvent, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}

		var eventName string
		var rawData string
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				rawData = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}

		entry := sseEvent{Event: eventName, Raw: rawData}
		if rawData != "[DONE]" {
			if err := json.Unmarshal([]byte(rawData), &entry.Data); err != nil {
				t.Fatalf("json.Unmarshal(%q) error = %v", rawData, err)
			}
		}
		events = append(events, entry)
	}
	return events
}

func assertEventTypes(t *testing.T, events []sseEvent, want ...string) {
	t.Helper()

	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.Event)
	}
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d (%v)", len(got), len(want), got)
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("event[%d] = %q, want %q (all=%v)", idx, got[idx], want[idx], got)
		}
	}
}

func nestedMapFromAny(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func sliceOfMapsFromAny(value any) []map[string]any {
	items, _ := value.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		mapped, _ := item.(map[string]any)
		if mapped != nil {
			out = append(out, mapped)
		}
	}
	return out
}
