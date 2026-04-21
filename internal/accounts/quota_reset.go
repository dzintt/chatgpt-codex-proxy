package accounts

import "time"

func PrimaryRateLimitReset(snapshot *QuotaSnapshot, now time.Time) *time.Time {
	if snapshot == nil {
		return nil
	}
	return activeWindowResetWindow(&snapshot.RateLimit, now)
}

func QuotaReset(snapshot *QuotaSnapshot, now time.Time) *time.Time {
	if snapshot == nil {
		return nil
	}
	return firstActiveWindowReset(now, &snapshot.RateLimit, snapshot.SecondaryRateLimit)
}

func firstActiveWindowReset(now time.Time, windows ...*RateLimitWindow) *time.Time {
	for _, window := range windows {
		if reset := activeWindowResetWindow(window, now); reset != nil {
			return reset
		}
	}
	return nil
}

func activeWindowResetWindow(window *RateLimitWindow, now time.Time) *time.Time {
	if window == nil {
		return nil
	}
	if !window.Allowed {
		if window.ResetAt == nil || !window.ResetAt.After(now) {
			return nil
		}
		ts := window.ResetAt.UTC()
		return &ts
	}
	if !window.LimitReached || window.ResetAt == nil || !window.ResetAt.After(now) {
		return nil
	}
	ts := window.ResetAt.UTC()
	return &ts
}
