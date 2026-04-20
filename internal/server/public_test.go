package server

import (
	"bytes"
	"encoding/json"
	"errors"
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
}

type memoryAccountsStore struct {
	state     accounts.State
	saveCount int
}

func (m *memoryAccountsStore) Load() (accounts.State, error) {
	return m.state, nil
}

func (m *memoryAccountsStore) Save(state accounts.State) error {
	m.state = state
	m.saveCount++
	return nil
}

func (f *fakeEventStream) NextEvent() (*codex.StreamEvent, error) {
	if f.index >= len(f.events) {
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

func TestStreamChatCompletionBuffersTupleJSONAndStreamsToolCalls(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	logs := new(bytes.Buffer)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	normalized := translate.NormalizedRequest{
		Endpoint: translate.EndpointChat,
		Model:    "gpt-5.4",
		Stream:   true,
		TupleSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pair": map[string]any{
					"type": "array",
					"prefixItems": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "number"},
					},
				},
			},
		},
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.function_call_arguments.delta", Raw: map[string]any{
				"call_id": "call_1",
				"name":    "lookup_weather",
				"delta":   `{"city":"`,
			}},
			{Type: "response.output_text.delta", Raw: map[string]any{"delta": `{"pair":{"0":"left",`}},
			{Type: "response.output_text.delta", Raw: map[string]any{"delta": `"1":2}}`}},
			{Type: "response.completed", Raw: map[string]any{
				"response_id": "resp_123",
				"response": map[string]any{
					"id":          "resp_123",
					"status":      "completed",
					"output_text": `{"pair":{"0":"left","1":2}}`,
				},
			}},
		},
	}

	app.streamChatCompletion(ctx, accounts.Record{}, normalized, stream)
	body := recorder.Body.String()

	if strings.Contains(body, `"content":"{\"pair\":{\"0\":\"left\"`) {
		t.Fatal("raw tuple JSON delta should not be streamed")
	}
	if !strings.Contains(body, `"tool_calls":[{"function":{"arguments":"{\"city\":\""},"index":0}]`) {
		t.Fatalf("expected streamed tool call delta, body = %s", body)
	}
	if !strings.Contains(body, `"content":"{\"pair\":[\"left\",2]}"`) {
		t.Fatalf("expected reconverted tuple JSON delta, body = %s", body)
	}
}

func TestStreamResponsesBuffersTupleJSONAndPatchesCompletedPayload(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	logs := new(bytes.Buffer)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	normalized := translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
		TupleSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pair": map[string]any{
					"type": "array",
					"prefixItems": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "number"},
					},
				},
			},
		},
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.output_text.delta", Raw: map[string]any{"delta": `{"pair":{"0":"left",`}},
			{Type: "response.output_text.done", Raw: map[string]any{"text": `{"pair":{"0":"left","1":2}}`}},
			{Type: "response.completed", Raw: map[string]any{
				"response_id": "resp_456",
				"response": map[string]any{
					"id":          "resp_456",
					"status":      "completed",
					"output_text": `{"pair":{"0":"left","1":2}}`,
					"output": []any{
						map[string]any{
							"type": "message",
							"content": []any{
								map[string]any{
									"type": "output_text",
									"text": `{"pair":{"0":"left","1":2}}`,
								},
							},
						},
					},
				},
			}},
		},
	}

	app.streamResponses(ctx, accounts.Record{}, normalized, stream)
	body := recorder.Body.String()

	if strings.Contains(body, `event: response.output_text.done`) {
		t.Fatalf("response.output_text.done should be suppressed, body = %s", body)
	}
	if !strings.Contains(body, `event: response.output_text.delta`) || !strings.Contains(body, `\"pair\":[\"left\",2]`) {
		t.Fatalf("expected synthetic reconverted delta, body = %s", body)
	}
	if !strings.Contains(body, `"output_text":"{\"pair\":[\"left\",2]}"`) {
		t.Fatalf("expected patched completed payload, body = %s", body)
	}
}

