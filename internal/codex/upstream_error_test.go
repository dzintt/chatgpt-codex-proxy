package codex

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestNewUpstreamErrorParsesRetryAfter(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().UTC().Add(15 * time.Second).Unix()
	tests := []struct {
		name      string
		body      string
		headers   http.Header
		wantExact int
		wantMin   int
	}{
		{
			name:      "from json resets_in_seconds",
			body:      `{"error":{"message":"rate limited","resets_in_seconds":12}}`,
			wantExact: 12,
		},
		{
			name:      "from retry-after header",
			body:      `{"error":{"message":"rate limited"}}`,
			headers:   http.Header{"Retry-After": []string{"9"}},
			wantExact: 9,
		},
		{
			name:    "from json resets_at timestamp",
			body:    `{"error":{"message":"rate limited","resets_at":` + strconv.FormatInt(resetAt, 10) + `}}`,
			wantMin: 1,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := NewUpstreamError("codex response", http.StatusTooManyRequests, tc.body, tc.headers)
			if tc.wantExact > 0 && err.RetryAfter != tc.wantExact {
				t.Fatalf("RetryAfter = %d, want %d", err.RetryAfter, tc.wantExact)
			}
			if tc.wantMin > 0 && err.RetryAfter < tc.wantMin {
				t.Fatalf("RetryAfter = %d, want >= %d", err.RetryAfter, tc.wantMin)
			}
		})
	}
}
