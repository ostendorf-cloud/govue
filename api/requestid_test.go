// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// This file is package api_test rather than api: middleware/recover.go and
// middleware/ratelimit.go import api, so a test in package api cannot import
// middleware without creating an import cycle.
package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/web-kit/api"
	"github.com/ostendorf-cloud/web-kit/logging"
	"github.com/ostendorf-cloud/web-kit/middleware"
)

// jsonKeyCount reports how many times key appears as a top-level key in
// the JSON object line, by walking the raw token stream instead of
// unmarshalling into a map: encoding/json's map decoding silently keeps
// only the last value of a duplicate key, which would hide exactly the
// bug this test exists to catch.
func jsonKeyCount(t *testing.T, line []byte, key string) int {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(line))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("reading the opening token: %v", err)
	}
	if tok != json.Delim('{') {
		t.Fatalf("log line is not a JSON object: %s", line)
	}
	count := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("reading a key: %v", err)
		}
		if k, ok := keyTok.(string); ok && k == key {
			count++
		}
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("reading the value for %v: %v", keyTok, err)
		}
	}
	return count
}

// TestHandleLogsRequestIDExactlyOnce drives a request through the real
// middleware.RequestID, which already scopes the context logger with
// "request_id". Handle must log through that logger as is, never re-adding
// the same key itself.
func TestHandleLogsRequestIDExactlyOnce(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := middleware.RequestID(logger)(api.Handle(func(http.ResponseWriter, *http.Request) error {
		return api.Internal(errors.New("disk gone"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil))

	line := bytes.TrimSpace(buf.Bytes())
	if got := jsonKeyCount(t, line, "request_id"); got != 1 {
		t.Errorf("request_id appears %d times in the log line, want exactly 1: %s", got, line)
	}
}

// TestHandleOmitsRequestIDWithoutRequestIDMiddleware simulates an application
// that put its own base logger into the context by some means other than
// middleware.RequestID. Handle must not invent a request_id key: it logs
// through the logger it is handed, and that logger carries none.
func TestHandleOmitsRequestIDWithoutRequestIDMiddleware(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := api.Handle(func(http.ResponseWriter, *http.Request) error {
		return api.Internal(errors.New("disk gone"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req = req.WithContext(logging.NewContext(req.Context(), logger))
	handler.ServeHTTP(rec, req)

	line := bytes.TrimSpace(buf.Bytes())
	if got := jsonKeyCount(t, line, "request_id"); got != 0 {
		t.Errorf("request_id appears %d times in the log line, want 0 (no request-id middleware in the chain): %s", got, line)
	}
}
