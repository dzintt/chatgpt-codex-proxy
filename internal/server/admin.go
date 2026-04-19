package server

import (
	"net/http"
	"strings"

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
		"account_id":    record.ID,
		"status":        record.Status,
		"cached_quota":  quota,
		"local_usage":   record.LocalUsage,
		"oauth_expires": record.Token.ExpiresAt,
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
	c.JSON(http.StatusOK, a.accounts.Summary())
}
