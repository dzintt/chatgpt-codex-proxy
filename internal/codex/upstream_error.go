package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type UpstreamError struct {
	Op         string
	StatusCode int
	Body       string
	Code       string
	RetryAfter int
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	op := strings.TrimSpace(e.Op)
	if op == "" {
		op = "upstream request"
	}
	if body := strings.TrimSpace(e.Body); body != "" {
		return fmt.Sprintf("%s failed (%d): %s", op, e.StatusCode, body)
	}
	if statusText := strings.TrimSpace(http.StatusText(e.StatusCode)); statusText != "" {
		return fmt.Sprintf("%s failed (%d): %s", op, e.StatusCode, statusText)
	}
	return fmt.Sprintf("%s failed (%d)", op, e.StatusCode)
}

func (e *UpstreamError) Message() string {
	if e == nil {
		return ""
	}
	if body := strings.TrimSpace(e.Body); body != "" {
		return body
	}
	if statusText := strings.TrimSpace(http.StatusText(e.StatusCode)); statusText != "" {
		return statusText
	}
	return "upstream request failed"
}

func NewUpstreamError(op string, statusCode int, body string, headers http.Header) *UpstreamError {
	err := &UpstreamError{
		Op:         strings.TrimSpace(op),
		StatusCode: statusCode,
		Body:       strings.TrimSpace(body),
	}
	err.Code, err.RetryAfter = parseUpstreamErrorMetadata(err.Body, headers)
	return err
}

func parseUpstreamErrorMetadata(body string, headers http.Header) (string, int) {
	code, retryAfter := parseUpstreamErrorBody(body)
	if headerRetry := parseRetryAfterHeaders(headers); headerRetry > retryAfter {
		retryAfter = headerRetry
	}
	return code, retryAfter
}

func parseUpstreamErrorBody(body string) (string, int) {
	if strings.TrimSpace(body) == "" {
		return "", 0
	}

	var payload upstreamErrorEnvelope
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", 0
	}

	nested := upstreamErrorFields{
		Code:            payload.Code,
		ResetsInSeconds: payload.ResetsInSeconds,
		RetryAfter:      payload.RetryAfter,
		ResetsAt:        payload.ResetsAt,
	}
	if payload.Error != nil {
		if nested.Code == "" {
			nested.Code = payload.Error.Code
		}
		if nested.ResetsInSeconds == nil {
			nested.ResetsInSeconds = payload.Error.ResetsInSeconds
		}
		if nested.RetryAfter == nil {
			nested.RetryAfter = payload.Error.RetryAfter
		}
		if nested.ResetsAt == nil {
			nested.ResetsAt = payload.Error.ResetsAt
		}
	}

	if retryAfter, ok := parseRetryAfterValue(nested.ResetsInSeconds); ok {
		return strings.TrimSpace(nested.Code), retryAfter
	}
	if retryAfter, ok := parseRetryAfterValue(nested.RetryAfter); ok {
		return strings.TrimSpace(nested.Code), retryAfter
	}
	if resetAt, ok := parseUnixTimestamp(nested.ResetsAt); ok {
		retryAfter := int(time.Until(resetAt).Seconds())
		if retryAfter > 0 {
			return strings.TrimSpace(nested.Code), retryAfter
		}
	}
	return strings.TrimSpace(nested.Code), 0
}

func parseRetryAfterHeaders(headers http.Header) int {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return seconds
	}
	if when, err := http.ParseTime(raw); err == nil {
		seconds := int(time.Until(when).Seconds())
		if seconds > 0 {
			return seconds
		}
	}
	return 0
}

type upstreamErrorEnvelope struct {
	Error           *upstreamErrorFields `json:"error"`
	Code            string               `json:"code"`
	ResetsInSeconds json.RawMessage      `json:"resets_in_seconds"`
	RetryAfter      json.RawMessage      `json:"retry_after"`
	ResetsAt        json.RawMessage      `json:"resets_at"`
}

type upstreamErrorFields struct {
	Code            string          `json:"code"`
	ResetsInSeconds json.RawMessage `json:"resets_in_seconds"`
	RetryAfter      json.RawMessage `json:"retry_after"`
	ResetsAt        json.RawMessage `json:"resets_at"`
}

func parseRetryAfterValue(raw json.RawMessage) (int, bool) {
	if parsed, ok := parsePositiveInt64(raw); ok && parsed > 0 {
		return int(parsed), true
	}
	return 0, false
}

func parseUnixTimestamp(raw json.RawMessage) (time.Time, bool) {
	if parsed, ok := parsePositiveInt64(raw); ok && parsed > 0 {
		return time.Unix(parsed, 0).UTC(), true
	}
	return time.Time{}, false
}

func parsePositiveInt64(raw json.RawMessage) (int64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false
	}

	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err == nil {
		if value, err := number.Int64(); err == nil && value > 0 {
			return value, true
		}
		if value, err := number.Float64(); err == nil && value > 0 {
			return int64(value), true
		}
		return 0, false
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return 0, false
		}
		if value, err := strconv.ParseInt(text, 10, 64); err == nil && value > 0 {
			return value, true
		}
		if value, err := strconv.ParseFloat(text, 64); err == nil && value > 0 {
			return int64(value), true
		}
	}

	return 0, false
}
