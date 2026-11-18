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

const (
	routePath   = "/api/v1/auth/token"
	appOrigin   = "capacitor://localhost"
	otherOrigin = "https://evil.example.test"
)

// crossRequest is an unsafe request from another site, as a native app's
// webview sends it.
func crossRequest(t *testing.T, method, path, origin string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", origin)

	return r
}

func TestTrustOriginOnRouteTrustsOneRouteAndTheGivenOrigins(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.TrustOriginOnRoute(http.MethodPost, routePath, []string{appOrigin}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		origin string
		// pass is true when the origin half lets the request through. The
		// protector in this test has a session for everyone, so a request
		// that passes the origin half is then refused by the token half.
		pass bool
	}{
		{"the listed origin on the exact route", http.MethodPost, routePath, appOrigin, true},
		{"an origin that is not listed on the route", http.MethodPost, routePath, otherOrigin, false},
		{"the listed origin in another case", http.MethodPost, routePath, "CAPACITOR://localhost", false},
		{"the listed origin with a trailing slash", http.MethodPost, routePath, appOrigin + "/", false},
		{"another method", http.MethodPut, routePath, appOrigin, false},
		{"a neighbouring path", http.MethodPost, routePath + "/rotate", appOrigin, false},
		{"a trailing slash", http.MethodPost, routePath + "/", appOrigin, false},
		{"a shorter path", http.MethodPost, "/api/v1/auth", appOrigin, false},
		{"another route", http.MethodPost, "/notes", appOrigin, false},
		{"an upper case path", http.MethodPost, "/API/V1/AUTH/TOKEN", appOrigin, false},
		{"a path with one upper case letter", http.MethodPost, "/api/v1/auth/Token", appOrigin, false},
		{"an encoded slash", http.MethodPost, "/api/v1/auth%2Ftoken", appOrigin, false},
		{"an encoded slash in lower case", http.MethodPost, "/api/v1/auth%2ftoken", appOrigin, false},
		{"an encoded letter", http.MethodPost, "/api/v1/auth/%74oken", appOrigin, false},
		{"an encoded letter in upper case", http.MethodPost, "/api/v1/auth/%54oken", appOrigin, false},
		{"a double slash", http.MethodPost, "/api/v1//auth/token", appOrigin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := p.Check(crossRequest(t, tc.method, tc.path, tc.origin))
			if err == nil {
				t.Fatal("the request passed both halves")
			}
			// The token half is what is left after the origin half: the
			// protector has a session for everyone and the request no token.
			originRefused := strings.Contains(err.Error(), "cross origin")
			if tc.pass == originRefused {
				t.Errorf("err = %v, origin half passes = %v", err, tc.pass)
			}
		})
	}
}

// A same origin request keeps working on the route, and an origin of
// TrustOrigin is not trusted on it: the list given here is the whole list.
func TestTrustOriginOnRouteListIsTheWholeListOnTheRoute(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.TrustOrigin("https://web.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := p.TrustOriginOnRoute(http.MethodPost, routePath, []string{appOrigin}); err != nil {
		t.Fatal(err)
	}

	same := httptest.NewRequestWithContext(t.Context(), http.MethodPost, routePath, nil)
	same.Header.Set("Sec-Fetch-Site", "same-origin")
	if err := p.Check(same); err == nil || strings.Contains(err.Error(), "cross origin") {
		t.Errorf("same origin: err = %v, want the token half only", err)
	}
	if err := p.Check(crossRequest(t, http.MethodPost, routePath, "https://web.example.test")); err == nil ||
		!strings.Contains(err.Error(), "cross origin") {
		t.Errorf("a web origin on the route: err = %v, want the origin refusal", err)
	}
	if err := p.Check(crossRequest(t, http.MethodPost, "/notes", "https://web.example.test")); err == nil ||
		strings.Contains(err.Error(), "cross origin") {
		t.Errorf("a web origin elsewhere: err = %v, want the token half only", err)
	}
}

