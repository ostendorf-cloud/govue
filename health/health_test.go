// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// get calls handler with a GET request for path and decodes the JSON body.
func get(t *testing.T, handler http.HandlerFunc, path string) (int, readyBody) {
	t.Helper()

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

	var body readyBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content type = %q, want application/json; charset=utf-8", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q, want no-store", got)
	}
	return rec.Code, body
}

func TestLiveAlwaysAnswersHealthy(t *testing.T) {
	t.Parallel()

	r := New()
	r.Add("database", time.Second, func(context.Context) error { return errors.New("down") })

	if code, body := get(t, r.Live, "/healthz"); code != http.StatusOK || body.Status != "ok" {
		t.Errorf("live = %d %+v, want 200 ok while a check fails", code, body)
	}

	r.Drain()
	if code, body := get(t, r.Live, "/healthz"); code != http.StatusOK || body.Status != "ok" {
		t.Errorf("live = %d %+v, want 200 ok while draining", code, body)
	}
}

func TestReadyWithoutChecksIsReady(t *testing.T) {
	t.Parallel()

	code, body := get(t, New().Ready, "/readyz")
	if code != http.StatusOK || body.Status != "ready" {
		t.Errorf("ready = %d %+v, want 200 ready", code, body)
	}
}

func TestReadyWhileDrainingIgnoresChecks(t *testing.T) {
	t.Parallel()

	r := New()
	r.Add("database", time.Second, func(context.Context) error { return nil })
	if r.Draining() {
		t.Error("a fresh registry reports draining")
	}
	r.Drain()

	code, body := get(t, r.Ready, "/readyz")
	if code != http.StatusServiceUnavailable || body.Status != "draining" {
		t.Errorf("ready = %d %+v, want 503 draining even with a passing check", code, body)
	}
	if !r.Draining() {
		t.Error("Draining() is false after Drain()")
	}
}

func TestReadyReportsFailuresWithoutTheirErrorText(t *testing.T) {
	t.Parallel()

	const secret = "pq: password authentication failed for user wishlist"

	r := New()
	r.Add("database", time.Second, func(context.Context) error { return errors.New(secret) })
	r.Add("object storage", time.Second, func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	r.Ready(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}

	var body readyBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	if body.Status != "unavailable" || body.Checks["database"] != "failed" {
		t.Errorf("body = %+v, want the database check reported as failed", body)
	}
	if _, ok := body.Checks["object storage"]; ok {
		t.Error("a passing check is reported in the response")
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Errorf("the check's error text leaked into the response: %s", rec.Body.String())
	}
}

func TestReadyReportsATimeoutSeparatelyFromAFailure(t *testing.T) {
	t.Parallel()

	r := New()
	r.Add("slow", 10*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	code, body := get(t, r.Ready, "/readyz")
	if code != http.StatusServiceUnavailable || body.Checks["slow"] != "timeout" {
		t.Errorf("ready = %d %+v, want the slow check reported as timeout", code, body)
	}
}

func TestReadyDoesNotWaitForACheckThatIgnoresItsTimeout(t *testing.T) {
	t.Parallel()

	const (
		timeout = 20 * time.Millisecond
		sleep   = 300 * time.Millisecond
	)

	r := New()
	r.Add("slow", timeout, func(context.Context) error {
		// A check is not required to select on ctx.Done(): nothing in the
		// Check contract forces that cooperation. This one ignores
		// cancellation entirely and reports success once it wakes up, well
		// past its own configured timeout.
		time.Sleep(sleep)
		return nil
	})

	start := time.Now()
	code, body := get(t, r.Ready, "/readyz")
	elapsed := time.Since(start)

	if elapsed >= sleep {
		t.Errorf("/readyz took %s, waited for the full %s sleep instead of the %s timeout", elapsed, sleep, timeout)
	}
	if code != http.StatusServiceUnavailable || body.Checks["slow"] == "" {
		t.Errorf("ready = %d %+v, want the overrunning check reported as not passing even though it returns nil", code, body)
	}
}

func TestReadyLogsAFailedChecksCauseWithTheRequestID(t *testing.T) {
	t.Parallel()

	const (
		name  = "database"
		cause = "pq: password authentication failed for user wishlist"
		reqID = "req-123"
	)

	var logged bytes.Buffer
	// The logger already carries the request id, the way middleware.RequestID
	// scopes it before a handler ever runs: this package logs through
	// whatever logger the context carries and never re-adds the key itself.
	logger := logging.New(&logged, slog.LevelWarn).With("request_id", reqID)
	ctx := logging.NewContext(t.Context(), logger)

	r := New()
	r.Add(name, time.Second, func(context.Context) error { return errors.New(cause) })

	rec := httptest.NewRecorder()
	r.Ready(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), cause) {
		t.Errorf("the check's error text leaked into the response: %s", rec.Body.String())
	}

	for _, want := range []string{name, cause, reqID} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log output = %s, want it to contain %q", logged.String(), want)
		}
	}
}

