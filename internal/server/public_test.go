package server

import (
	"bytes"
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
