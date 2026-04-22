package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
)

type healthResponse struct {
	Status          string                    `json:"status"`
	Accounts        int                       `json:"accounts,omitempty"`
	Rotation        accounts.RotationStrategy `json:"rotation,omitempty"`
	Continuations   bool                      `json:"continuations,omitempty"`
	DefaultModel    string                    `json:"default_model,omitempty"`
	CodexBaseURL    string                    `json:"codex_base_url,omitempty"`
	RequestTimeout  string                    `json:"request_timeout,omitempty"`
	ContinuationTTL string                    `json:"continuation_ttl,omitempty"`
	Error           string                    `json:"error,omitempty"`
}

func (a *App) handleHealthLive(c *gin.Context) {
	c.JSON(http.StatusOK, healthResponse{Status: "ok"})
}

func (a *App) handleHealth(c *gin.Context) {
	records, err := a.accounts.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, healthResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, healthResponse{
		Status:          "ok",
		Accounts:        len(records),
		Rotation:        a.accounts.RotationStrategy(),
		Continuations:   true,
		DefaultModel:    a.cfg.DefaultModel,
		CodexBaseURL:    a.cfg.CodexBaseURL,
		RequestTimeout:  a.cfg.RequestTimeout.String(),
		ContinuationTTL: a.cfg.ContinuationTTL.String(),
	})
}
