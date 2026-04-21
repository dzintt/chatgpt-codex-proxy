package middleware

import "github.com/gin-gonic/gin"

const (
	RequestAccountIDKey         = "request_account_id"
	RequestUpstreamAccountIDKey = "request_upstream_account_id"
)

func SetRequestAccount(c *gin.Context, accountID, upstreamAccountID string) {
	if c == nil {
		return
	}
	if accountID != "" {
		c.Set(RequestAccountIDKey, accountID)
	}
	if upstreamAccountID != "" {
		c.Set(RequestUpstreamAccountIDKey, upstreamAccountID)
	}
}

func GetRequestAccountID(c *gin.Context) string {
	return getContextString(c, RequestAccountIDKey)
}

func GetRequestUpstreamAccountID(c *gin.Context) string {
	return getContextString(c, RequestUpstreamAccountIDKey)
}

func getContextString(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(key)
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}