func TestReadyRunsChecksInParallel(t *testing.T) {
	t.Parallel()

	r := New()
	var running, peak atomic.Int32
	for _, name := range []string{"a", "b", "c"} {
		r.Add(name, time.Second, func(context.Context) error {
			now := running.Add(1)
			for {
				top := peak.Load()
				if now <= top || peak.CompareAndSwap(top, now) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
			return nil
		})
	}

	start := time.Now()
	if code, _ := get(t, r.Ready, "/readyz"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("three 20ms checks took %s, they did not run in parallel", elapsed)
	}
	if peak.Load() < 2 {
		t.Errorf("peak concurrency = %d, want at least 2", peak.Load())
	}
}

func TestAddDefaultsAZeroOrNegativeTimeout(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		timeout time.Duration
	}{
		{name: "zero", timeout: 0},
		{name: "negative", timeout: -time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := New()
			r.Add("check", tc.timeout, func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("the check got no deadline at all")
					return nil
				}
				if left := time.Until(deadline); left > DefaultTimeout {
					t.Errorf("deadline is %s away, want at most DefaultTimeout (%s)", left, DefaultTimeout)
				}
				return nil
			})

			if code, _ := get(t, r.Ready, "/readyz"); code != http.StatusOK {
				t.Errorf("status = %d, want 200", code)
			}
		})
	}
}

func TestHandleRegistersBothEndpoints(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	New().Handle(mux)

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestHandleRejectsANonGETMethod(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	New().Handle(mux)

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, rec.Code)
		}
	}
}

// TestReadyLogsRequestIDExactlyOnce drives a request through the real
// middleware.RequestID, which already scopes the context logger with
// "request_id". logFailure must log through that logger as is, never
// re-adding the same key itself.
func TestReadyLogsRequestIDExactlyOnce(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := New()
	r.Add("database", time.Second, func(context.Context) error { return errors.New("down") })

	handler := middleware.RequestID(logger)(http.HandlerFunc(r.Ready))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))

	line := bytes.TrimSpace(buf.Bytes())
	if got := jsonKeyCount(t, line, "request_id"); got != 1 {
		t.Errorf("request_id appears %d times in the log line, want exactly 1: %s", got, line)
	}
}

// TestReadyOmitsRequestIDWithoutRequestIDMiddleware simulates an application
// that put its own base logger into the context by some means other than
// middleware.RequestID. logFailure must not invent a request_id key: it
// logs through the logger it is handed, and that logger carries none.
func TestReadyOmitsRequestIDWithoutRequestIDMiddleware(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := New()
	r.Add("database", time.Second, func(context.Context) error { return errors.New("down") })

	ctx := logging.NewContext(t.Context(), logger)
	rec := httptest.NewRecorder()
	r.Ready(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))

	line := bytes.TrimSpace(buf.Bytes())
	if got := jsonKeyCount(t, line, "request_id"); got != 0 {
		t.Errorf("request_id appears %d times in the log line, want 0 (no request-id middleware in the chain): %s", got, line)
	}
}
