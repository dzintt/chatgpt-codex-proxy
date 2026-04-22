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

func TestNormalizeChatCompletionsBodyAcceptsResponsesShape(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "gpt-5.4",
		"instructions": "Be concise.",
		"input": {
			"role": "user",
			"content": [{"type": "text", "text": "hello"}]
		},
		"stream": true,
		"tools": [{
			"type": "function",
			"name": "Shell",
			"description": "Run a shell command",
			"parameters": {
				"type": "object"
			}
		}]
	}`)

	normalized, err := normalizeChatCompletionsBody(body, "gpt-5.4")
	if err != nil {
		t.Fatalf("normalizeChatCompletionsBody() error = %v", err)
	}

	if normalized.Endpoint != translate.EndpointChat {
		t.Fatalf("endpoint = %q, want %q", normalized.Endpoint, translate.EndpointChat)
	}
	if normalized.Instructions != "Be concise." {
		t.Fatalf("instructions = %q, want Be concise.", normalized.Instructions)
	}
	if !normalized.Stream {
		t.Fatal("stream = false, want true")
	}
	if len(normalized.Input) != 1 {
		t.Fatalf("len(input) = %d, want 1", len(normalized.Input))
	}
	if normalized.Input[0].Role != "user" {
		t.Fatalf("input role = %q, want user", normalized.Input[0].Role)
	}
	if got := normalized.Input[0].Content[0].Text; got != "hello" {
		t.Fatalf("input text = %q, want hello", got)
	}
	if len(normalized.Tools) != 1 || normalized.Tools[0].Name != "Shell" {
		t.Fatalf("tools = %#v, want Shell passthrough", normalized.Tools)
	}
}

func TestNormalizeChatCompletionsBodyLiftsInstructionRolesFromResponsesShape(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{"role": "system", "content": "You are GPT-5.4."},
			{"role": "user", "content": "Explain this repository."},
			{"role": "user", "content": [{"type": "input_text", "text": "<user_query>\nwhat does this project do\n</user_query>"}]}
		],
		"tools": [{
			"type": "function",
			"name": "Shell",
			"description": "Run a shell command",
			"parameters": {
				"type": "object"
			}
		}]
	}`)

	normalized, err := normalizeChatCompletionsBody(body, "gpt-5.4")
	if err != nil {
		t.Fatalf("normalizeChatCompletionsBody() error = %v", err)
	}

	if normalized.Endpoint != translate.EndpointChat {
		t.Fatalf("endpoint = %q, want %q", normalized.Endpoint, translate.EndpointChat)
	}
	if normalized.Instructions != "You are GPT-5.4." {
		t.Fatalf("instructions = %q, want lifted system instructions", normalized.Instructions)
	}
	if len(normalized.Input) != 2 {
		t.Fatalf("len(input) = %d, want 2", len(normalized.Input))
	}
	if normalized.Input[0].Role != "user" || normalized.Input[1].Role != "user" {
		t.Fatalf("input roles = %#v, want only user items", normalized.Input)
	}
}

