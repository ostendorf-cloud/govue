// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package csrf_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ostendorf-cloud/govue/csrf"
)

// countingExpected always has a session to protect and counts how often it
// was asked, so a test sees whether the token half ran at all.
func countingExpected(calls *atomic.Int32) csrf.Expected {
	return func(*http.Request) (string, bool) {
		calls.Add(1)
		return csrf.Token(key, "session-1"), true
	}
}

// newProtector returns a protector on countingExpected, with the bearer
// exemption when exempt is set.
func newProtector(t *testing.T, calls *atomic.Int32, exempt bool) *csrf.Protector {
	t.Helper()
	p, err := csrf.New(countingExpected(calls))
	if err != nil {
		t.Fatal(err)
	}
	if exempt {
		p.ExemptBearer()
	}
	return p
}

// authShapes are the Authorization headers the tests below send, with
// whether the shared predicate calls them bearer or malformed.
var authShapes = []struct {
	name   string
	auth   []string
	bearer bool
}{
	{"no header", nil, false},
	{"bearer", []string{"Bearer example_fake"}, true},
	{"lower case bearer", []string{"bearer example_fake"}, true},
	{"upper case bearer", []string{"BEARER example_fake"}, true},
	{"bearer without a token", []string{"Bearer"}, true},
	{"bearer with a junk token", []string{"Bearer not-a-token"}, true},
	{"two headers", []string{"Bearer a", "Bearer b"}, true},
	{"two basic headers", []string{"Basic a", "Basic b"}, true},
	{"basic", []string{"Basic dXNlcjpwYXNz"}, false},
	{"a scheme glued to the token", []string{"Bearerx"}, false},
	{"a scheme with a comma", []string{"Bearer, x"}, false},
	{"a tab after the scheme", []string{"Bearer\tx"}, false},
	{"an empty value", []string{""}, false},
	{"a non ASCII scheme", []string{"Bеarer x"}, false},
}

// checkBothHalves asserts that p ran both halves on a cross site and a same
// origin unsafe request carrying auth.
func checkBothHalves(t *testing.T, p *csrf.Protector, calls *atomic.Int32, auth []string, extra http.Header) {
	t.Helper()
	for _, site := range []string{"cross-site", "same-origin"} {
		calls.Store(0)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
		req.Header.Set("Sec-Fetch-Site", site)
		for _, v := range auth {
			req.Header.Add("Authorization", v)
		}
		for k, vs := range extra {
			req.Header[k] = vs
		}
		err := p.Check(req)
		switch site {
		case "cross-site":
			if err == nil || errors.Is(err, csrf.ErrNoToken) || errors.Is(err, csrf.ErrBadToken) {
				t.Errorf("cross site: Check = %v, want the origin half to refuse", err)
			}
		default:
			if !errors.Is(err, csrf.ErrNoToken) {
				t.Errorf("same origin: Check = %v, want ErrNoToken", err)
			}
			if calls.Load() != 1 {
				t.Errorf("same origin: Expected ran %d times, want 1", calls.Load())
			}
		}
	}
}

// TestBearerIsCheckedByDefault pins the default: without ExemptBearer every
// request keeps both halves, whatever Authorization header it carries.
func TestBearerIsCheckedByDefault(t *testing.T) {
	t.Parallel()

	for _, tc := range authShapes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			checkBothHalves(t, newProtector(t, &calls, false), &calls, tc.auth, nil)
		})
	}
}

