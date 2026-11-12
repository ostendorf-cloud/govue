// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// serve runs one request through the middleware wrapping a mux, exactly how
// an application wires it: the middleware outside, so r.Pattern is already
// set by the time it reads it.
func serve(t *testing.T, h *HTTP, method, target string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /lists/{id}", handler)
	mux.HandleFunc("POST /lists", handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	h.Middleware(mux).ServeHTTP(rec, req)
	return rec
}

// scrape runs one request through Handler and returns the body, failing the
// test if the scrape itself did not succeed.
func scrape(t *testing.T, gatherer prometheus.Gatherer) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil)
	Handler(gatherer).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestMiddleware_LabelsByRoutePatternAndStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		method     string
		target     string
		handler    http.HandlerFunc
		wantRoute  string
		wantStatus string
	}{
		{
			name:       "a matched route with an id in the path",
			method:     "GET",
			target:     "/lists/1",
			handler:    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			wantRoute:  "GET /lists/{id}",
			wantStatus: "200",
		},
		{
			name:       "a different method and route",
			method:     "POST",
			target:     "/lists",
			handler:    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) },
			wantRoute:  "POST /lists",
			wantStatus: "201",
		},
		{
			name:       "an unmatched path gets the bounded label, not the raw path",
			method:     "GET",
			target:     "/nope",
			handler:    func(http.ResponseWriter, *http.Request) {},
			wantRoute:  unmatchedRoute,
			wantStatus: "404",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry := prometheus.NewRegistry()
			h := NewHTTP(registry)
			rec := serve(t, h, tc.method, tc.target, tc.handler)

			wantCode, err := strconv.Atoi(tc.wantStatus)
			if err != nil {
				t.Fatalf("test case status %q is not a number", tc.wantStatus)
			}
			if rec.Code != wantCode {
				t.Errorf("status = %d, want %d", rec.Code, wantCode)
			}
			if got := testutil.ToFloat64(h.requests.WithLabelValues(tc.method, tc.wantRoute, tc.wantStatus)); got != 1 {
				t.Errorf("requests{method=%s,route=%s,status=%s} = %v, want 1", tc.method, tc.wantRoute, tc.wantStatus, got)
			}
		})
	}
}

func TestMiddleware_CollapsesDifferentIdsIntoOneSeries(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	h := NewHTTP(registry)

	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	serve(t, h, "GET", "/lists/1", ok)
	serve(t, h, "GET", "/lists/2", ok)

	// Two different ids, one series: the pattern is the label, not the path.
	if got := testutil.ToFloat64(h.requests.WithLabelValues("GET", "GET /lists/{id}", "200")); got != 2 {
		t.Errorf("GET /lists/{id} 200 = %v, want 2", got)
	}
	if count := testutil.CollectAndCount(h.requests); count != 1 {
		t.Errorf("%d series, want 1", count)
	}

	body := scrape(t, registry)
	if !strings.Contains(body, `http_requests_total{method="GET",route="GET /lists/{id}",status="200"} 2`) {
		t.Errorf("scrape output is missing the counter:\n%s", body)
	}
	if strings.Contains(body, "/lists/1") || strings.Contains(body, "/lists/2") {
		t.Errorf("a raw path reached a label:\n%s", body)
	}
}

func TestMiddleware_TimesTheRequest(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	h := NewHTTP(registry)
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	h.now = func() time.Time {
		now = now.Add(250 * time.Millisecond)
		return now
	}

	serve(t, h, "GET", "/lists/1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	body := scrape(t, registry)
	// 0.25s lands in the 0.25 bucket and in every bucket above it.
	if !strings.Contains(body, `http_request_duration_seconds_bucket{method="GET",route="GET /lists/{id}",status="200",le="0.25"} 1`) {
		t.Errorf("the duration was not observed:\n%s", body)
	}
}

func TestMiddleware_CountsAPanicAsAServerError(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	h := NewHTTP(registry)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not pass through the middleware")
			}
		}()
		serve(t, h, "GET", "/lists/1", func(http.ResponseWriter, *http.Request) {
			panic("handler is broken")
		})
	}()

	if got := testutil.ToFloat64(h.requests.WithLabelValues("GET", "GET /lists/{id}", "500")); got != 1 {
		t.Errorf("panicking request counted as %v 500s, want 1", got)
	}
	if got := testutil.ToFloat64(h.inFlight); got != 0 {
		t.Errorf("in flight = %v after the panic, want 0", got)
	}
}

func TestMiddleware_TracksInFlight(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	h := NewHTTP(registry)

	var duringRequest float64
	serve(t, h, "GET", "/lists/1", func(w http.ResponseWriter, _ *http.Request) {
		duringRequest = testutil.ToFloat64(h.inFlight)
		w.WriteHeader(http.StatusOK)
	})

	if duringRequest != 1 {
		t.Errorf("in flight during the request = %v, want 1", duringRequest)
	}
	if got := testutil.ToFloat64(h.inFlight); got != 0 {
		t.Errorf("in flight after the request = %v, want 0", got)
	}
}

func TestNewRegistry_HoldsTheRuntimeCollectors(t *testing.T) {
	t.Parallel()

	body := scrape(t, NewRegistry())
	for _, want := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_start_time_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape output is missing %s", want)
		}
	}
}

func TestHandler_KeepsScrapingAfterABrokenCollector(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	h := NewHTTP(registry)
	serve(t, h, "GET", "/lists/1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	registry.MustRegister(brokenCollector{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil)
	Handler(registry).ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Errorf("a broken collector hid the working metrics:\n%s", rec.Body.String())
	}
}

func TestMiddleware_PreservesFlushThroughResponseController(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	h := NewHTTP(registry)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/lists/1", nil)

	var flushErr error
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	})
	h.Middleware(handler).ServeHTTP(rec, req)

	if flushErr != nil {
		t.Fatalf("Flush through the status writer returned %v, want nil", flushErr)
	}
	if !rec.Flushed {
		t.Error("the underlying recorder was never flushed")
	}
}

// brokenCollector fails on every scrape, as a collector reading a dependency
// would when that dependency is down.
type brokenCollector struct{}

func (brokenCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc("broken", "always fails", nil, nil)
}

func (brokenCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.NewInvalidMetric(
		prometheus.NewDesc("broken", "always fails", nil, nil),
		errUnavailable{},
	)
}

type errUnavailable struct{}

func (errUnavailable) Error() string { return "dependency unavailable" }
