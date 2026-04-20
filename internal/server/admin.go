package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
)

func (a *App) handleAdminAccounts(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"accounts": a.accounts.List()})
}

func (a *App) handleAdminDeviceLoginStart(c *gin.Context) {
	record, err := a.deviceLogins.Start(c.Request.Context())
	if err != nil {
		a.writeAdminError(c, http.StatusBadGateway, "device_login_start_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, record)
}

func (a *App) handleAdminDeviceLoginGet(c *gin.Context) {
	record, ok := a.deviceLogins.Get(c.Param("login_id"))
	if !ok {
		a.writeAdminError(c, http.StatusNotFound, "login_not_found", "device login not found")
		return
	}
	c.JSON(http.StatusOK, record)
}

func (a *App) handleAdminAccountDelete(c *gin.Context) {
	if err := a.accounts.Remove(c.Param("account_id")); err != nil {
		a.writeAdminError(c, http.StatusNotFound, "account_not_found", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) handleAdminAccountPatch(c *gin.Context) {
	var body struct {
		Label  *string `json:"label"`
		Status *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		a.writeAdminError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	var statusPtr *accounts.Status
	if body.Status != nil {
		status := accounts.Status(strings.TrimSpace(*body.Status))
		switch status {
		case accounts.StatusActive, accounts.StatusDisabled:
			statusPtr = &status
		default:
			a.writeAdminError(c, http.StatusBadRequest, "invalid_status", "status must be active or disabled")
			return
		}
	}

	record, err := a.accounts.Patch(c.Param("account_id"), body.Label, statusPtr)
	if err != nil {
		a.writeAdminError(c, http.StatusNotFound, "account_not_found", err.Error())
		return
	}
	c.JSON(http.StatusOK, record)
}

func (a *App) handleAdminAccountUsage(c *gin.Context) {
	record, quota, err := a.accountMgr.GetUsage(c.Request.Context(), c.Param("account_id"), c.Query("cached") == "true")
	if err != nil {
		a.writeAdminError(c, http.StatusBadGateway, "usage_lookup_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"account_id":          record.ID,
		"upstream_account_id": record.AccountID,
		"user_id":             record.UserID,
		"status":              record.Status,
		"block_state":         record.BlockState,
		"cached_quota":        record.CachedQuota,
		"quota_runtime":       quota,
		"quota_source":        quotaSource(quota),
		"quota_fetched_at":    quotaFetchedAt(quota),
		"local_usage":         record.LocalUsage,
		"last_error":          record.LastError,
		"oauth_expires":       record.Token.ExpiresAt,
	})
}

func (a *App) handleAdminAccountRefresh(c *gin.Context) {
	record, err := a.accountMgr.Refresh(c.Request.Context(), c.Param("account_id"))
	if err != nil {
		a.writeAdminError(c, http.StatusBadGateway, "refresh_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"account": record})
}

func (a *App) handleAdminRotationGet(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"strategy": a.accounts.RotationStrategy()})
}

func (a *App) handleAdminRotationPut(c *gin.Context) {
	var body struct {
		Strategy string `json:"strategy"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		a.writeAdminError(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	strategy := accounts.RotationStrategy(strings.TrimSpace(body.Strategy))
	switch strategy {
	case accounts.RotationLeastUsed, accounts.RotationRoundRobin, accounts.RotationSticky:
	default:
		a.writeAdminError(c, http.StatusBadRequest, "invalid_strategy", "strategy must be least_used, round_robin, or sticky")
		return
	}
	if err := a.accounts.SetRotationStrategy(strategy); err != nil {
		a.writeAdminError(c, http.StatusInternalServerError, "rotation_update_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"strategy": strategy})
}

func (a *App) handleAdminUsageSummary(c *gin.Context) {
	live := a.accounts.Summary()
	if a.usageHistory == nil {
		c.JSON(http.StatusOK, live)
		return
	}

	summary, err := a.usageHistory.CumulativeSummary(live)
	if err != nil {
		a.logger.Warn("compute cumulative usage summary failed", "error", err.Error())
	}
	c.JSON(http.StatusOK, summary)
}

func (a *App) handleAdminUsageHistory(c *gin.Context) {
	if a.usageHistory == nil {
		a.writeAdminError(c, http.StatusServiceUnavailable, "usage_history_unavailable", "usage history store is not configured")
		return
	}

	granularity := strings.TrimSpace(c.DefaultQuery("granularity", "hourly"))
	switch granularity {
	case "raw", "hourly", "daily":
	default:
		a.writeAdminError(c, http.StatusBadRequest, "invalid_granularity", "granularity must be raw, hourly, or daily")
		return
	}

	hours := 24
	if raw := strings.TrimSpace(c.Query("hours")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			a.writeAdminError(c, http.StatusBadRequest, "invalid_hours", "hours must be a positive integer")
			return
		}
		if parsed > 24*30 {
			parsed = 24 * 30
		}
		hours = parsed
	}

	c.JSON(http.StatusOK, gin.H{
		"granularity": granularity,
		"hours":       hours,
		"points":      a.usageHistory.History(hours, granularity, time.Now().UTC()),
	})
}

func quotaSource(snapshot *accounts.QuotaSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return snapshot.Source
}

func quotaFetchedAt(snapshot *accounts.QuotaSnapshot) *time.Time {
	if snapshot == nil || snapshot.FetchedAt.IsZero() {
		return nil
	}
	ts := snapshot.FetchedAt.UTC()
	return &ts
}
