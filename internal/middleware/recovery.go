package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.Error("panic recovered", "request_id", GetRequestID(c), "panic", recovered, "path", c.Request.URL.Path)
		AbortJSON(c, http.StatusInternalServerError, OpenAIErrorPayload("internal_server_error", "server_error", "internal_server_error", ""))
	})
}
