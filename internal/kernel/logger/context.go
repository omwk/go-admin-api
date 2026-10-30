package logger

import (
	"context"
	"log/slog"
)

type contextKey uint8

const (
	traceKey contextKey = iota
	loggerKey
)

// WithTraceID attaches a trace identifier to a request context.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey, id)
}

// TraceID returns the request trace identifier, if present.
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey).(string)
	return id
}

// With attaches a logger to a context for use by downstream operations.
func With(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, log)
}

// From returns the contextual or default logger with the request trace attached.
func From(ctx context.Context) *slog.Logger {
	log, ok := ctx.Value(loggerKey).(*slog.Logger)
	if !ok || log == nil {
		log = slog.Default()
	}
	return log.With("trace_id", TraceID(ctx))
}