func TestNormalizeChatCompletionsBodyAcceptsArrayToolOutputInResponsesShape(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{"role": "assistant", "type": "function_call", "call_id": "call_1", "name": "Glob", "arguments": "{\"glob_pattern\":\"README*\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": [
				{"type": "output_text", "text": "Result of search"},
				{"type": "output_text", "text": "README.md"}
			]},
			{"role": "user", "content": "explain this project"}
		]
	}`)

	normalized, err := normalizeChatCompletionsBody(body, "gpt-5.4")
	if err != nil {
		t.Fatalf("normalizeChatCompletionsBody() error = %v", err)
	}

	if len(normalized.Input) != 3 {
		t.Fatalf("len(input) = %d, want 3", len(normalized.Input))
	}
	if normalized.Input[1].Type != "function_call_output" {
		t.Fatalf("input[1].Type = %q, want function_call_output", normalized.Input[1].Type)
	}
	if normalized.Input[1].OutputText != "" {
		t.Fatalf("input[1].OutputText = %q, want empty", normalized.Input[1].OutputText)
	}
	if len(normalized.Input[1].OutputContent) != 2 {
		t.Fatalf("len(input[1].OutputContent) = %d, want 2", len(normalized.Input[1].OutputContent))
	}
	if normalized.Input[1].OutputContent[0].Text != "Result of search" || normalized.Input[1].OutputContent[1].Text != "README.md" {
		t.Fatalf("input[1].OutputContent = %#v, want preserved output parts", normalized.Input[1].OutputContent)
	}
}

func TestNormalizeChatCompletionsBodyPrefersMessagesShape(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "hello"}],
		"instructions": "ignored",
		"input": {"role": "user", "content": [{"type": "text", "text": "ignored"}]}
	}`)

	normalized, err := normalizeChatCompletionsBody(body, "gpt-5.4")
	if err != nil {
		t.Fatalf("normalizeChatCompletionsBody() error = %v", err)
	}

	if len(normalized.Input) != 1 {
		t.Fatalf("len(input) = %d, want 1", len(normalized.Input))
	}
	if got := normalized.Input[0].Content[0].Text; got != "hello" {
		t.Fatalf("input text = %q, want hello", got)
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
			ContinuationTTL: time.Minute,
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
			ContinuationTTL: time.Minute,
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
			ContinuationTTL: time.Minute,
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

func TestContinuationInputHistoryIncludesAssistantReplay(t *testing.T) {
	t.Parallel()

	accumulator := translate.NewAccumulator(translate.NormalizedRequest{})
	accumulator.Normalized.Input = []codex.InputItem{{
		Role: "user",
		Type: "message",
		Content: []codex.ContentPart{{
			Type:     "input_text",
			Text:     "hello",
			Detail:   "low",
			FileURL:  "file://example",
			FileData: "raw",
			FileID:   "file_123",
			Filename: "demo.txt",
		}},
	}}
	accumulator.TextBuilder.WriteString("assistant replay")

	history := continuationInputHistory(accumulator)
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if history[0].Role != "user" || history[0].Content[0].Text != "hello" {
		t.Fatalf("history[0] = %#v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content[0].Text != "assistant replay" {
		t.Fatalf("history[1] = %#v", history[1])
	}
}

func TestStreamChatCompletionEmitsReasoningContentAndStrictUsage(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_chat_reasoning",
		AccountID: "upstream_chat_reasoning",
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

	record, _ := accountsSvc.Get("acct_chat_reasoning")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.reasoning_summary_text.delta",
				Raw: map[string]any{
					"response_id": "resp_chat_reasoning",
					"delta":       "Reasoning summary",
				},
			},
			{
				Type: "response.output_text.delta",
				Raw: map[string]any{
					"response_id": "resp_chat_reasoning",
					"delta":       "Final answer",
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":          "resp_chat_reasoning",
						"model":       "gpt-5.4",
						"status":      "completed",
						"output_text": "Final answer",
						"usage": map[string]any{
							"input_tokens":  12,
							"output_tokens": 5,
							"input_tokens_details": map[string]any{
								"cached_tokens": 4,
							},
							"output_tokens_details": map[string]any{
								"reasoning_tokens": 2,
							},
						},
					},
				},
			},
		},
	}

	app.streamChatCompletion(ctx, record, translate.NormalizedRequest{
		Endpoint:  translate.EndpointChat,
		Model:     "gpt-5.4",
		Stream:    true,
		Reasoning: &codex.Reasoning{Effort: "high"},
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	if len(events) != 5 {
		t.Fatalf("event count = %d, want 5", len(events))
	}
	reasoningDelta := events[1].Data
	choices := sliceOfMapsFromAny(reasoningDelta["choices"])
	delta := nestedMapFromAny(choices[0]["delta"])
	if delta["reasoning_content"] != "Reasoning summary" {
		t.Fatalf("reasoning_content = %#v, want reasoning summary", delta["reasoning_content"])
	}
	finalChunk := events[3].Data
	usage := nestedMapFromAny(finalChunk["usage"])
	if usage["prompt_tokens"] != float64(12) {
		t.Fatalf("prompt_tokens = %#v, want 12", usage["prompt_tokens"])
	}
	if usage["completion_tokens"] != float64(5) {
		t.Fatalf("completion_tokens = %#v, want 5", usage["completion_tokens"])
	}
	promptDetails := nestedMapFromAny(usage["prompt_tokens_details"])
	if promptDetails["cached_tokens"] != float64(4) {
		t.Fatalf("cached_tokens = %#v, want 4", promptDetails["cached_tokens"])
	}
	completionDetails := nestedMapFromAny(usage["completion_tokens_details"])
	if completionDetails["reasoning_tokens"] != float64(2) {
		t.Fatalf("reasoning_tokens = %#v, want 2", completionDetails["reasoning_tokens"])
	}
}

func TestStreamChatCompletionDoesNotSynthesizeReasoningContentFromCompletedOutput(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_chat_reasoning_fallback",
		AccountID: "upstream_chat_reasoning_fallback",
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

	record, _ := accountsSvc.Get("acct_chat_reasoning_fallback")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.output_text.delta",
				Raw: map[string]any{
					"response_id": "resp_chat_reasoning_fallback",
					"delta":       "Final answer",
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":          "resp_chat_reasoning_fallback",
						"model":       "gpt-5.4",
						"status":      "completed",
						"output_text": "Final answer",
						"output": []any{
							map[string]any{
								"type":   "reasoning",
								"id":     "rs_1",
								"status": "completed",
								"summary": []any{
									map[string]any{
										"type": "summary_text",
										"text": "Recovered summary",
									},
								},
							},
							map[string]any{
								"type":   "message",
								"role":   "assistant",
								"status": "completed",
								"content": []any{
									map[string]any{
										"type": "output_text",
										"text": "Final answer",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	app.streamChatCompletion(ctx, record, translate.NormalizedRequest{
		Endpoint:  translate.EndpointChat,
		Model:     "gpt-5.4",
		Stream:    true,
		Reasoning: &codex.Reasoning{Effort: "high"},
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4", len(events))
	}
	textDelta := events[1].Data
	choices := sliceOfMapsFromAny(textDelta["choices"])
	delta := nestedMapFromAny(choices[0]["delta"])
	if _, ok := delta["reasoning_content"]; ok {
		t.Fatalf("unexpected synthesized reasoning_content = %#v", delta["reasoning_content"])
	}
}

func TestStreamChatCompletionUsesToolNameFromOutputItemWhenArgumentEventsOmitIt(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_chat_tool_name",
		AccountID: "upstream_chat_tool_name",
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

	record, _ := accountsSvc.Get("acct_chat_tool_name")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.function_call_arguments.delta",
				Raw: map[string]any{
					"response_id":  "resp_chat_tool_name",
					"item_id":      "fc_tool",
					"output_index": 0,
					"delta":        `{"path":"C:\\`,
				},
			},
			{
				Type: "response.output_item.added",
				Raw: map[string]any{
					"response_id":  "resp_chat_tool_name",
					"output_index": 0,
					"item": map[string]any{
						"id":        "fc_tool",
						"call_id":   "call_glob",
						"type":      "function_call",
						"name":      "Glob",
						"arguments": `{"path":"C:\\`,
						"status":    "in_progress",
					},
				},
			},
			{
				Type: "response.function_call_arguments.done",
				Raw: map[string]any{
					"response_id":  "resp_chat_tool_name",
					"item_id":      "fc_tool",
					"output_index": 0,
					"arguments":    `{"path":"C:\\Users\\Anson"}`,
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_chat_tool_name",
						"model":  "gpt-5.4",
						"status": "completed",
					},
				},
			},
		},
	}

	app.streamChatCompletion(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointChat,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6", len(events))
	}

	toolNameChunk := events[1].Data
	choices := sliceOfMapsFromAny(toolNameChunk["choices"])
	delta := nestedMapFromAny(choices[0]["delta"])
	toolCalls := sliceOfMapsFromAny(delta["tool_calls"])
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(toolCalls))
	}
	if toolCalls[0]["id"] != "call_glob" {
		t.Fatalf("tool_calls[0].id = %#v, want call_glob", toolCalls[0]["id"])
	}
	function := nestedMapFromAny(toolCalls[0]["function"])
	if function["name"] != "Glob" {
		t.Fatalf("function.name = %#v, want Glob", function["name"])
	}

	firstArgumentsChunk := events[2].Data
	choices = sliceOfMapsFromAny(firstArgumentsChunk["choices"])
	delta = nestedMapFromAny(choices[0]["delta"])
	toolCalls = sliceOfMapsFromAny(delta["tool_calls"])
	function = nestedMapFromAny(toolCalls[0]["function"])
	firstArguments, _ := function["arguments"].(string)

	secondArgumentsChunk := events[3].Data
	choices = sliceOfMapsFromAny(secondArgumentsChunk["choices"])
	delta = nestedMapFromAny(choices[0]["delta"])
	toolCalls = sliceOfMapsFromAny(delta["tool_calls"])
	function = nestedMapFromAny(toolCalls[0]["function"])
	secondArguments, _ := function["arguments"].(string)

	if firstArguments+secondArguments != `{"path":"C:\\Users\\Anson"}` {
		t.Fatalf("combined function.arguments = %q, want complete arguments", firstArguments+secondArguments)
	}
}

func TestStreamChatCompletionSupportsCustomToolCalls(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_chat_custom_tool",
		AccountID: "upstream_chat_custom_tool",
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

	record, _ := accountsSvc.Get("acct_chat_custom_tool")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.custom_tool_call_input.delta",
				Raw: map[string]any{
					"response_id":  "resp_chat_custom_tool",
					"item_id":      "ctc_tool",
					"output_index": 0,
					"delta":        "*** Begin Patch\\n",
				},
			},
			{
				Type: "response.output_item.added",
				Raw: map[string]any{
					"response_id":  "resp_chat_custom_tool",
					"output_index": 0,
					"item": map[string]any{
						"id":      "ctc_tool",
						"call_id": "call_patch",
						"type":    "custom_tool_call",
						"name":    "ApplyPatch",
						"input":   "*** Begin Patch\\n",
						"status":  "in_progress",
					},
				},
			},
			{
				Type: "response.custom_tool_call_input.done",
				Raw: map[string]any{
					"response_id":  "resp_chat_custom_tool",
					"item_id":      "ctc_tool",
					"output_index": 0,
					"input":        "*** Begin Patch\\n*** End Patch\\n",
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_chat_custom_tool",
						"model":  "gpt-5.4",
						"status": "completed",
					},
				},
			},
		},
	}

	app.streamChatCompletion(ctx, record, translate.NormalizedRequest{
		Endpoint: translate.EndpointChat,
		Model:    "gpt-5.4",
		Stream:   true,
	}, stream)

	events := parseSSEEvents(t, recorder.Body.String())
	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6", len(events))
	}

	toolChunk := events[1].Data
	choices := sliceOfMapsFromAny(toolChunk["choices"])
	delta := nestedMapFromAny(choices[0]["delta"])
	toolCalls := sliceOfMapsFromAny(delta["tool_calls"])
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(toolCalls))
	}
	if toolCalls[0]["id"] != "call_patch" {
		t.Fatalf("tool_calls[0].id = %#v, want call_patch", toolCalls[0]["id"])
	}
	if toolCalls[0]["type"] != "function" {
		t.Fatalf("tool_calls[0].type = %#v, want function compatibility shim", toolCalls[0]["type"])
	}
	function := nestedMapFromAny(toolCalls[0]["function"])
	if function["name"] != "ApplyPatch" {
		t.Fatalf("function.name = %#v, want ApplyPatch", function["name"])
	}
	if function["arguments"] != "" {
		t.Fatalf("function.arguments = %q, want empty initializer", function["arguments"])
	}

	inputChunk := events[2].Data
	choices = sliceOfMapsFromAny(inputChunk["choices"])
	delta = nestedMapFromAny(choices[0]["delta"])
	toolCalls = sliceOfMapsFromAny(delta["tool_calls"])
	function = nestedMapFromAny(toolCalls[0]["function"])
	firstDelta, _ := function["arguments"].(string)

	inputDoneChunk := events[3].Data
	choices = sliceOfMapsFromAny(inputDoneChunk["choices"])
	delta = nestedMapFromAny(choices[0]["delta"])
	toolCalls = sliceOfMapsFromAny(delta["tool_calls"])
	function = nestedMapFromAny(toolCalls[0]["function"])
	secondDelta, _ := function["arguments"].(string)

	if firstDelta+secondDelta != "*** Begin Patch\\n*** End Patch\\n" {
		t.Fatalf("combined function.arguments deltas = %q, want complete streamed input", firstDelta+secondDelta)
	}

	finalChunk := events[4].Data
	choices = sliceOfMapsFromAny(finalChunk["choices"])
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", choices[0]["finish_reason"])
	}
}

