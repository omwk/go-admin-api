package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New constructs a JSON or text logger using the configured minimum level.
func New(out io.Writer, level string, format string) (*slog.Logger, error) {
	var minimum slog.Level
	if err := minimum.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log.level must be debug, info, warn or error")
	}
	options := &slog.HandlerOptions{Level: minimum, ReplaceAttr: timestampKey}
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(out, options)
	case "text":
		handler = slog.NewTextHandler(out, options)
	default:
		return nil, fmt.Errorf("log.format must be json or text")
	}
	return slog.New(&fieldsHandler{inner: handler}), nil
}

func timestampKey(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) == 0 && attr.Key == slog.TimeKey {
		attr.Key = "ts"
		attr.Value = slog.TimeValue(attr.Value.Time().UTC())
	}
	return attr
}

type fieldsHandler struct {
	inner slog.Handler
	keys  []string
}

func (h *fieldsHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *fieldsHandler) Handle(ctx context.Context, record slog.Record) error {
	seen := make(map[string]bool, len(h.keys)+record.NumAttrs())
	for _, key := range h.keys {
		seen[key] = true
	}
	record.Attrs(func(attr slog.Attr) bool {
		seen[attr.Key] = true
		return true
	})
	cloned := record.Clone()
	for _, attr := range []slog.Attr{
		slog.String("trace_id", TraceID(ctx)), slog.String("module", ""),
		slog.String("action", ""), slog.Int64("duration_ms", 0), slog.Int("error_code", 0),
	} {
		if !seen[attr.Key] {
			cloned.AddAttrs(attr)
		}
	}
	return h.inner.Handle(ctx, cloned)
}

func (h *fieldsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	keys := append([]string(nil), h.keys...)
	for _, attr := range attrs {
		keys = append(keys, attr.Key)
	}
	return &fieldsHandler{inner: h.inner.WithAttrs(attrs), keys: keys}
}

func (h *fieldsHandler) WithGroup(name string) slog.Handler {
	return &fieldsHandler{inner: h.inner.WithGroup(name), keys: h.keys}
}
