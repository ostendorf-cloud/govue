// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultPolicy(t *testing.T) {
	policy := DefaultPolicy()

	if !policy.NeedsNonce() {
		t.Error("the default policy wants a nonce in script-src and style-src")
	}
	without := policy.String()
	for _, want := range []string{
		"default-src 'none'",
		"script-src 'self'",
		"img-src 'self' data:",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	} {
		if !strings.Contains(without, want) {
			t.Errorf("policy %q is missing %q", without, want)
		}
	}
	if strings.Contains(without, "nonce-") {
		t.Errorf("a policy rendered without a nonce still carries one: %s", without)
	}

	with := policy.Header("abc")
	if !strings.Contains(with, "script-src 'self' 'nonce-abc'") ||
		!strings.Contains(with, "style-src 'self' 'nonce-abc'") {
		t.Errorf("policy with nonce = %s", with)
	}
	if strings.Contains(with, "img-src 'self' data: 'nonce-abc'") {
		t.Error("the nonce reached a directive it was not marked for")
	}
}

func TestPolicySetAddAndOrder(t *testing.T) {
	policy := NewPolicy().
		Set("default-src", "'none'").
		Set("connect-src", "'self'").
		Add("connect-src", "https://api.example.test").
		Add("worker-src", "'self'").
		Set("upgrade-insecure-requests")

	want := "default-src 'none'; connect-src 'self' https://api.example.test; worker-src 'self'; upgrade-insecure-requests"
	if got := policy.String(); got != want {
		t.Errorf("policy = %q, want %q", got, want)
	}
	if policy.NeedsNonce() {
		t.Error("a policy without a marked directive wants no nonce")
	}
}

func TestPolicySetReplacesWithoutChangingTheOrder(t *testing.T) {
	policy := NewPolicy().
		Set("default-src", "'none'").
		Set("style-src", "'self'").
		Set("img-src", "'self'").
		Set("style-src", "'self'", "'unsafe-inline'")

	want := "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'"
	if got := policy.String(); got != want {
		t.Errorf("policy = %q, want %q", got, want)
	}
}

func TestPolicyNonceInAnUnsetDirectiveIsNotNeeded(t *testing.T) {
	policy := NewPolicy().Set("default-src", "'none'").NonceIn("script-src")
	if policy.NeedsNonce() {
		t.Error("a nonce is generated for a directive the policy does not set")
	}
}

func TestSecurityHeaders(t *testing.T) {
	var nonce string
	handler := DefaultSecurity().Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonce = Nonce(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	for name, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Strict-Transport-Security":    "max-age=31536000; includeSubDomains; preload",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"X-Frame-Options":              "DENY",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := rec.Header().Get("Permissions-Policy"); !strings.Contains(got, "camera=()") {
		t.Errorf("Permissions-Policy = %q", got)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "" {
		t.Errorf("X-Robots-Tag = %q, want unset by default", got)
	}
	if nonce == "" {
		t.Fatal("the handler got no nonce")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'nonce-"+nonce+"'") {
		t.Errorf("the header nonce and the handler's differ: %s", rec.Header().Get("Content-Security-Policy"))
	}
}

func TestSecurityNonceIsFreshPerResponse(t *testing.T) {
	var nonces []string
	handler := DefaultSecurity().Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonces = append(nonces, Nonce(r.Context()))
	}))

	for range 2 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	}
	if nonces[0] == nonces[1] {
		t.Error("two responses share a nonce")
	}
}

func TestSecurityOptions(t *testing.T) {
	security := DefaultSecurity()
	security.HSTS = 0
	security.Policy = nil
	security.RobotsTag = "noindex, nofollow"
	security.FrameOptions = ""

	handler := security.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := Nonce(r.Context()); got != "" {
			t.Errorf("nonce = %q without a policy", got)
		}
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	for name, want := range map[string]string{
		"Strict-Transport-Security": "",
		"Content-Security-Policy":   "",
		"X-Frame-Options":           "",
		"X-Robots-Tag":              "noindex, nofollow",
		"X-Content-Type-Options":    "nosniff",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestSecurityHSTSWithoutSubdomains(t *testing.T) {
	security := DefaultSecurity()
	security.HSTS = 30 * 24 * time.Hour
	security.HSTSIncludeSubDomains = false
	security.HSTSPreload = false

	rec := httptest.NewRecorder()
	security.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=2592000" {
		t.Errorf("HSTS = %q", got)
	}
}

func TestSecurityPolicyWithoutNonceIsRenderedOnce(t *testing.T) {
	security := DefaultSecurity()
	security.Policy = NewPolicy().Set("default-src", "'self'")

	rec := httptest.NewRecorder()
	security.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("policy = %q", got)
	}
}
