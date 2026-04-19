package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func APIKey(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if required == "" {
			c.Next()
			return
		}

		token := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if token == "" {
			auth := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				token = strings.TrimSpace(auth[7:])
			}
		}

		if subtleEqual(token, required) {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "invalid_api_key",
				"type":    "authentication_error",
				"code":    "invalid_api_key",
			},
		})
	}
}

func subtleEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for i := 0; i < len(left); i++ {
		diff |= left[i] ^ right[i]
	}
	return diff == 0
}
