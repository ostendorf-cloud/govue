// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

const exposeHeader = "Access-Control-Expose-Headers"

// exposingAppCORS builds the app handler with the given expose names and the
// web policy web, which may be nil.
func exposingAppCORS(t *testing.T, web *CORS, expose []string) http.Handler {
	t.Helper()
	c, err := NewAppCORSWithConfig(mustAppOrigins(t, "capacitor://localhost"), web, AppCORSConfig{ExposeHeaders: expose})
	if err != nil {
		t.Fatal(err)
	}

	return c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
}

func appExposeDo(t *testing.T, h http.Handler, method, origin string, preflight bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "/api/v1/auth/token", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if preflight {
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

func TestAppCORSExposeHeadersOnAnAllowedOrigin(t *testing.T) {
	t.Parallel()
	rec := appExposeDo(t, exposingAppCORS(t, nil, []string{"Retry-After"}), http.MethodPost, "capacitor://localhost", false)
	if got := rec.Header().Get(exposeHeader); got != "Retry-After" {
		t.Errorf("expose headers = %q, want Retry-After", got)
	}
	if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Origin" {
		t.Errorf("Vary = %q, want one Origin", got)
	}
}

func TestAppCORSExposeHeadersNotOnARefusedOrigin(t *testing.T) {
	t.Parallel()
	web, err := NewCORS(CORSConfig{Origins: []string{"https://web.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]http.Handler{
		"without web": exposingAppCORS(t, nil, []string{"Retry-After"}),
		"with web":    exposingAppCORS(t, web, []string{"Retry-After"}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, origin := range []string{"https://evil.example.test", ""} {
				rec := appExposeDo(t, h, http.MethodGet, origin, false)
				if _, ok := rec.Header()[exposeHeader]; ok {
					t.Errorf("origin %q got %q", origin, rec.Header().Get(exposeHeader))
				}
			}
		})
	}
}

func TestAppCORSExposeHeadersNotOnAPreflight(t *testing.T) {
	t.Parallel()
	rec := appExposeDo(t, exposingAppCORS(t, nil, []string{"Retry-After"}), http.MethodOptions, "capacitor://localhost", true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight = %d", rec.Code)
	}
	if _, ok := rec.Header()[exposeHeader]; ok {
		t.Errorf("preflight carries %q", rec.Header().Get(exposeHeader))
	}
}

func TestAppCORSExposeHeadersDefaultSendsNothing(t *testing.T) {
	t.Parallel()
	web, err := NewCORS(CORSConfig{Origins: []string{"https://web.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := NewAppCORS(mustAppOrigins(t, "capacitor://localhost"), web)
	if err != nil {
		t.Fatal(err)
	}
	next := plain.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for name, h := range map[string]http.Handler{
		"NewAppCORS":  next,
		"nil list":    exposingAppCORS(t, nil, nil),
		"empty list":  exposingAppCORS(t, nil, []string{}),
		"zero config": exposingAppCORS(t, web, nil),
	} {
		t.Run(name, func(t *testing.T) {
			rec := appExposeDo(t, h, http.MethodGet, "capacitor://localhost", false)
			if _, ok := rec.Header()[exposeHeader]; ok {
				t.Errorf("header sent: %q", rec.Header().Get(exposeHeader))
			}
		})
	}
}

func TestAppCORSExposeHeadersAreNormalisedAndOrdered(t *testing.T) {
	t.Parallel()
	h := exposingAppCORS(t, nil, []string{"x-request-id", "RETRY-AFTER", "X-Request-Id", "retry-after", "etag"})
	rec := appExposeDo(t, h, http.MethodGet, "capacitor://localhost", false)
	if got, want := rec.Header().Get(exposeHeader), "X-Request-Id, Retry-After, Etag"; got != want {
		t.Errorf("expose headers = %q, want %q", got, want)
	}
}

func TestNewAppCORSRefusesBadExposeHeaders(t *testing.T) {
	t.Parallel()
	// A distinctive value that must never appear in an error text.
	const secret = "X-Never-Echo-Me bad"
	for _, tc := range []struct {
		name   string
		expose []string
	}{
		{"empty name", []string{"Retry-After", ""}},
		{"space in name", []string{secret}},
		{"colon", []string{"Retry-After:"}},
		{"comma", []string{"A,B"}},
		{"control character", []string{"A\r\nB"}},
		{"wildcard alone", []string{"*"}},
		{"wildcard mixed", []string{"Retry-After", "*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewAppCORSWithConfig(mustAppOrigins(t, "capacitor://localhost"), nil,
				AppCORSConfig{ExposeHeaders: tc.expose})
			if err == nil || c != nil {
				t.Fatalf("%q was accepted", tc.expose)
			}
			for _, v := range tc.expose {
				if v != "" && v != "*" {
					configtexttest.AssertHidden(t, err, v, "ExposeHeaders")
				}
			}
		})
	}
}

func TestAppCORSExposeHeadersLeaveTheWebPolicyAlone(t *testing.T) {
	t.Parallel()
	web, err := NewCORS(CORSConfig{Origins: []string{"https://web.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	h := exposingAppCORS(t, web, []string{"Retry-After", "X-App-Only"})
	rec := appExposeDo(t, h, http.MethodGet, "https://web.example.test", false)
	if _, ok := rec.Header()[exposeHeader]; ok {
		t.Errorf("web origin got the app list: %q", rec.Header().Get(exposeHeader))
	}

	// The web policy keeps its own list, and the app list does not change it.
	webExposing, err := NewCORS(CORSConfig{Origins: []string{"https://web.example.test"}, ExposeHeaders: []string{"Etag"}})
	if err != nil {
		t.Fatal(err)
	}
	rec = appExposeDo(t, exposingAppCORS(t, webExposing, []string{"Retry-After"}), http.MethodGet, "https://web.example.test", false)
	if got := rec.Header().Get(exposeHeader); got != "Etag" {
		t.Errorf("web expose headers = %q, want Etag", got)
	}
}
