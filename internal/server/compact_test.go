package server

import (
	"bytes"
	"context"
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
	"chatgpt-codex-proxy/internal/models"
	"chatgpt-codex-proxy/internal/translate"
)

func TestResponsesCompactRouteRequiresAuth(t *testing.T) {
	t.Parallel()

	app := newCompactTestApp(t, nil)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestHandleResponsesCompactRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	app := newCompactTestApp(t, nil)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	app.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	assertOpenAIErrorCode(t, recorder.Body.Bytes(), "invalid_request_error")
}

func TestHandleResponsesCompactRejectsUnsupportedModel(t *testing.T) {
	t.Parallel()

	app := newCompactTestApp(t, nil)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"model":"not-a-real-model","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	app.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	assertOpenAIErrorCode(t, recorder.Body.Bytes(), "model_not_found")
}

func TestHandleResponsesCompactWrapsBareOutput(t *testing.T) {
	t.Parallel()

	var received codex.CompactRequest
	app := newCompactTestApp(t, func(ctx context.Context, record accounts.Record, req codex.CompactRequest) (codex.CompactResponse, *accounts.QuotaSnapshot, error) {
		received = req
		return codex.CompactResponse{
			Output: []map[string]any{{
				"type":              "compaction",
				"id":                "cmp_1",
				"encrypted_content": "enc_1",
			}},
		}, nil, nil
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	app.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	if received.Model == "" {
		t.Fatal("received.Model = empty, want resolved default model")
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := body["object"]; got != "response.compaction" {
		t.Fatalf("object = %#v, want response.compaction", got)
	}
	output, _ := body["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("len(output) = %d, want 1", len(output))
	}
}

func TestHandleResponsesCompactPreservesUsageAndExpandsPreviousResponse(t *testing.T) {
	t.Parallel()

	var received codex.CompactRequest
	app := newCompactTestApp(t, func(ctx context.Context, record accounts.Record, req codex.CompactRequest) (codex.CompactResponse, *accounts.QuotaSnapshot, error) {
		received = req
		return codex.CompactResponse{
			ID:        "cmp_resp_1",
			Object:    "response.compaction",
			CreatedAt: 123,
			Usage: map[string]any{
				"input_tokens":  10,
				"output_tokens": 4,
			},
			Output: []map[string]any{{
				"type":              "compaction",
				"id":                "cmp_2",
				"encrypted_content": "enc_2",
			}},
		}, nil, nil
	})

	app.continuations.Put(accounts.ContinuationRecord{
		ResponseID: "resp_prev_compact",
		AccountID:  "acct_compact",
		Model:      "gpt-5.4",
		InputHistory: []accounts.ContinuationInputItem{{
			Role:  "assistant",
			Type:  "message",
			Phase: "output",
			Content: []accounts.ContinuationContentPart{{
				Type: "output_text",
				Text: "Earlier compacted output",
			}},
		}},
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{
		"previous_response_id":"resp_prev_compact",
		"input":{"role":"user","content":"next turn"}
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	app.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if received.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", received.Model)
	}
	if len(received.Input) != 2 {
		t.Fatalf("len(input) = %d, want 2", len(received.Input))
	}
	if received.Input[0].Role != "assistant" || received.Input[0].Phase != "output" {
		t.Fatalf("input[0] = %#v, want expanded assistant output-phase history", received.Input[0])
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := body["id"]; got != "cmp_resp_1" {
		t.Fatalf("id = %#v, want cmp_resp_1", got)
	}
	usage, _ := body["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) {
		t.Fatalf("usage[input_tokens] = %#v, want 10", usage["input_tokens"])
	}
}

func TestResolveCompactRequestRejectsUnknownPreviousResponseID(t *testing.T) {
	t.Parallel()

	app := &App{
		continuations: accounts.NewContinuationManager(time.Minute),
	}

	_, _, err := app.resolveCompactRequest(translate.NormalizedCompactRequest{
		PreviousResponseID: "resp_missing",
	})
	if err == nil {
		t.Fatal("resolveCompactRequest() error = nil, want invalid previous response id")
	}
}

func newCompactTestApp(t *testing.T, caller func(context.Context, accounts.Record, codex.CompactRequest) (codex.CompactResponse, *accounts.QuotaSnapshot, error)) *App {
	t.Helper()

	gin.SetMode(gin.TestMode)

	now := time.Now().UTC()
	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_compact",
		AccountID: "upstream_compact",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   now.Add(time.Hour),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	cfg := config.Config{
		ProxyAPIKey:     "test-key",
		CodexBaseURL:    "https://example.invalid",
		DefaultModel:    "gpt-5.4",
		ContinuationTTL: time.Minute,
		RequestTimeout:  5 * time.Second,
		OpenAIBeta:      "assistants=v2",
	}
	httpClient := codex.NewHTTPClient(cfg)
	t.Cleanup(func() { _ = httpClient.Close() })

	modelCatalog := models.NewCatalog(models.BootstrapEntries())
	app := &App{
		cfg:           cfg,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		engine:        gin.New(),
		accounts:      accountsSvc,
		httpClient:    httpClient,
		compactCaller: caller,
		continuations: accounts.NewContinuationManager(time.Minute),
		models:        modelCatalog,
	}
	app.accountMgr = codex.NewAccountManager(cfg, accountsSvc, nil, httpClient, modelCatalog)
	app.routes()
	return app
}

func assertOpenAIErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Error.Code != want {
		t.Fatalf("error.code = %q, want %q", payload.Error.Code, want)
	}
}
