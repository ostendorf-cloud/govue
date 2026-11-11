package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestLevel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		input   string
		want    slog.Level
		wantErr bool
	}{
		{name: "empty defaults to info", input: "", want: slog.LevelInfo},
		{name: "lowercase info", input: "info", want: slog.LevelInfo},
		{name: "uppercase info", input: "INFO", want: slog.LevelInfo},
		{name: "debug with surrounding whitespace", input: " debug ", want: slog.LevelDebug},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "warning spells out warn", input: "warning", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
		{name: "unknown name errors", input: "trace", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Level(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Level(%q) = %v, want an error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Level(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("Level(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestNewWritesJSONAndRespectsTheLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(&buf, slog.LevelWarn)

	logger.Info("dropped")
	if buf.Len() != 0 {
		t.Fatalf("info written at level warn: %s", buf.String())
	}

	logger.Warn("kept", "count", 2)
	var line struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
		Count   int    `json:"count"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("output is not JSON: %v: %s", err, buf.String())
	}
	if line.Level != "WARN" || line.Message != "kept" || line.Count != 2 {
		t.Errorf("line = %+v", line)
	}
}

func TestContextRoundTrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)
	ctx := NewContext(WithRequestID(context.Background(), "abc123"), logger)

	if got := RequestID(ctx); got != "abc123" {
		t.Errorf("RequestID(ctx) = %q, want abc123", got)
	}

	FromContext(ctx).Info("hit")
	if !bytes.Contains(buf.Bytes(), []byte(`"msg":"hit"`)) {
		t.Errorf("logger carried by the context did not write: %s", buf.String())
	}
}

func TestEmptyContextIsSafe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if got := RequestID(ctx); got != "" {
		t.Errorf("RequestID(ctx) = %q, want empty", got)
	}

	// Must not panic and must not write anywhere.
	FromContext(ctx).Error("nowhere")
}
