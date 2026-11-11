package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
