package server

import (
	"strings"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/middleware"
	"chatgpt-codex-proxy/internal/translate"
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

func (a *App) logCompatibilityWarnings(c *gin.Context, endpoint string, warnings []translate.CompatibilityWarning) {
	if a == nil || a.logger == nil || len(warnings) == 0 {
		return
	}

	for _, warning := range warnings {
		a.logger.Warn("request compatibility warning",
			"request_id", middleware.GetRequestID(c),
			"path", c.Request.URL.Path,
			"endpoint", endpoint,
			"field", warning.Field,
			"behavior", warning.Behavior,
			"detail", warning.Detail,
		)
	}
}

func (a *App) logTupleReconversionWarning(c *gin.Context, endpoint, responseID string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}

	attrs := []any{
		"request_id", middleware.GetRequestID(c),
		"path", c.Request.URL.Path,
		"endpoint", endpoint,
		"error", logErrorText(err),
	}
	if responseID != "" {
		attrs = append(attrs, "response_id", responseID)
	}
	a.logger.Warn("tuple reconversion failed", attrs...)
}
