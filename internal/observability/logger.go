package observability

import (
	"log/slog"
	"os"
)

func NewLogger(levelText string) *slog.Logger {
	level := new(slog.LevelVar)
	switch levelText {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}
