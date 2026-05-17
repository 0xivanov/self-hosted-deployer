package logging

import (
	"log/slog"
	"os"
	"strings"
)

func New(component string, level string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(handler).With("component", component)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func Redact(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "[REDACTED]"
	}
	return value[:4] + "...[REDACTED]..." + value[len(value)-4:]
}
