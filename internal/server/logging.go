package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/middleware"
	"chatgpt-codex-proxy/internal/translate"
)

func (a *App) logUpstreamRequestFailure(c *gin.Context, endpoint, accountID string, status int, code string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}

	attrs := contextLogAttrs(c, endpoint)
	attrs = append(attrs,
		"status", status,
		"error_code", code,
		"error", logErrorText(err),
	)
	attrs = appendStringAttr(attrs, "account_id", accountID)

	a.logger.Error("upstream request failed", attrs...)
}

func (a *App) logUpstreamStreamFailure(c *gin.Context, endpoint, accountID, responseID string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}

	attrs := contextLogAttrs(c, endpoint)
	attrs = append(attrs, "error", logErrorText(err))
	attrs = appendStringAttr(attrs, "account_id", accountID)
	attrs = appendStringAttr(attrs, "response_id", responseID)

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
		attrs := contextLogAttrs(c, endpoint)
		attrs = append(attrs, "field", warning.Field, "behavior", warning.Behavior, "detail", warning.Detail)
		// a.logger.Warn("request compatibility warning", attrs...)
	}
}

func (a *App) logTupleReconversionWarning(c *gin.Context, endpoint, responseID string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}

	attrs := contextLogAttrs(c, endpoint)
	attrs = append(attrs, "error", logErrorText(err))
	attrs = appendStringAttr(attrs, "response_id", responseID)
	a.logger.Warn("tuple reconversion failed", attrs...)
}

func (a *App) logIncomingPayload(c *gin.Context, endpoint string, payload []byte) {
	if a == nil || a.logger == nil || !a.cfg.DebugLogPayloads {
		return
	}
	formatted := formatPayloadForLog(payload)
	if formatted == "" {
		return
	}

	attrs := contextLogAttrs(c, endpoint)
	attrs = append(attrs,
		"direction", "incoming",
		"payload", formatted,
	)
	a.logger.Info("payload debug", attrs...)
}

func (a *App) logUpstreamPayload(c *gin.Context, endpoint, transport, accountID string, payload any) {
	if a == nil || a.logger == nil || !a.cfg.DebugLogPayloads {
		return
	}
	formatted := formatPayloadForLog(payload)
	if formatted == "" {
		return
	}

	attrs := contextLogAttrs(c, endpoint)
	attrs = append(attrs,
		"direction", "upstream",
		"transport", transport,
		"payload", formatted,
	)
	attrs = appendStringAttr(attrs, "account_id", accountID)
	a.logger.Info("payload debug", attrs...)
}

func contextLogAttrs(c *gin.Context, endpoint string) []any {
	requestID := ""
	path := ""
	if c != nil {
		requestID = middleware.GetRequestID(c)
		if c.Request != nil && c.Request.URL != nil {
			path = c.Request.URL.Path
		}
	}
	return []any{
		"request_id", requestID,
		"path", path,
		"endpoint", endpoint,
	}
}

func appendStringAttr(attrs []any, key, value string) []any {
	if value == "" {
		return attrs
	}
	return append(attrs, key, value)
}

func formatPayloadForLog(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case []byte:
		return normalizePayloadString(typed)
	case string:
		return normalizePayloadString([]byte(typed))
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("<payload marshal error: %v>", err)
		}
		return normalizePayloadString(payload)
	}
}

func normalizePayloadString(payload []byte) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if json.Valid(trimmed) && json.Compact(&compact, trimmed) == nil {
		return compact.String()
	}
	return string(trimmed)
}
