package middleware

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const RequestIDKey = "request_id"

var requestSequence uint64

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-Id")
		if requestID == "" {
			requestID = nextRequestID()
		}
		c.Set(RequestIDKey, requestID)
		c.Writer.Header().Set("X-Request-Id", requestID)
		c.Next()
	}
}

func GetRequestID(c *gin.Context) string {
	value, ok := c.Get(RequestIDKey)
	if !ok {
		return ""
	}
	requestID, _ := value.(string)
	return requestID
}

func nextRequestID() string {
	return fmt.Sprintf("req_%d_%08x", time.Now().UTC().UnixNano(), atomic.AddUint64(&requestSequence, 1))
}
