// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package metrics counts and times HTTP requests and serves them for
// scraping.
//
// Every series is labelled by the route pattern, never by the raw path: a
// path carries ids and tokens, so it would create a new series per request
// and fill the database with cardinality nobody can query.
//
// This package provides only the shared registry and the HTTP
// instrumentation. An application specific metric is out of scope here and
// is registered by the application itself.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// unmatchedRoute is the label used when a request never matched a mux
// pattern, so an unregistered path or a probe cannot create a new series.
const unmatchedRoute = "unmatched"

// NewRegistry is a registry holding the Go runtime and process collectors,
// the two every app wants and nothing more.
func NewRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return registry
}

// Handler serves gatherer's metrics for a scraper.
func Handler(gatherer prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		// A broken collector must not take the whole scrape down.
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// HTTP holds the request metrics of one app.
type HTTP struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
	now      func() time.Time
}

// NewHTTP registers the request metrics into registry, so a test can build a
// fresh one and nothing lands in the global default registry.
func NewHTTP(registry prometheus.Registerer) *HTTP {
	labels := []string{"method", "route", "status"}
	h := &HTTP{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Requests answered, by method, route pattern and status.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "Time to answer a request, by method, route pattern and status.",
			// The default buckets, 5ms to 10s, which is the range a web
			// request lives in.
			Buckets: prometheus.DefBuckets,
		}, labels),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Requests being handled right now.",
		}),
		now: time.Now,
	}
	registry.MustRegister(h.requests, h.duration, h.inFlight)
	return h
}

// Middleware counts and times what passes through it, labelled by the route
// pattern matched for the request, never the raw path.
//
// The pattern comes from r.Pattern, which a http.ServeMux sets while
// matching, before it calls the final handler. It is only populated once
// next.ServeHTTP has returned, so this middleware must wrap the mux rather
// than sit inside it.
func (h *HTTP) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.inFlight.Inc()
		defer h.inFlight.Dec()

		start := h.now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		// Deferred, so a panicking request is counted as the 500 it will be
		// answered with instead of disappearing from the metrics. observe
		// guards against running twice: once here, once from the normal
		// return below.
		counted := false
		observe := func(status int) {
			if counted {
				return
			}
			counted = true
			route := r.Pattern
			if route == "" {
				route = unmatchedRoute
			}
			labels := prometheus.Labels{
				"method": r.Method,
				"route":  route,
				"status": strconv.Itoa(status),
			}
			h.requests.With(labels).Inc()
			h.duration.With(labels).Observe(h.now().Sub(start).Seconds())
		}
		defer func() {
			observe(http.StatusInternalServerError)
		}()

		next.ServeHTTP(rec, r)
		observe(rec.status)
	})
}

// statusWriter wraps a http.ResponseWriter to capture the status code for
// the metrics above. It defaults to 200 when the handler calls Write without
// an explicit WriteHeader first, exactly as the real response would. It
// exposes the wrapped writer through Unwrap, so http.ResponseController can
// reach a capability such as Flush or SetWriteDeadline straight through it,
// matching middleware.statusWriter.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code before passing it through.
func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap exposes the real http.ResponseWriter so http.ResponseController can
// reach a capability such as Flush or SetWriteDeadline straight through this
// wrapper.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
