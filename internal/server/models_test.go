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

func TestHandleModelByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modelID    string
		wantStatus int
		assertBody func(*testing.T, []byte)
	}{
		{
			name:       "resolves alias",
			modelID:    "codex",
			wantStatus: http.StatusOK,
			assertBody: func(t *testing.T, bodyBytes []byte) {
				t.Helper()

				var body map[string]any
				if err := json.Unmarshal(bodyBytes, &body); err != nil {
					t.Fatalf("json.Unmarshal() error = %v", err)
				}
				if body["id"] != "codex" {
					t.Fatalf("id = %#v, want codex", body["id"])
				}
				if body["created"] != float64(openai.ModelCreatedTimestamp) {
					t.Fatalf("created = %#v, want %d", body["created"], openai.ModelCreatedTimestamp)
				}
			},
		},
		{
			name:       "returns not found",
			modelID:    "unknown",
			wantStatus: http.StatusNotFound,
			assertBody: func(t *testing.T, bodyBytes []byte) {
				t.Helper()

				var body map[string]map[string]any
				if err := json.Unmarshal(bodyBytes, &body); err != nil {
					t.Fatalf("json.Unmarshal() error = %v", err)
				}
				if body["error"]["code"] != "model_not_found" {
					t.Fatalf("error.code = %#v, want model_not_found", body["error"]["code"])
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+tc.modelID, nil)
			ctx.Params = gin.Params{{Key: "model_id", Value: tc.modelID}}

			app := &App{cfg: config.Config{DefaultModel: openai.CanonicalDefaultModel}}
			app.handleModelByID(ctx)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			tc.assertBody(t, recorder.Body.Bytes())
		})
	}
}
