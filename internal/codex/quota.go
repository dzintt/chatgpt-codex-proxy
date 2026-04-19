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
