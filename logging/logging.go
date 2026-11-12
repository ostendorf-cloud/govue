// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package logging builds the structured logger this library's packages and
// its consumers log through, and carries a request scoped logger plus its
// request id through a context.
//
// Every other package in this module takes a logger instead of building one,
// per this repository's own rule. This package is the one place that rule
// does not apply to: it exists to construct the logger in the first place, so
// an application builds it once and injects it everywhere else.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Level parses a level name, typically read from an environment variable,
// case insensitively and with surrounding whitespace trimmed.
func Level(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logging: unknown log level %q, want debug, info, warn or error", name)
	}
}

// New builds the JSON logger an application constructs once and injects
// everywhere else. Where w writes to is the caller's decision; this package
// never picks a destination on its own.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

type ctxKey int

const (
	loggerKey ctxKey = iota
	requestIDKey
)

// NewContext carries a logger, usually one already holding the request id,
// through ctx.
func NewContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext returns the logger carried by ctx. Without one it returns a
// logger that discards everything, so code called outside its usual request
// scope never panics and never writes to an unexpected place.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}

// WithRequestID carries the id of the request being handled through ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the id of the request being handled, or an empty string
// when ctx carries none. A client facing error payload can hand this back so
// a report ties to the log line that holds the detail.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
