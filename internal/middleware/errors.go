package middleware

import "github.com/gin-gonic/gin"

func AbortJSON(c *gin.Context, status int, payload any) {
	c.AbortWithStatusJSON(status, payload)
}

func OpenAIErrorPayload(message, typ, code, param string) gin.H {
	errorBody := gin.H{
		"message": message,
		"type":    typ,
		"code":    code,
	}
	if param != "" {
		errorBody["param"] = param
	}
	return gin.H{"error": errorBody}
}

func AdminErrorPayload(code, message string) gin.H {
	return gin.H{
		"error":   code,
		"message": message,
	}
}
