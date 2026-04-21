package codex

import (
	"net/http"
	"strconv"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
)

func ParseQuotaFromHeaders(headers http.Header) *accounts.QuotaSnapshot {
	primary := parseRateWindow(headers, "x-codex-primary")
	secondary := parseRateWindow(headers, "x-codex-secondary")
	credits := parseCredits(headers, "x-codex")
	if primary == nil && secondary == nil {
		return nil
	}
	snapshot := &accounts.QuotaSnapshot{
		PlanType:  "unknown",
		Source:    "response_headers",
		FetchedAt: time.Now().UTC(),
		RateLimit: accounts.RateLimitWindow{
			Allowed:      true,
			LimitReached: primary != nil && primary.UsedPercent != nil && *primary.UsedPercent >= 100,
		},
	}
	if primary != nil {
		snapshot.RateLimit = *primary
	}
	if secondary != nil {
		snapshot.SecondaryRateLimit = secondary
	}
	if credits != nil {
		snapshot.Credits = credits
	}
	return snapshot
}

func QuotaFromUsageResponse(payload UsageResponse) *accounts.QuotaSnapshot {
	snapshot := &accounts.QuotaSnapshot{
		PlanType:  payload.PlanType,
		Source:    "usage_endpoint",
		FetchedAt: time.Now().UTC(),
		RateLimit: accounts.RateLimitWindow{
			Allowed:      payload.RateLimit.Allowed,
			LimitReached: payload.RateLimit.LimitReached,
			UsedPercent:  floatPtr(payload.RateLimit.PrimaryWindow, func(w *UsageWindow) float64 { return w.UsedPercent }),
			ResetAt:      timePtr(payload.RateLimit.PrimaryWindow, func(w *UsageWindow) int64 { return w.ResetAt }),
			LimitWindowSeconds: intPtr(payload.RateLimit.PrimaryWindow, func(w *UsageWindow) int {
				return w.LimitWindowSeconds
			}),
		},
	}
	if payload.RateLimit.SecondaryWindow != nil {
		snapshot.SecondaryRateLimit = &accounts.RateLimitWindow{
			Allowed:      true,
			LimitReached: payload.RateLimit.SecondaryWindow.UsedPercent >= 100,
			UsedPercent:  floatPtr(payload.RateLimit.SecondaryWindow, func(w *UsageWindow) float64 { return w.UsedPercent }),
			ResetAt:      timePtr(payload.RateLimit.SecondaryWindow, func(w *UsageWindow) int64 { return w.ResetAt }),
			LimitWindowSeconds: intPtr(payload.RateLimit.SecondaryWindow, func(w *UsageWindow) int {
				return w.LimitWindowSeconds
			}),
		}
	}
	if payload.CodeReviewRateLimit != nil {
		snapshot.CodeReviewRateLimit = &accounts.RateLimitWindow{
			Allowed:      payload.CodeReviewRateLimit.Allowed,
			LimitReached: payload.CodeReviewRateLimit.LimitReached,
			UsedPercent:  floatPtr(payload.CodeReviewRateLimit.PrimaryWindow, func(w *UsageWindow) float64 { return w.UsedPercent }),
			ResetAt:      timePtr(payload.CodeReviewRateLimit.PrimaryWindow, func(w *UsageWindow) int64 { return w.ResetAt }),
			LimitWindowSeconds: intPtr(payload.CodeReviewRateLimit.PrimaryWindow, func(w *UsageWindow) int {
				return w.LimitWindowSeconds
			}),
		}
	}
	if credits := parseCreditsFromUsage(payload.Credits); credits != nil {
		snapshot.Credits = credits
	}
	return snapshot
}

func ParseQuotaFromEvent(event *StreamEvent, planType string) *accounts.QuotaSnapshot {
	if event == nil || event.Type != "codex.rate_limits" {
		return nil
	}
	rateLimits, _ := event.Raw["rate_limits"].(map[string]any)
	if rateLimits == nil {
		return nil
	}

	primary := parseEventRateWindow(rateLimits["primary"])
	secondary := parseEventRateWindow(rateLimits["secondary"])
	codeReview := parseEventRateWindow(firstMapValue(rateLimits, "code_review", "code_review_rate_limit"))
	if primary == nil && secondary == nil && codeReview == nil {
		return nil
	}

	snapshot := &accounts.QuotaSnapshot{
		PlanType:  firstNonEmpty(planType, "unknown"),
		Source:    "response_event",
		FetchedAt: time.Now().UTC(),
		RateLimit: accounts.RateLimitWindow{
			Allowed: true,
		},
	}
	if primary != nil {
		snapshot.RateLimit = *primary
	}
	if secondary != nil {
		snapshot.SecondaryRateLimit = secondary
	}
	if codeReview != nil {
		snapshot.CodeReviewRateLimit = codeReview
	}
	return snapshot
}