func TestStreamResponsesPreservesReasoningItemsAndEvents(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_responses_reasoning",
		AccountID: "upstream_responses_reasoning",
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

	record, _ := accountsSvc.Get("acct_responses_reasoning")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.reasoning_summary_text.delta",
				Raw: map[string]any{
					"response_id": "resp_responses_reasoning",
					"delta":       "Reasoning summary",
				},
			},
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_responses_reasoning",
						"model":  "gpt-5.4",
						"status": "completed",
						"output": []any{
							map[string]any{
								"type":              "reasoning",
								"id":                "rs_1",
								"status":            "completed",
								"encrypted_content": "encrypted-reasoning",
								"summary": []any{
									map[string]any{
										"type": "summary_text",
										"text": "Reasoning summary",
									},
								},
							},
							map[string]any{
								"type":   "message",
								"role":   "assistant",
								"status": "completed",
								"content": []any{
									map[string]any{
										"type": "output_text",
										"text": "Final answer",
									},
								},
							},
						},
						"output_text": "Final answer",
						"usage": map[string]any{
							"input_tokens":  8,
							"output_tokens": 3,
							"output_tokens_details": map[string]any{
								"reasoning_tokens": 1,
							},
						},
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
		"response.reasoning_summary_text.delta",
		"response.completed",
		"done",
	)
	completed := events[1].Data
	response := nestedMapFromAny(completed["response"])
	output := sliceOfMapsFromAny(response["output"])
	if len(output) != 2 {
		t.Fatalf("response.output len = %d, want 2", len(output))
	}
	if output[0]["type"] != "reasoning" {
		t.Fatalf("output[0].type = %#v, want reasoning", output[0]["type"])
	}
	if output[0]["encrypted_content"] != "encrypted-reasoning" {
		t.Fatalf("output[0].encrypted_content = %#v, want encrypted-reasoning", output[0]["encrypted_content"])
	}
	usage := nestedMapFromAny(response["usage"])
	outputDetails := nestedMapFromAny(usage["output_tokens_details"])
	if outputDetails["reasoning_tokens"] != float64(1) {
		t.Fatalf("reasoning_tokens = %#v, want 1", outputDetails["reasoning_tokens"])
	}
}

