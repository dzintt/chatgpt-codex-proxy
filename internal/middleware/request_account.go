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
	if c == nil {
		return ""
	}
	value, ok := c.Get(RequestAccountIDKey)
	if !ok {
		return ""
	}
	accountID, _ := value.(string)
	return accountID
}

func GetRequestUpstreamAccountID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(RequestUpstreamAccountIDKey)
	if !ok {
		return ""
	}
	accountID, _ := value.(string)
	return accountID
}
