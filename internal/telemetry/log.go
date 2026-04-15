package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxLoggerKey struct{}

func NewLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})
	return slog.New(h)
}

func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxLoggerKey{}, log)
}

func Log(ctx context.Context) *slog.Logger {
	if v := ctx.Value(ctxLoggerKey{}); v != nil {
		if log, ok := v.(*slog.Logger); ok {
			return log
		}
	}
	return slog.Default()
}
