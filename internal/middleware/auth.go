package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func APIKey(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if required == "" {
			AbortJSON(c, http.StatusInternalServerError, OpenAIErrorPayload("internal_server_error", "server_error", "internal_server_error", ""))
			return
		}

		token := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if token == "" {
			auth := strings.TrimSpace(c.GetHeader("Authorization"))
			if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
				token = strings.TrimSpace(auth[7:])
			}
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(required)) == 1 {
			c.Next()
			return
		}

		AbortJSON(c, http.StatusUnauthorized, OpenAIErrorPayload("invalid_api_key", "authentication_error", "invalid_api_key", ""))
	}
}