func TestStreamChatCompletionReturnsStreamErrorOnEarlyEOF(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_stream_chat_eof",
			AccountID: "upstream_stream_chat_eof",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	account, ok := accountsSvc.Get("acct_stream_chat_eof")
	if !ok {
		t.Fatal("Get() returned false")
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.output_text.delta", Raw: map[string]any{"delta": "partial"}},
		},
	}

	app.streamChatCompletion(ctx, account, translate.NormalizedRequest{
		Endpoint: translate.EndpointChat,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	body := recorder.Body.String()
	if strings.Contains(body, "[DONE]") {
		t.Fatalf("stream should not finish cleanly on early EOF, body = %s", body)
	}
	if !strings.Contains(body, errIncompleteResponse.Error()) {
		t.Fatalf("expected incomplete response error in stream, body = %s", body)
	}

	updated, ok := accountsSvc.Get("acct_stream_chat_eof")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
}

func TestStreamResponsesReturnsStreamErrorOnEarlyEOF(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_stream_responses_eof",
			AccountID: "upstream_stream_responses_eof",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	account, ok := accountsSvc.Get("acct_stream_responses_eof")
	if !ok {
		t.Fatal("Get() returned false")
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.output_text.delta", Raw: map[string]any{"delta": "partial"}},
		},
	}

	app.streamResponses(ctx, account, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	body := recorder.Body.String()
	if strings.Contains(body, "event: done") || strings.Contains(body, "[DONE]") {
		t.Fatalf("stream should not finish cleanly on early EOF, body = %s", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, errIncompleteResponse.Error()) {
		t.Fatalf("expected error event for incomplete stream, body = %s", body)
	}

	updated, ok := accountsSvc.Get("acct_stream_responses_eof")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
}

func TestStreamChatCompletionClassifiesStructuredRateLimitFailure(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_stream_chat_rate_limit",
			AccountID: "upstream_stream_chat_rate_limit",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{RateLimitFallback: 60 * time.Second, QuotaFallback: 5 * time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	account, ok := accountsSvc.Get("acct_stream_chat_rate_limit")
	if !ok {
		t.Fatal("Get() returned false")
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "error", Raw: map[string]any{
				"error": map[string]any{
					"code":              "rate_limited",
					"message":           "Too many requests",
					"resets_in_seconds": 12,
				},
			}},
		},
	}

	app.streamChatCompletion(ctx, account, translate.NormalizedRequest{
		Endpoint: translate.EndpointChat,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	body := recorder.Body.String()
	if !strings.Contains(body, "upstream account rate limited") {
		t.Fatalf("expected classified rate limit message in stream body, body = %s", body)
	}

	updated, ok := accountsSvc.Get("acct_stream_chat_rate_limit")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.BlockState.Reason != accounts.BlockRateLimit {
		t.Fatalf("block_reason = %q, want rate_limit", updated.BlockState.Reason)
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
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
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_stream_responses_unauthorized",
			AccountID: "upstream_stream_responses_unauthorized",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{RateLimitFallback: 60 * time.Second, QuotaFallback: 5 * time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	account, ok := accountsSvc.Get("acct_stream_responses_unauthorized")
	if !ok {
		t.Fatal("Get() returned false")
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.failed", Raw: map[string]any{
				"response": map[string]any{
					"id": "resp_unauthorized",
					"error": map[string]any{
						"code":    "invalid_token",
						"message": "Session expired",
					},
				},
			}},
		},
	}

	app.streamResponses(ctx, account, translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	body := recorder.Body.String()
	if !strings.Contains(body, "upstream account unauthorized") {
		t.Fatalf("expected classified unauthorized message in stream body, body = %s", body)
	}

	updated, ok := accountsSvc.Get("acct_stream_responses_unauthorized")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.Status != accounts.StatusExpired {
		t.Fatalf("status = %q, want expired", updated.Status)
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
}

func TestLogCompatibilityWarningsDoesNotLeakRequestBody(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	logs := new(bytes.Buffer)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"secret":"do-not-log-me"}`))
	ctx.Set(middleware.RequestIDKey, "req-test")

	app := &App{
		logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	app.logCompatibilityWarnings(ctx, "chat_completions", []translate.CompatibilityWarning{{
		Field:    "temperature",
		Behavior: "ignored_with_warning",
		Detail:   "field is accepted for compatibility but not applied in this proxy",
	}})

	if strings.Contains(logs.String(), "do-not-log-me") {
		t.Fatal("request body appeared in logs")
	}

	var entry map[string]any
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if entry["level"] != "WARN" {
		t.Fatalf("level = %#v, want WARN", entry["level"])
	}
	if entry["field"] != "temperature" {
		t.Fatalf("field = %#v, want temperature", entry["field"])
	}
}

func TestStreamResponsesSuppressesCodexRateLimitsEvent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	logs := new(bytes.Buffer)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_test",
			AccountID: "upstream_test",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	normalized := translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Model:    "gpt-5.4",
		Stream:   true,
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "codex.rate_limits", Raw: map[string]any{
				"rate_limits": map[string]any{
					"primary": map[string]any{
						"used_percent":   100.0,
						"window_minutes": 300.0,
						"reset_at":       float64(now.Add(5 * time.Minute).Unix()),
					},
				},
			}},
			{Type: "response.output_text.delta", Raw: map[string]any{"delta": "hello"}},
			{Type: "response.completed", Raw: map[string]any{
				"response_id": "resp_789",
				"response": map[string]any{
					"id":          "resp_789",
					"status":      "completed",
					"output_text": "hello",
				},
			}},
		},
	}

	account, ok := accountsSvc.Get("acct_test")
	if !ok {
		t.Fatal("Get() returned false")
	}

	app.streamResponses(ctx, account, normalized, stream)
	body := recorder.Body.String()

	if strings.Contains(body, "codex.rate_limits") {
		t.Fatalf("codex.rate_limits event leaked downstream: %s", body)
	}

	updated, ok := accountsSvc.Get("acct_test")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.CachedQuota == nil || updated.CachedQuota.RateLimit.UsedPercent == nil || *updated.CachedQuota.RateLimit.UsedPercent != 100 {
		t.Fatalf("cached_quota = %#v, want primary used_percent 100", updated.CachedQuota)
	}
	if updated.BlockState.Reason != accounts.BlockQuotaPrimary {
		t.Fatalf("block_reason = %q, want quota_primary", updated.BlockState.Reason)
	}
}

func TestCollectEventsRecordsRequestWithoutUsageBlock(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_usage_less",
			AccountID: "upstream_usage_less",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	account, ok := accountsSvc.Get("acct_usage_less")
	if !ok {
		t.Fatal("Get() returned false")
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.completed", Raw: map[string]any{
				"response_id": "resp_usage_less",
				"response": map[string]any{
					"id":     "resp_usage_less",
					"status": "completed",
				},
			}},
		},
	}

	accumulator, err := app.collectEvents(account, translate.NormalizedRequest{Endpoint: translate.EndpointResponses, Model: "gpt-5.4"}, stream)
	if err != nil {
		t.Fatalf("collectEvents() error = %v", err)
	}
	if accumulator.Usage != nil {
		t.Fatalf("accumulator.Usage = %#v, want nil", accumulator.Usage)
	}

	updated, ok := accountsSvc.Get("acct_usage_less")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
	if updated.LocalUsage.LastUsedAt == nil {
		t.Fatal("last_used_at = nil, want timestamp")
	}
	if updated.LocalUsage.EmptyResponseCount != 1 {
		t.Fatalf("empty_response_count = %d, want 1", updated.LocalUsage.EmptyResponseCount)
	}
}

func TestCollectEventsDoesNotMarkToolCallOnlyResponseAsEmpty(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_tool_only",
			AccountID: "upstream_tool_only",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:           config.Config{ContinuationTTL: time.Minute},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts:      accountsSvc,
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	account, ok := accountsSvc.Get("acct_tool_only")
	if !ok {
		t.Fatal("Get() returned false")
	}

	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{Type: "response.function_call_arguments.done", Raw: map[string]any{
				"call_id":     "call_1",
				"name":        "lookup_weather",
				"arguments":   `{"city":"SF"}`,
				"response_id": "resp_tool_only",
			}},
			{Type: "response.completed", Raw: map[string]any{
				"response_id": "resp_tool_only",
				"response": map[string]any{
					"id":     "resp_tool_only",
					"status": "completed",
				},
			}},
		},
	}

	if _, err := app.collectEvents(account, translate.NormalizedRequest{Endpoint: translate.EndpointResponses, Model: "gpt-5.4"}, stream); err != nil {
		t.Fatalf("collectEvents() error = %v", err)
	}

	updated, ok := accountsSvc.Get("acct_tool_only")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
	if updated.LocalUsage.EmptyResponseCount != 0 {
		t.Fatalf("empty_response_count = %d, want 0", updated.LocalUsage.EmptyResponseCount)
	}
}

func TestCollectEventsCountsIncompleteOrFailedResponseAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		accountID string
		events    []*codex.StreamEvent
		wantErr   func(error) bool
	}{
		{
			name:      "early eof before completion",
			accountID: "acct_early_eof",
			events: []*codex.StreamEvent{
				{Type: "response.output_text.delta", Raw: map[string]any{"delta": "partial"}},
			},
			wantErr: func(err error) bool { return errors.Is(err, errIncompleteResponse) },
		},
		{
			name:      "response.failed event",
			accountID: "acct_failed_attempt",
			events: []*codex.StreamEvent{
				{Type: "response.failed", Raw: map[string]any{
					"error": map[string]any{
						"message": "upstream failure",
					},
				}},
			},
			wantErr: func(err error) bool {
				var upstreamErr *codex.UpstreamError
				return err != nil && errors.As(err, &upstreamErr) && upstreamErr.Message() == "upstream failure"
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now().UTC()
			accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
				Records: []*accounts.Record{{
					ID:        tc.accountID,
					AccountID: "upstream_" + tc.accountID,
					Status:    accounts.StatusActive,
					CreatedAt: now,
					UpdatedAt: now,
				}},
			}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			app := &App{
				cfg:           config.Config{ContinuationTTL: time.Minute},
				logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
				accounts:      accountsSvc,
				continuations: accounts.NewContinuationManager(time.Minute),
			}

			account, ok := accountsSvc.Get(tc.accountID)
			if !ok {
				t.Fatal("Get() returned false")
			}

			stream := &fakeEventStream{events: tc.events}

			if _, err := app.collectEvents(account, translate.NormalizedRequest{Endpoint: translate.EndpointResponses, Model: "gpt-5.4"}, stream); !tc.wantErr(err) {
				t.Fatalf("collectEvents() error = %v, want expected failure", err)
			}

			updated, ok := accountsSvc.Get(tc.accountID)
			if !ok {
				t.Fatal("Get(updated) returned false")
			}
			if updated.LocalUsage.RequestCount != 1 {
				t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
			}
			if updated.LocalUsage.EmptyResponseCount != 0 {
				t.Fatalf("empty_response_count = %d, want 0", updated.LocalUsage.EmptyResponseCount)
			}
		})
	}
}

func TestQuotaBlockReasonIgnoresExpiredSecondaryWindow(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resetPast := now.Add(-time.Minute)
	usedPercent := 100.0
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_stale_quota",
			AccountID: "upstream_stale_quota",
			Status:    accounts.StatusActive,
			CachedQuota: &accounts.QuotaSnapshot{
				RateLimit: accounts.RateLimitWindow{
					Allowed: true,
				},
				SecondaryRateLimit: &accounts.RateLimitWindow{
					Allowed:      true,
					LimitReached: true,
					UsedPercent:  &usedPercent,
					ResetAt:      &resetPast,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{accounts: accountsSvc}
	if reason := app.quotaBlockReason("acct_stale_quota"); reason != accounts.BlockQuotaPrimary {
		t.Fatalf("quotaBlockReason() = %q, want quota_primary", reason)
	}

	updated, ok := accountsSvc.Get("acct_stale_quota")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.CachedQuota == nil || updated.CachedQuota.SecondaryRateLimit == nil {
		t.Fatalf("cached quota missing secondary window: %#v", updated.CachedQuota)
	}
	if updated.CachedQuota.SecondaryRateLimit.LimitReached {
		t.Fatalf("secondary limit should have been cleared after expiry: %#v", updated.CachedQuota.SecondaryRateLimit)
	}
}

func TestBlockUntilUsesRetryAfterBeforeFallback(t *testing.T) {
	t.Parallel()

	accountsSvc, err := accounts.NewService(&memoryAccountsStore{}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
		},
		accounts: accountsSvc,
	}

	start := time.Now().UTC()
	until := app.blockUntil("", accounts.BlockRateLimit, io.EOF)
	if until == nil {
		t.Fatal("blockUntil() = nil")
	}
	if until.Sub(start) < 59*time.Second || until.Sub(start) > 61*time.Second {
		t.Fatalf("fallback block duration = %v, want about 60s", until.Sub(start))
	}

	start = time.Now().UTC()
	until = app.blockUntil("acct_missing", accounts.BlockRateLimit, errors.New("429 too many requests retry-after: 10"))
	if until == nil {
		t.Fatal("blockUntil(retry-after) = nil")
	}
	if until.Sub(start) < 9*time.Second || until.Sub(start) > 11*time.Second {
		t.Fatalf("retry-after block duration = %v, want about 10s", until.Sub(start))
	}

	start = time.Now().UTC()
	until = app.blockUntil("acct_missing", accounts.BlockQuotaPrimary, io.EOF)
	if until == nil {
		t.Fatal("blockUntil(quota fallback) = nil")
	}
	if until.Sub(start) < 4*time.Minute || until.Sub(start) > 6*time.Minute {
		t.Fatalf("quota fallback block duration = %v, want about 5m", until.Sub(start))
	}
}

func TestBlockUntilForRateLimitUsesPrimaryQuotaResetOnly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	primaryReset := now.Add(2 * time.Minute)
	secondaryReset := now.Add(2 * time.Hour)
	codeReviewReset := now.Add(3 * time.Hour)
	usedPercent := 100.0
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_latest_reset",
			AccountID: "upstream_latest_reset",
			Status:    accounts.StatusActive,
			CachedQuota: &accounts.QuotaSnapshot{
				RateLimit: accounts.RateLimitWindow{
					Allowed:      true,
					LimitReached: true,
					UsedPercent:  &usedPercent,
					ResetAt:      &primaryReset,
				},
				SecondaryRateLimit: &accounts.RateLimitWindow{
					Allowed:      true,
					LimitReached: true,
					UsedPercent:  &usedPercent,
					ResetAt:      &secondaryReset,
				},
				CodeReviewRateLimit: &accounts.RateLimitWindow{
					Allowed:      true,
					LimitReached: true,
					UsedPercent:  &usedPercent,
					ResetAt:      &codeReviewReset,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
		},
		accounts: accountsSvc,
	}

	start := time.Now().UTC()
	until := app.blockUntil("acct_latest_reset", accounts.BlockRateLimit, errors.New("429 too many requests retry-after: 10"))
	if until == nil {
		t.Fatal("blockUntil() = nil")
	}
	if until.Sub(start) < 110*time.Second || until.Sub(start) > 130*time.Second {
		t.Fatalf("block duration = %v, want about 2m based on primary reset", until.Sub(start))
	}
}

func TestObserveQuotaSnapshotPersistsOnce(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	store := &memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_quota_once",
			AccountID: "upstream_quota_once",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}
	accountsSvc, err := accounts.NewService(store, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	store.saveCount = 0

	app := &App{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	resetAt := now.Add(5 * time.Minute)
	windowSeconds := 300
	usedPercent := 80.0
	app.observeQuotaSnapshot("acct_quota_once", &accounts.QuotaSnapshot{
		RateLimit: accounts.RateLimitWindow{
			Allowed:            true,
			UsedPercent:        &usedPercent,
			ResetAt:            &resetAt,
			LimitWindowSeconds: &windowSeconds,
		},
	})

	if store.saveCount != 1 {
		t.Fatalf("saveCount = %d, want 1", store.saveCount)
	}
}

func TestHandleOpenStreamErrorCountsAndBlocksActualRateLimitedAccount(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_request_error",
			AccountID: "upstream_request_error",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:      config.Config{RateLimitFallback: 60 * time.Second, QuotaFallback: 5 * time.Minute},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	app.handleOpenStreamError(ctx, "responses", "acct_request_error", "preferred_only", &codex.UpstreamError{
		Op:         "codex response",
		StatusCode: http.StatusTooManyRequests,
		Body:       "quota exceeded retry-after: 10",
	})

	updated, ok := accountsSvc.Get("acct_request_error")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
	if updated.BlockState.Reason != accounts.BlockRateLimit {
		t.Fatalf("block_reason = %q, want rate_limit on the actual failing account", updated.BlockState.Reason)
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	app.handleOpenStreamError(ctx, "responses", "acct_request_error", "preferred_only", errors.New("503 upstream unavailable"))

	updated, ok = accountsSvc.Get("acct_request_error")
	if !ok {
		t.Fatal("Get(updated second) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count after non-429 = %d, want still 1", updated.LocalUsage.RequestCount)
	}
}

func TestHandleOpenStreamErrorDoesNotDoubleCountWhenIDsMatch(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_same_id",
			AccountID: "upstream_same_id",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg:      config.Config{RateLimitFallback: 60 * time.Second, QuotaFallback: 5 * time.Minute},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	app.handleOpenStreamError(ctx, "chat_completions", "acct_same_id", "acct_same_id", &codex.UpstreamError{
		Op:         "codex response",
		StatusCode: http.StatusTooManyRequests,
		Body:       "rate limited retry-after: 10",
	})

	updated, ok := accountsSvc.Get("acct_same_id")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", updated.LocalUsage.RequestCount)
	}
	if updated.BlockState.Reason != accounts.BlockRateLimit {
		t.Fatalf("block_reason = %q, want rate_limit", updated.BlockState.Reason)
	}
}

func TestClassifyUpstreamErrorPrefersRateLimitOverQuotaText(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_rate_limit",
			AccountID: "upstream_rate_limit",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
		},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	status, code, _ := app.classifyUpstreamError("acct_rate_limit", &codex.UpstreamError{
		Op:         "codex response",
		StatusCode: http.StatusTooManyRequests,
		Body:       "quota exceeded retry-after: 10",
	})
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status)
	}
	if code != "rate_limited" {
		t.Fatalf("code = %q, want rate_limited", code)
	}

	updated, ok := accountsSvc.Get("acct_rate_limit")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.BlockState.Reason != accounts.BlockRateLimit {
		t.Fatalf("block_reason = %q, want rate_limit", updated.BlockState.Reason)
	}
	if updated.LocalUsage.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1 for explicit 429", updated.LocalUsage.RequestCount)
	}
}

func TestClassifyUpstreamErrorPrefersQuotaWhen402MentionsRateLimit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
		Records: []*accounts.Record{{
			ID:        "acct_quota_with_rate_limit_text",
			AccountID: "upstream_quota_with_rate_limit_text",
			Status:    accounts.StatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	app := &App{
		cfg: config.Config{
			RateLimitFallback: 60 * time.Second,
			QuotaFallback:     5 * time.Minute,
		},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		accounts: accountsSvc,
	}

	status, code, _ := app.classifyUpstreamError("acct_quota_with_rate_limit_text", &codex.UpstreamError{
		Op:         "codex response",
		StatusCode: http.StatusPaymentRequired,
		Body:       "rate limit exceeded for this billing period",
	})
	if status != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", status)
	}
	if code != "quota_exhausted" {
		t.Fatalf("code = %q, want quota_exhausted", code)
	}

	updated, ok := accountsSvc.Get("acct_quota_with_rate_limit_text")
	if !ok {
		t.Fatal("Get(updated) returned false")
	}
	if updated.BlockState.Reason != accounts.BlockQuotaPrimary {
		t.Fatalf("block_reason = %q, want quota_primary", updated.BlockState.Reason)
	}
	if updated.BlockState.Until == nil {
		t.Fatal("block_until = nil, want fallback quota block")
	}
	duration := updated.BlockState.Until.Sub(time.Now().UTC())
	if duration < 4*time.Minute || duration > 6*time.Minute {
		t.Fatalf("block duration = %v, want about 5m", duration)
	}
	if updated.LocalUsage.RequestCount != 0 {
		t.Fatalf("request_count = %d, want 0 for quota classification", updated.LocalUsage.RequestCount)
	}
}

func TestClassifyUpstreamErrorHandlesStructuredStreamFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		accountID       string
		event           *codex.StreamEvent
		wantStatus      int
		wantCode        string
		wantBlockReason accounts.BlockReason
		wantRequests    int64
	}{
		{
			name:      "top-level error rate limit",
			accountID: "acct_stream_rate_limit",
			event: &codex.StreamEvent{Type: "error", Raw: map[string]any{
				"error": map[string]any{
					"code":              "rate_limited",
					"message":           "Too many requests",
					"resets_in_seconds": 12,
				},
			}},
			wantStatus:      http.StatusTooManyRequests,
			wantCode:        "rate_limited",
			wantBlockReason: accounts.BlockRateLimit,
			wantRequests:    1,
		},
		{
			name:      "response.failed quota exhaustion",
			accountID: "acct_stream_quota",
			event: &codex.StreamEvent{Type: "response.failed", Raw: map[string]any{
				"response": map[string]any{
					"id": "resp_1",
					"error": map[string]any{
						"code":    "usage_limit_reached",
						"message": "Billing period exhausted",
					},
				},
			}},
			wantStatus:      http.StatusPaymentRequired,
			wantCode:        "quota_exhausted",
			wantBlockReason: accounts.BlockQuotaPrimary,
			wantRequests:    0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now().UTC()
			accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
				Records: []*accounts.Record{{
					ID:        tc.accountID,
					AccountID: "upstream_" + tc.accountID,
					Status:    accounts.StatusActive,
					CreatedAt: now,
					UpdatedAt: now,
				}},
			}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			app := &App{
				cfg:      config.Config{RateLimitFallback: 60 * time.Second, QuotaFallback: 5 * time.Minute},
				logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
				accounts: accountsSvc,
			}

			err = upstreamEventError(tc.event)
			status, code, _ := app.classifyUpstreamError(tc.accountID, err)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}

			updated, ok := accountsSvc.Get(tc.accountID)
			if !ok {
				t.Fatal("Get(updated) returned false")
			}
			if updated.BlockState.Reason != tc.wantBlockReason {
				t.Fatalf("block_reason = %q, want %q", updated.BlockState.Reason, tc.wantBlockReason)
			}
			if updated.LocalUsage.RequestCount != tc.wantRequests {
				t.Fatalf("request_count = %d, want %d", updated.LocalUsage.RequestCount, tc.wantRequests)
			}
		})
	}
}

func TestClassifyUpstreamErrorRejectsWeakRateLimitSignals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		accountID   string
		err         error
		wantMessage string
	}{
		{
			name:        "generic upstream failure",
			accountID:   "acct_generic_error",
			err:         errors.New("failed to generate response"),
			wantMessage: "failed to generate response",
		},
		{
			name:        "retry-after without 429",
			accountID:   "acct_retry_after_only",
			err:         errors.New("503 service unavailable retry-after: 120"),
			wantMessage: "503 service unavailable retry-after: 120",
		},
		{
			name:        "plain text quota signal without typed upstream status",
			accountID:   "acct_plain_quota",
			err:         errors.New("proxy quota middleware unavailable"),
			wantMessage: "proxy quota middleware unavailable",
		},
		{
			name:        "plain text unauthorized signal without typed upstream status",
			accountID:   "acct_plain_unauthorized",
			err:         errors.New("local proxy unauthorized to upstream"),
			wantMessage: "local proxy unauthorized to upstream",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now().UTC()
			accountsSvc, err := accounts.NewService(&memoryAccountsStore{state: accounts.State{
				Records: []*accounts.Record{{
					ID:        tc.accountID,
					AccountID: "upstream_" + tc.accountID,
					Status:    accounts.StatusActive,
					CreatedAt: now,
					UpdatedAt: now,
				}},
			}}, accounts.RotationLeastUsed, accounts.ServiceOptions{})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			app := &App{
				cfg:      config.Config{RateLimitFallback: 60 * time.Second, QuotaFallback: 5 * time.Minute},
				logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
				accounts: accountsSvc,
			}

			status, code, message := app.classifyUpstreamError(tc.accountID, tc.err)
			if status != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", status)
			}
			if code != "upstream_error" {
				t.Fatalf("code = %q, want upstream_error", code)
			}
			if message != tc.wantMessage {
				t.Fatalf("message = %q, want %q", message, tc.wantMessage)
			}

			updated, ok := accountsSvc.Get(tc.accountID)
			if !ok {
				t.Fatal("Get(updated) returned false")
			}
			if updated.BlockState.Reason != accounts.BlockNone {
				t.Fatalf("block_reason = %q, want none", updated.BlockState.Reason)
			}
		})
	}
}
