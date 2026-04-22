package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
)

type adminAccountsResponse struct {
	Accounts []adminAccountResponse `json:"accounts"`
}

type adminAccountResponse struct {
	ID            string                  `json:"id"`
	UpstreamID    string                  `json:"upstream_account_id"`
	UserID        string                  `json:"user_id,omitempty"`
	Email         string                  `json:"email,omitempty"`
	PlanType      string                  `json:"plan_type,omitempty"`
	Label         string                  `json:"label,omitempty"`
	Status        accounts.Status         `json:"status"`
	EligibleNow   bool                    `json:"eligible_now"`
	CooldownUntil *time.Time              `json:"cooldown_until,omitempty"`
	LastError     string                  `json:"last_error,omitempty"`
	CachedQuota   *accounts.QuotaSnapshot `json:"cached_quota,omitempty"`
	OauthExpires  time.Time               `json:"oauth_expires"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

type adminAccountUsageResponse struct {
	AccountID      string                  `json:"account_id"`
	UpstreamID     string                  `json:"upstream_account_id"`
	UserID         string                  `json:"user_id,omitempty"`
	Status         accounts.Status         `json:"status"`
	EligibleNow    bool                    `json:"eligible_now"`
	CooldownUntil  *time.Time              `json:"cooldown_until,omitempty"`
	LastError      string                  `json:"last_error,omitempty"`
	CachedQuota    *accounts.QuotaSnapshot `json:"cached_quota,omitempty"`
	QuotaRuntime   *accounts.QuotaSnapshot `json:"quota_runtime,omitempty"`
	QuotaSource    string                  `json:"quota_source,omitempty"`
	QuotaFetchedAt *time.Time              `json:"quota_fetched_at,omitempty"`
	OauthExpires   time.Time               `json:"oauth_expires"`
}

type adminAccountRefreshResponse struct {
	Account accounts.Record `json:"account"`
}

type rotationResponse struct {
	Strategy accounts.RotationStrategy `json:"strategy"`
}

func (a *App) handleAdminAccounts(c *gin.Context) {
	records, err := a.accounts.List()
	if err != nil {
		a.writeAdminError(c, http.StatusInternalServerError, "accounts_list_failed", err.Error())
		return
	}
	items := make([]adminAccountResponse, 0, len(records))
	for _, record := range records {
		eligibleNow, err := a.accounts.EligibleNow(record.ID)
		if err != nil {
			a.writeAdminError(c, http.StatusInternalServerError, "accounts_list_failed", err.Error())
			return
		}
		items = append(items, adminAccountResponse{
			ID:            record.ID,
			UpstreamID:    record.AccountID,
			UserID:        record.UserID,
			Email:         record.Email,
			PlanType:      record.PlanType,
			Label:         record.Label,
			Status:        record.Status,
			EligibleNow:   eligibleNow,
			CooldownUntil: record.CooldownUntil,
			LastError:     record.LastError,
			CachedQuota:   record.CachedQuota,
			OauthExpires:  record.Token.ExpiresAt,
			CreatedAt:     record.CreatedAt,
			UpdatedAt:     record.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, adminAccountsResponse{Accounts: items})
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
	eligibleNow, err := a.accounts.EligibleNow(record.ID)
	if err != nil {
		a.writeAdminError(c, http.StatusInternalServerError, "usage_lookup_failed", err.Error())
		return
	}
	effectiveQuota := firstQuota(quota, record.CachedQuota)
	c.JSON(http.StatusOK, adminAccountUsageResponse{
		AccountID:      record.ID,
		UpstreamID:     record.AccountID,
		UserID:         record.UserID,
		Status:         record.Status,
		EligibleNow:    eligibleNow,
		CooldownUntil:  record.CooldownUntil,
		LastError:      record.LastError,
		CachedQuota:    record.CachedQuota,
		QuotaRuntime:   quota,
		QuotaSource:    quotaSource(effectiveQuota),
		QuotaFetchedAt: quotaFetchedAt(effectiveQuota),
		OauthExpires:   record.Token.ExpiresAt,
	})
}

func (a *App) handleAdminAccountRefresh(c *gin.Context) {
	record, err := a.accountMgr.Refresh(c.Request.Context(), c.Param("account_id"))
	if err != nil {
		a.writeAdminError(c, http.StatusBadGateway, "refresh_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, adminAccountRefreshResponse{Account: record})
}

func (a *App) handleAdminRotationGet(c *gin.Context) {
	c.JSON(http.StatusOK, rotationResponse{Strategy: a.accounts.RotationStrategy()})
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
	c.JSON(http.StatusOK, rotationResponse{Strategy: strategy})
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

func firstQuota(runtime, cached *accounts.QuotaSnapshot) *accounts.QuotaSnapshot {
	if runtime != nil {
		return runtime
	}
	return cached
}