// The token half stays on the route: a request with a session needs its
// token, an app without a session needs none.
func TestTrustOriginOnRouteKeepsTheTokenHalf(t *testing.T) {
	t.Parallel()

	p, err := csrf.New(func(*http.Request) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if err := p.TrustOriginOnRoute(http.MethodPost, routePath, []string{appOrigin}); err != nil {
		t.Fatal(err)
	}
	if err := p.Check(crossRequest(t, http.MethodPost, routePath, appOrigin)); err != nil {
		t.Errorf("no session: err = %v, want nil", err)
	}

	var calls atomic.Int32
	q := newProtector(t, &calls, false)
	if err := q.TrustOriginOnRoute(http.MethodPost, routePath, []string{appOrigin}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	q.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request with a session and no token reached the handler")
	})).ServeHTTP(rec, crossRequest(t, http.MethodPost, routePath, appOrigin))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestTrustOriginOnRouteRefusesWhatCouldWiden(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		method  string
		path    string
		origins []string
	}{
		{"no method", "", routePath, []string{appOrigin}},
		{"a lower case method", "post", routePath, []string{appOrigin}},
		{"a method with a space", "POST /x", routePath, []string{appOrigin}},
		{"GET", http.MethodGet, routePath, []string{appOrigin}},
		{"HEAD", http.MethodHead, routePath, []string{appOrigin}},
		{"OPTIONS", http.MethodOptions, routePath, []string{appOrigin}},
		{"CONNECT", http.MethodConnect, routePath, []string{appOrigin}},
		{"TRACE", http.MethodTrace, routePath, []string{appOrigin}},
		{"a host part", http.MethodPost, "example.test/token", []string{appOrigin}},
		{"no leading slash", http.MethodPost, "api/token", []string{appOrigin}},
		{"the root", http.MethodPost, "/", []string{appOrigin}},
		{"a trailing slash", http.MethodPost, "/api/", []string{appOrigin}},
		{"a subtree wildcard", http.MethodPost, "/api/{rest...}", []string{appOrigin}},
		{"a variable", http.MethodPost, "/api/{id}", []string{appOrigin}},
		{"the end anchor", http.MethodPost, "/{$}", []string{appOrigin}},
		{"a star", http.MethodPost, "/api/*", []string{appOrigin}},
		{"a query", http.MethodPost, "/api?x=1", []string{appOrigin}},
		{"a space", http.MethodPost, "/api /x", []string{appOrigin}},
		{"a parent segment", http.MethodPost, "/a/../b", []string{appOrigin}},
		{"a trailing parent segment", http.MethodPost, "/a/..", []string{appOrigin}},
		{"an encoded slash", http.MethodPost, "/a%2Fb", []string{appOrigin}},
		{"an encoded letter", http.MethodPost, "/a/%74", []string{appOrigin}},
		{"a double slash", http.MethodPost, "//x", []string{appOrigin}},
		{"a double slash inside", http.MethodPost, "/a//x", []string{appOrigin}},
		{"a dot segment", http.MethodPost, "/./x", []string{appOrigin}},
		{"a dot file", http.MethodPost, "/.x", []string{appOrigin}},
		{"no origin", http.MethodPost, routePath, nil},
		{"an empty origin list", http.MethodPost, routePath, []string{}},
		{"an empty origin", http.MethodPost, routePath, []string{""}},
		{"a wildcard origin", http.MethodPost, routePath, []string{"*"}},
		{"an origin with a path", http.MethodPost, routePath, []string{"https://app.example.test/x"}},
		{"an origin with a trailing slash", http.MethodPost, routePath, []string{"https://app.example.test/"}},
		{"an origin without a scheme", http.MethodPost, routePath, []string{"app.example.test"}},
		{"null", http.MethodPost, routePath, []string{"null"}},
		{"one bad origin among good ones", http.MethodPost, routePath, []string{appOrigin, "*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			p := newProtector(t, &calls, false)
			if err := p.TrustOriginOnRoute(tc.method, tc.path, tc.origins); err == nil {
				t.Errorf("TrustOriginOnRoute(%q, %q, %q) was accepted", tc.method, tc.path, tc.origins)
			}
			// A refused call leaves nothing behind.
			if err := p.Check(crossRequest(t, http.MethodPost, routePath, appOrigin)); err == nil ||
				!strings.Contains(err.Error(), "cross origin") {
				t.Errorf("after a refused call: err = %v, want the origin refusal", err)
			}
		})
	}
}

// The route is matched case sensitively: a route registered with an upper case
// letter does not trust the lower case spelling, and the other way round.
func TestTrustOriginOnRouteIsCaseSensitive(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.TrustOriginOnRoute(http.MethodPost, "/T", []string{appOrigin}); err != nil {
		t.Fatal(err)
	}
	if err := p.Check(crossRequest(t, http.MethodPost, "/T", appOrigin)); err == nil ||
		strings.Contains(err.Error(), "cross origin") {
		t.Errorf("the exact spelling: err = %v, want the token half only", err)
	}
	for _, path := range []string{"/t", "/%54"} {
		if err := p.Check(crossRequest(t, http.MethodPost, path, appOrigin)); err == nil ||
			!strings.Contains(err.Error(), "cross origin") {
			t.Errorf("path %q: err = %v, want the origin refusal", path, err)
		}
	}
}

// A second call for the same route is an error, never a silent replacement.
func TestTrustOriginOnRouteTwiceIsAnError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	p := newProtector(t, &calls, false)
	if err := p.TrustOriginOnRoute(http.MethodPost, routePath, []string{appOrigin}); err != nil {
		t.Fatal(err)
	}
	if err := p.TrustOriginOnRoute(http.MethodPost, routePath, []string{otherOrigin}); err == nil {
		t.Error("the second call was accepted")
	}
	if err := p.Check(crossRequest(t, http.MethodPost, routePath, otherOrigin)); err == nil ||
		!strings.Contains(err.Error(), "cross origin") {
		t.Errorf("the second call changed the list: err = %v", err)
	}
	// Another route is a different key.
	if err := p.TrustOriginOnRoute(http.MethodPost, "/other", []string{appOrigin}); err != nil {
		t.Errorf("another route: %v", err)
	}
}
