package server

import (
	"strings"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/middleware"
)

func (a *App) logUpstreamRequestFailure(c *gin.Context, endpoint, accountID string, status int, code string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}

	attrs := []any{
		"request_id", middleware.GetRequestID(c),
		"path", c.Request.URL.Path,
		"endpoint", endpoint,
		"status", status,
		"error_code", code,
		"error", logErrorText(err),
	}
	if accountID != "" {
		attrs = append(attrs, "account_id", accountID)
	}

	a.logger.Error("upstream request failed", attrs...)
}

func (a *App) logUpstreamStreamFailure(c *gin.Context, endpoint, accountID, responseID string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}

	attrs := []any{
		"request_id", middleware.GetRequestID(c),
		"path", c.Request.URL.Path,
		"endpoint", endpoint,
		"error", logErrorText(err),
	}
	if accountID != "" {
		attrs = append(attrs, "account_id", accountID)
	}
	if responseID != "" {
		attrs = append(attrs, "response_id", responseID)
	}

	a.logger.Error("upstream stream failed", attrs...)
}

func logErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
