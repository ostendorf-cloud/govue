// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package csrf_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ostendorf-cloud/govue/csrf"
)

const exemptPath = "/api/v1/auth/token"

// crossRequest is an unsafe request from another site, as a native app's
// webview sends it.
func crossRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "capacitor://localhost")

	return r
}

func TestExemptOriginSkipsTheOriginHalfForOneRoute(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.ExemptOrigin(http.MethodPost, exemptPath); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"the exact route", http.MethodPost, exemptPath, true},
		{"another method", http.MethodPut, exemptPath, false},
		{"a neighbouring path", http.MethodPost, exemptPath + "/rotate", false},
		{"a trailing slash", http.MethodPost, exemptPath + "/", false},
		{"a shorter path", http.MethodPost, "/api/v1/auth", false},
		{"another route", http.MethodPost, "/notes", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := p.Check(crossRequest(t, tc.method, tc.path))
			// The exempt route has no session behind it here, so the token
			// half is what is left: countingExpected has one, so it refuses
			// with ErrNoToken, which proves the origin half did not.
			if tc.want && (err == nil || !strings.Contains(err.Error(), "token")) {
				t.Errorf("exempt route: err = %v, want the token half only", err)
			}
			if !tc.want && (err == nil || !strings.Contains(err.Error(), "cross origin")) {
				t.Errorf("err = %v, want the origin refusal", err)
			}
		})
	}
}

// The token half stays on the exempt route: a request with a session needs
// its token, an app without a session needs none.
func TestExemptOriginKeepsTheTokenHalf(t *testing.T) {
	t.Parallel()

	p, err := csrf.New(func(*http.Request) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ExemptOrigin(http.MethodPost, exemptPath); err != nil {
		t.Fatal(err)
	}
	if err := p.Check(crossRequest(t, http.MethodPost, exemptPath)); err != nil {
		t.Errorf("no session: err = %v, want nil", err)
	}

	var calls atomic.Int32
	q := newProtector(t, &calls, false)
	if err := q.ExemptOrigin(http.MethodPost, exemptPath); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	q.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request with a session and no token reached the handler")
	})).ServeHTTP(rec, crossRequest(t, http.MethodPost, exemptPath))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestExemptOriginRefusesWhatCouldWiden(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"no method", "", exemptPath},
		{"a lower case method", "post", exemptPath},
		{"a method with a space", "POST /x", exemptPath},
		{"a host part", http.MethodPost, "example.test/token"},
		{"no leading slash", http.MethodPost, "api/token"},
		{"the root", http.MethodPost, "/"},
		{"a trailing slash", http.MethodPost, "/api/"},
		{"a subtree wildcard", http.MethodPost, "/api/{rest...}"},
		{"a variable", http.MethodPost, "/api/{id}"},
		{"the end anchor", http.MethodPost, "/{$}"},
		{"a star", http.MethodPost, "/api/*"},
		{"a query", http.MethodPost, "/api?x=1"},
		{"a space", http.MethodPost, "/api /x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			if err := newProtector(t, &calls, false).ExemptOrigin(tc.method, tc.path); err == nil {
				t.Errorf("ExemptOrigin(%q, %q) was accepted", tc.method, tc.path)
			}
		})
	}
}

// A second call for the same route is an error, never the panic net/http
// raises for a conflicting pattern.
func TestExemptOriginTwiceIsAnError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.ExemptOrigin(http.MethodPost, exemptPath); err != nil {
		t.Fatal(err)
	}
	if err := p.ExemptOrigin(http.MethodPost, exemptPath); err == nil {
		t.Error("the second call was accepted")
	}
}

// The exemption names no trusted origin: another route still refuses.
func TestExemptOriginDoesNotTrustTheOrigin(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.ExemptOrigin(http.MethodPost, exemptPath); err != nil {
		t.Fatal(err)
	}
	if err := p.Check(crossRequest(t, http.MethodPost, "/other")); err == nil {
		t.Error("another route from the same origin was accepted")
	}
}