// TestCSRFAloneDoesNotSkipAnInjectedBearer is the review's repro: csrf in
// front of a handler that authenticates by a cookie of its own, behind a
// proxy that adds a bearer header to every request. With the default, a cross
// site form post does not reach the handler.
func TestCSRFAloneDoesNotSkipAnInjectedBearer(t *testing.T) {
	t.Parallel()

	p, err := csrf.New(func(r *http.Request) (string, bool) {
		c, err := r.Cookie("own")
		if err != nil {
			return "", false
		}
		return csrf.Token(key, c.Value), true
	})
	if err != nil {
		t.Fatal(err)
	}
	reached := false
	h := p.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/transfer", strings.NewReader("to=attacker"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.test")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer injected-by-a-proxy")
	req.AddCookie(&http.Cookie{Name: "own", Value: "victim-session"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if reached || rec.Code != http.StatusForbidden {
		t.Errorf("reached %v status %d, want 403 before the handler", reached, rec.Code)
	}
}

// TestExemptBearerSkipsBothHalves pins the CSRF boundary of ADR 0001 with the
// exemption on. A bearer and a malformed request skip the origin and the
// token half, and Expected is not called. Every other request keeps both
// halves exactly as before.
func TestExemptBearerSkipsBothHalves(t *testing.T) {
	t.Parallel()

	for _, tc := range authShapes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			p := newProtector(t, &calls, true)
			if !tc.bearer {
				checkBothHalves(t, p, &calls, tc.auth, nil)
				return
			}
			for _, site := range []string{"cross-site", "same-origin", ""} {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
				if site != "" {
					req.Header.Set("Sec-Fetch-Site", site)
				}
				for _, v := range tc.auth {
					req.Header.Add("Authorization", v)
				}
				if err := p.Check(req); err != nil {
					t.Errorf("%q: Check = %v, want nil: a bearer request skips both halves", site, err)
				}
			}
			if calls.Load() != 0 {
				t.Errorf("Expected ran %d times, want 0: a bearer request never reads the session", calls.Load())
			}
		})
	}
}

// TestExemptBearerTwiceStaysOn pins that the option sets, never toggles.
func TestExemptBearerTwiceStaysOn(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, true)
	p.ExemptBearer()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer example_fake")
	if err := p.Check(req); err != nil {
		t.Errorf("Check after two calls = %v, want the exemption", err)
	}
}

// TestExemptBearerSkipsNothingElse pins that the exemption follows the
// Authorization header alone. No other header buys a skip.
func TestExemptBearerSkipsNothingElse(t *testing.T) {
	t.Parallel()

	for name, extra := range map[string]http.Header{
		"X-Requested-With":         {"X-Requested-With": {"XMLHttpRequest"}},
		"Proxy-Authorization":      {"Proxy-Authorization": {"Bearer example_fake"}},
		"X-Authorization":          {"X-Authorization": {"Bearer example_fake"}},
		"a lower case key":         {"authorization": {"Bearer example_fake"}},
		"X-Forwarded-Access-Token": {"X-Forwarded-Access-Token": {"example_fake"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			checkBothHalves(t, newProtector(t, &calls, true), &calls, nil, extra)
		})
	}
}

// TestACookieRequestStillNeedsTheRightToken pins the token half for a cookie
// request with an Authorization header of another scheme.
func TestACookieRequestStillNeedsTheRightToken(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, true)
	for _, tc := range []struct {
		name  string
		token string
		want  error
	}{
		{"the right token", csrf.Token(key, "session-1"), nil},
		{"another session's token", csrf.Token(key, "session-2"), csrf.ErrBadToken},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		req.Header.Set(csrf.HeaderName, tc.token)
		if err := p.Check(req); !errors.Is(err, tc.want) {
			t.Errorf("%s: Check = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestMiddlewarePassesABearerRequestThrough pins that the skip reaches the
// handler through the middleware, not only through Check.
func TestMiddlewarePassesABearerRequestThrough(t *testing.T) {
	t.Parallel()

	for _, exempt := range []bool{true, false} {
		var calls atomic.Int32
		h := newProtector(t, &calls, exempt).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))

		for _, tc := range []struct {
			name string
			auth string
			want int
		}{
			{"bearer", "Bearer example_fake", http.StatusTeapot},
			{"basic", "Basic dXNlcjpwYXNz", http.StatusForbidden},
		} {
			want := tc.want
			if !exempt {
				want = http.StatusForbidden
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/things/1", nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Authorization", tc.auth)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != want {
				t.Errorf("exempt %v, %s: status = %d, want %d", exempt, tc.name, rec.Code, want)
			}
		}
	}
}
