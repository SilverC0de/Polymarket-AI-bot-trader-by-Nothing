// Package logging provides a redacting slog.Handler that prevents sensitive
// field values from ever reaching log output.
package logging

import (
	"context"
	"log/slog"
	"strings"
)

// sensitiveKeys is the set of lowercase substrings that, when found anywhere
// in an attribute key, cause the value to be replaced with [REDACTED].
var sensitiveKeys = []string{
	"private",
	"secret",
	"passphrase",
	"api_key",
	"apikey",
	"eoa",
	"wallet",
	"proxy",
	"address",
}

func isSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func redactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		if a.Value.Kind() == slog.KindGroup {
			out[i] = slog.Group(a.Key, redactGroupArgs(a.Value.Group())...)
		} else if isSensitive(a.Key) {
			out[i] = slog.String(a.Key, "[REDACTED]")
		} else {
			out[i] = a
		}
	}
	return out
}

func redactGroupArgs(attrs []slog.Attr) []any {
	args := make([]any, len(attrs))
	for i, a := range redactAttrs(attrs) {
		args[i] = a
	}
	return args
}

// redactingHandler wraps any slog.Handler and scrubs sensitive attributes
// before they reach the underlying handler.
type redactingHandler struct {
	inner slog.Handler
}

// NewHandler wraps h and redacts any attribute whose key contains a sensitive
// substring (case-insensitive). Use this as the sole handler so that
// credentials can never leak into logs regardless of call site.
func NewHandler(h slog.Handler) slog.Handler {
	return &redactingHandler{inner: h}
}

func (r *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return r.inner.Enabled(ctx, level)
}

func (r *redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	clean := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		if a.Value.Kind() == slog.KindGroup {
			clean.AddAttrs(slog.Group(a.Key, redactGroupArgs(a.Value.Group())...))
		} else if isSensitive(a.Key) {
			clean.AddAttrs(slog.String(a.Key, "[REDACTED]"))
		} else {
			clean.AddAttrs(a)
		}
		return true
	})
	return r.inner.Handle(ctx, clean)
}

func (r *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &redactingHandler{inner: r.inner.WithAttrs(redactAttrs(attrs))}
}

func (r *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: r.inner.WithGroup(name)}
}
