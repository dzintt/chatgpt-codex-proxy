package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/openai"
)

func TestHandleModelsIncludesCreatedTimestamp(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	app := &App{cfg: config.Config{DefaultModel: openai.CanonicalDefaultModel}}
	app.handleModels(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(body.Data) == 0 {
		t.Fatal("expected model list")
	}
	if body.Data[0]["created"] != float64(openai.ModelCreatedTimestamp) {
		t.Fatalf("created = %#v, want %d", body.Data[0]["created"], openai.ModelCreatedTimestamp)
	}
}

func TestHandleModelByIDResolvesAlias(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/codex", nil)
	ctx.Params = gin.Params{{Key: "model_id", Value: "codex"}}

	app := &App{cfg: config.Config{DefaultModel: openai.CanonicalDefaultModel}}
	app.handleModelByID(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body["id"] != "codex" {
		t.Fatalf("id = %#v, want codex", body["id"])
	}
}

func TestHandleModelByIDReturnsNotFound(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/unknown", nil)
	ctx.Params = gin.Params{{Key: "model_id", Value: "unknown"}}

	app := &App{cfg: config.Config{DefaultModel: openai.CanonicalDefaultModel}}
	app.handleModelByID(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body["error"]["code"] != "model_not_found" {
		t.Fatalf("error.code = %#v, want model_not_found", body["error"]["code"])
	}
}
