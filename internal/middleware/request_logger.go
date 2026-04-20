package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

type RequestLoggerOptions struct {
	SkipPaths map[string]struct{}
}

func RequestLogger(logger *slog.Logger, opts RequestLoggerOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if _, skip := opts.SkipPaths[path]; skip {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		attrs := []any{
			"request_id", GetRequestID(c),
			"method", c.Request.Method,
			"path", path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		}

		if size := c.Writer.Size(); size >= 0 {
			attrs = append(attrs, "bytes_written", size)
		}
		if userAgent := c.Request.UserAgent(); userAgent != "" {
			attrs = append(attrs, "user_agent", userAgent)
		}
		if query := c.Request.URL.RawQuery; query != "" {
			attrs = append(attrs, "query", query)
		}

		logger.Log(c.Request.Context(), requestLogLevel(c.Writer.Status()), "http request completed", attrs...)
	}
}

func requestLogLevel(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status == 401 || status == 403 || status == 429:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
