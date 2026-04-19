package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestLoggerLogsSuccessfulRequest(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	engine.ServeHTTP(recorder, request)

	entries := decodeLogEntries(t, logs)
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry["msg"] != "http request completed" {
		t.Fatalf("msg = %v, want http request completed", entry["msg"])
	}
	if entry["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", entry["level"])
	}
	if entry["method"] != http.MethodGet {
		t.Fatalf("method = %v, want GET", entry["method"])
	}
	if entry["path"] != "/v1/models" {
		t.Fatalf("path = %v, want /v1/models", entry["path"])
	}
	if entry["route"] != "/v1/models" {
		t.Fatalf("route = %v, want /v1/models", entry["route"])
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want %d", entry["status"], http.StatusOK)
	}
	if _, ok := entry["latency_ms"]; !ok {
		t.Fatal("latency_ms missing from log entry")
	}
	if _, ok := entry["request_id"]; !ok {
		t.Fatal("request_id missing from log entry")
	}
}

func TestRequestLoggerSkipsHealthLive(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	engine.ServeHTTP(recorder, request)

	entries := decodeLogEntries(t, logs)
	if len(entries) != 0 {
		t.Fatalf("log entries = %d, want 0", len(entries))
	}
}

func TestRequestLoggerLogsHealth(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	engine.ServeHTTP(recorder, request)

	entries := decodeLogEntries(t, logs)
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if entries[0]["path"] != "/health" {
		t.Fatalf("path = %v, want /health", entries[0]["path"])
	}
}

func TestRequestLoggerPreservesRequestID(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.GET("/v1/models", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("X-Request-Id", "test-123")
	engine.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Request-Id"); got != "test-123" {
		t.Fatalf("X-Request-Id header = %q, want test-123", got)
	}

	entries := decodeLogEntries(t, logs)
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if entries[0]["request_id"] != "test-123" {
		t.Fatalf("request_id = %v, want test-123", entries[0]["request_id"])
	}
}

func TestRequestLoggerWarnsOnUnauthorized(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.Use(APIKey("secret"))
	engine.GET("/private", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	engine.ServeHTTP(recorder, request)

	entries := decodeLogEntries(t, logs)
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if entries[0]["status"] != float64(http.StatusUnauthorized) {
		t.Fatalf("status = %v, want %d", entries[0]["status"], http.StatusUnauthorized)
	}
	if entries[0]["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", entries[0]["level"])
	}
}

func TestRequestLoggerLogsRecoveredPanic(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.Use(Recovery(newTestLogger(logs)))
	engine.GET("/panic", func(c *gin.Context) {
		panic("boom")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	engine.ServeHTTP(recorder, request)

	entries := decodeLogEntries(t, logs)
	if len(entries) != 2 {
		t.Fatalf("log entries = %d, want 2", len(entries))
	}
	if entries[0]["msg"] != "panic recovered" {
		t.Fatalf("first msg = %v, want panic recovered", entries[0]["msg"])
	}
	if entries[0]["level"] != "ERROR" {
		t.Fatalf("first level = %v, want ERROR", entries[0]["level"])
	}
	if entries[1]["msg"] != "http request completed" {
		t.Fatalf("second msg = %v, want http request completed", entries[1]["msg"])
	}
	if entries[1]["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("second status = %v, want %d", entries[1]["status"], http.StatusInternalServerError)
	}
	if entries[1]["level"] != "ERROR" {
		t.Fatalf("second level = %v, want ERROR", entries[1]["level"])
	}
}

func TestRequestLoggerDoesNotLogBodyContents(t *testing.T) {
	t.Parallel()

	engine, logs := newLoggedEngine(t)
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusAccepted)
	})

	recorder := httptest.NewRecorder()
	body := `{"secret":"do-not-log-me"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	if strings.Contains(logs.String(), "do-not-log-me") {
		t.Fatal("request body appeared in logs")
	}
}

func newLoggedEngine(t *testing.T) (*gin.Engine, *bytes.Buffer) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	logs := new(bytes.Buffer)
	engine := gin.New()
	engine.SetTrustedProxies(nil)
	engine.Use(RequestID())
	engine.Use(RequestLogger(newTestLogger(logs), RequestLoggerOptions{
		SkipPaths: map[string]struct{}{
			"/health/live": {},
		},
	}))
	return engine, logs
}

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func decodeLogEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return nil
	}

	lines := strings.Split(raw, "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal log entry: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}