func TestStreamResponsesDoesNotSynthesizeReasoningSummaryEventFromCompletedOutput(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set(middleware.RequestIDKey, "req-test")

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_responses_reasoning_fallback",
		AccountID: "upstream_responses_reasoning_fallback",
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

	record, _ := accountsSvc.Get("acct_responses_reasoning_fallback")
	stream := &fakeEventStream{
		events: []*codex.StreamEvent{
			{
				Type: "response.completed",
				Raw: map[string]any{
					"response": map[string]any{
						"id":     "resp_responses_reasoning_fallback",
						"model":  "gpt-5.4",
						"status": "completed",
						"output": []any{
							map[string]any{
								"type":   "reasoning",
								"id":     "rs_1",
								"status": "completed",
								"summary": []any{
									map[string]any{
										"type": "summary_text",
										"text": "Recovered summary",
									},
								},
							},
							map[string]any{
								"type":   "message",
								"role":   "assistant",
								"status": "completed",
								"content": []any{
									map[string]any{
										"type": "output_text",
										"text": "Final answer",
									},
								},
							},
						},
						"output_text": "Final answer",
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
		"response.completed",
		"done",
	)
}

func TestContinuationInputHistoryIncludesReasoningReplay(t *testing.T) {
	t.Parallel()

	accumulator := translate.NewAccumulator(translate.NormalizedRequest{
		Endpoint: translate.EndpointResponses,
		Input: []codex.InputItem{{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: "hello",
			}},
		}},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.completed",
		Raw: map[string]any{
			"response": map[string]any{
				"id":     "resp_continuation_reasoning",
				"model":  "gpt-5.4",
				"status": "completed",
				"output": []any{
					map[string]any{
						"type":              "reasoning",
						"id":                "rs_1",
						"status":            "completed",
						"encrypted_content": "encrypted-reasoning",
						"summary": []any{
							map[string]any{
								"type": "summary_text",
								"text": "Reasoning summary",
							},
						},
					},
					map[string]any{
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": "assistant replay",
							},
						},
					},
				},
				"output_text": "assistant replay",
			},
		},
	})

	history := continuationInputHistory(accumulator)
	if len(history) != 3 {
		t.Fatalf("history len = %d, want 3", len(history))
	}
	if history[1].Type != "reasoning" {
		t.Fatalf("history[1].Type = %q, want reasoning", history[1].Type)
	}
	if history[1].EncryptedContent != "encrypted-reasoning" {
		t.Fatalf("history[1].EncryptedContent = %q, want encrypted-reasoning", history[1].EncryptedContent)
	}
	if len(history[1].Summary) != 1 || history[1].Summary[0].Text != "Reasoning summary" {
		t.Fatalf("history[1].Summary = %#v", history[1].Summary)
	}

	replayed := continuationInputItemsToCodex(history)
	if len(replayed) != 3 {
		t.Fatalf("replayed len = %d, want 3", len(replayed))
	}
	if replayed[1].Type != "reasoning" {
		t.Fatalf("replayed[1].Type = %q, want reasoning", replayed[1].Type)
	}
	if replayed[1].EncryptedContent != "encrypted-reasoning" {
		t.Fatalf("replayed[1].EncryptedContent = %q, want encrypted-reasoning", replayed[1].EncryptedContent)
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
		cfg:      config.Config{},
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
	}}, accounts.RotationLeastUsed)
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
