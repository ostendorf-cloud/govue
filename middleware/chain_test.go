// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// recordingMiddleware appends name to order when it runs on the way in, and
// again, suffixed, on the way out, so a test can read off the exact order
// every layer ran in.
func recordingMiddleware(order *[]string, name string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name+":in")
			next.ServeHTTP(w, r)
			*order = append(*order, name+":out")
		})
	}
}

func TestChainRunsTheFirstMiddlewareOutermost(t *testing.T) {
	var order []string
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	})

	chained := Chain(handler,
		recordingMiddleware(&order, "A"),
		recordingMiddleware(&order, "B"),
		recordingMiddleware(&order, "C"),
	)
	chained.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	want := []string{"A:in", "B:in", "C:in", "handler", "C:out", "B:out", "A:out"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, got := range order {
		if got != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestChainWithNoMiddlewaresReturnsTheHandlerUnchanged(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	Chain(handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if !called {
		t.Error("the handler was not called")
	}
}