func parseRateWindow(headers http.Header, prefix string) *accounts.RateLimitWindow {
	pctRaw := headers.Get(prefix + "-used-percent")
	if pctRaw == "" {
		return nil
	}
	pct, err := strconv.ParseFloat(pctRaw, 64)
	if err != nil {
		return nil
	}
	window := &accounts.RateLimitWindow{
		Allowed:      true,
		LimitReached: pct >= 100,
		UsedPercent:  &pct,
	}
	if resetRaw := headers.Get(prefix + "-reset-at"); resetRaw != "" {
		if seconds, err := strconv.ParseInt(resetRaw, 10, 64); err == nil {
			ts := time.Unix(seconds, 0).UTC()
			window.ResetAt = &ts
		}
	}
	if windowRaw := headers.Get(prefix + "-window-minutes"); windowRaw != "" {
		if minutes, err := strconv.Atoi(windowRaw); err == nil {
			seconds := minutes * 60
			window.LimitWindowSeconds = &seconds
		}
	}
	return window
}

func parseEventRateWindow(value any) *accounts.RateLimitWindow {
	raw, _ := value.(map[string]any)
	if raw == nil {
		return nil
	}
	usedPercent, ok := parseFloat(raw["used_percent"])
	if !ok {
		return nil
	}
	window := &accounts.RateLimitWindow{
		Allowed:      true,
		LimitReached: usedPercent >= 100,
		UsedPercent:  &usedPercent,
	}
	if resetAt, ok := parseUnixTime(raw["reset_at"]); ok {
		window.ResetAt = &resetAt
	}
	if minutes, ok := parseInt(raw["window_minutes"]); ok {
		seconds := minutes * 60
		window.LimitWindowSeconds = &seconds
	} else if seconds, ok := parseInt(raw["limit_window_seconds"]); ok {
		window.LimitWindowSeconds = &seconds
	}
	return window
}

func firstMapValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func parseCredits(headers http.Header, prefix string) *accounts.CreditsSnapshot {
	hasAny := false
	credits := &accounts.CreditsSnapshot{}
	if value, ok := parseBoolHeader(headers.Get(prefix + "-credits-has-credits")); ok {
		credits.HasCredits = value
		hasAny = true
	}
	if value, ok := parseBoolHeader(headers.Get(prefix + "-credits-unlimited")); ok {
		credits.Unlimited = value
		hasAny = true
	}
	if value, ok := parseFloatHeader(headers.Get(prefix + "-credits-balance")); ok {
		credits.Balance = &value
		hasAny = true
	}
	if value := headers.Get(prefix + "-active-limit"); value != "" {
		credits.ActiveLimit = value
		hasAny = true
	}
	if !hasAny {
		return nil
	}
	return credits
}

func parseCreditsFromUsage(value any) *accounts.CreditsSnapshot {
	raw, _ := value.(map[string]any)
	if raw == nil {
		return nil
	}
	hasAny := false
	credits := &accounts.CreditsSnapshot{}
	if parsed, ok := parseBool(raw["has_credits"]); ok {
		credits.HasCredits = parsed
		hasAny = true
	}
	if parsed, ok := parseBool(raw["unlimited"]); ok {
		credits.Unlimited = parsed
		hasAny = true
	}
	if parsed, ok := parseFloat(raw["balance"]); ok {
		credits.Balance = &parsed
		hasAny = true
	}
	if parsed, ok := raw["active_limit"].(string); ok && parsed != "" {
		credits.ActiveLimit = parsed
		hasAny = true
	}
	if !hasAny {
		return nil
	}
	return credits
}

func parseBoolHeader(raw string) (bool, bool) {
	if raw == "" {
		return false, false
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return value, true
}

func parseFloatHeader(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func parseFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case jsonNumber:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func parseInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case jsonNumber:
		parsed, err := strconv.Atoi(string(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func parseBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(typed)
		return parsed, err == nil
	default:
		return false, false
	}
}

func parseUnixTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case int64:
		return time.Unix(typed, 0).UTC(), true
	case int:
		return time.Unix(int64(typed), 0).UTC(), true
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	case jsonNumber:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(parsed, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}

type jsonNumber string

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func floatPtr[T any](value *T, getter func(*T) float64) *float64 {
	if value == nil {
		return nil
	}
	result := getter(value)
	return &result
}

func intPtr[T any](value *T, getter func(*T) int) *int {
	if value == nil {
		return nil
	}
	result := getter(value)
	return &result
}

func timePtr[T any](value *T, getter func(*T) int64) *time.Time {
	if value == nil {
		return nil
	}
	ts := time.Unix(getter(value), 0).UTC()
	return &ts
}
