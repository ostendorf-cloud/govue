// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package csrf_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

// TestBearerRequestsSkipBothHalves pins the CSRF boundary of ADR 0001. A
// bearer and a malformed request skip the origin and the token half: a
// browser never attaches an Authorization header by itself, and session
// never reads the cookie on such a request. Every other request keeps both
// halves exactly as before.
func TestBearerRequestsSkipBothHalves(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		auth        []string
		wantSkipped bool
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
		{"an empty value", []string{""}, false},
		{"a non ASCII scheme", []string{"Bеarer x"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, origin := range []struct {
				name     string
				site     string
				wantErr  bool
				wantCall bool
			}{
				// Cross site, the origin half refuses before the token half.
				{"cross site", "cross-site", true, false},
				// Same origin without a token, the token half refuses.
				{"same origin without a token", "same-origin", true, true},
			} {
				t.Run(origin.name, func(t *testing.T) {
					t.Parallel()

					var calls atomic.Int32
					p, err := csrf.New(countingExpected(&calls))
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
					req.Header.Set("Sec-Fetch-Site", origin.site)
					for _, v := range tc.auth {
						req.Header.Add("Authorization", v)
					}

					err = p.Check(req)
					if tc.wantSkipped {
						if err != nil {
							t.Errorf("Check = %v, want nil: a bearer request skips both halves", err)
						}
						if calls.Load() != 0 {
							t.Errorf("Expected ran %d times, want 0: a bearer request never reads the session", calls.Load())
						}
						return
					}
					if (err != nil) != origin.wantErr {
						t.Errorf("Check = %v, want an error: a cookie request keeps both halves", err)
					}
					if origin.site == "cross-site" && err != nil && (errors.Is(err, csrf.ErrNoToken) || errors.Is(err, csrf.ErrBadToken)) {
						t.Errorf("Check = %v, want the origin half to refuse first", err)
					}
					if origin.site == "same-origin" && !errors.Is(err, csrf.ErrNoToken) {
						t.Errorf("Check = %v, want ErrNoToken", err)
					}
					if got := calls.Load() != 0; got != origin.wantCall {
						t.Errorf("Expected ran = %v, want %v", got, origin.wantCall)
					}
				})
			}
		})
	}
}

// TestACookieRequestStillNeedsTheRightToken pins the token half for a cookie
// request with an Authorization header of another scheme.
func TestACookieRequestStillNeedsTheRightToken(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p, err := csrf.New(countingExpected(&calls))
	if err != nil {
		t.Fatal(err)
	}
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

	var calls atomic.Int32
	p, err := csrf.New(countingExpected(&calls))
	if err != nil {
		t.Fatal(err)
	}
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/things/1", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Authorization", tc.auth)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}
