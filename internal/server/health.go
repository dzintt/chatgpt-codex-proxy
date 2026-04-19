package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *App) handleHealthLive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (a *App) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":           "ok",
		"accounts":         len(a.accounts.List()),
		"rotation":         a.accounts.RotationStrategy(),
		"continuations":    true,
		"default_model":    a.cfg.DefaultModel,
		"codex_base_url":   a.cfg.CodexBaseURL,
		"request_timeout":  a.cfg.RequestTimeout.String(),
		"continuation_ttl": a.cfg.ContinuationTTL.String(),
	})
}
