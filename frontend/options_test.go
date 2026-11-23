// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"net/http"
	"testing"
)

// TestOptionsStarAnswers404WithTheHeaders covers what the handler answers
// to OPTIONS *: the 404 page with the security headers. That the request
// reaches the handler under Run at all is pinned in httpserver, by
// TestServeHandsOptionsStarToTheHandler, which goes through http.Server.
func TestOptionsStarAnswers404WithTheHeaders(t *testing.T) {
	res := do(t, newServer(t).Handler(), http.MethodOptions, "*", nil)
	_ = body(t, res)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("OPTIONS * lacks the security headers: %v", res.Header)
	}
}
