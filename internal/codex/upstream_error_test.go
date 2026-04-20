package codex

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestNewUpstreamErrorParsesRetryAfterFromJSONBody(t *testing.T) {
	t.Parallel()

	err := NewUpstreamError("codex response", http.StatusTooManyRequests, `{"error":{"message":"rate limited","resets_in_seconds":12}}`, nil)
	if err.RetryAfter != 12 {
		t.Fatalf("RetryAfter = %d, want 12", err.RetryAfter)
	}
}

func TestNewUpstreamErrorParsesRetryAfterFromHeaders(t *testing.T) {
	t.Parallel()

	headers := http.Header{"Retry-After": []string{"9"}}
	err := NewUpstreamError("codex response", http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`, headers)
	if err.RetryAfter != 9 {
		t.Fatalf("RetryAfter = %d, want 9", err.RetryAfter)
	}
}

func TestNewUpstreamErrorParsesResetTimestampFromBody(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().UTC().Add(15 * time.Second).Unix()
	err := NewUpstreamError("codex response", http.StatusTooManyRequests, `{"error":{"message":"rate limited","resets_at":`+strconv.FormatInt(resetAt, 10)+`}}`, nil)
	if err.RetryAfter <= 0 {
		t.Fatalf("RetryAfter = %d, want positive duration", err.RetryAfter)
	}
}
