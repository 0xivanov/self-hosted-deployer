package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(component string, level string) *slog.Logger {
	return slog.New(newHandler(os.Stderr, level)).With("component", component)
}

func newHandler(w io.Writer, level string) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: parseLevel(level),
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	})
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
