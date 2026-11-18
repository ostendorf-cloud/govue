// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session"
)

// testClock is a clock a test moves by hand, safe for the parallel requests
// of one test.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newClockedApp is newTestApp on a clock the test controls, so the session
// store and the real MemoryDeviceStore see the same injected time.
func newClockedApp(t *testing.T) (*app, *testClock) {
	t.Helper()
	env := testEnv(t, 1000)
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	clock := &testClock{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	cfg.now = clock.Now
	a, err := newApp(cfg, logging.New(&strings.Builder{}, cfg.logLevel))
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return a, clock
}

const (
	webSubject = "web-subject"
	appSubject = "app-subject"
	sameOrigin = "https://app.example.test"
	evilOrigin = "https://evil.example.test"
)

// fixtures are the credentials of one contract run.
type fixtures struct {
	cookie    *http.Cookie
	csrfToken string
	token     string
}

func newFixtures(t *testing.T, a *app, clock *testClock) fixtures {
	t.Helper()
	rec := httptest.NewRecorder()
	s, err := a.sessions.Start(t.Context(), rec, session.Session{Subject: webSubject})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Start set %d cookies, want 1", len(cookies))
	}
	token, _, err := a.sessions.IssueDevice(t.Context(), appSubject, "phone", clock.Now())
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	return fixtures{cookie: cookies[0], csrfToken: csrf.Token(a.cfg.csrfKey, s.ID), token: token}
}

// credential is one way a request authenticates, or fails to.
type credential struct {
	name   string
	cookie bool
	auth   func(f fixtures) []string
	// as is the subject the request authenticates as, empty for none.
	as string
	// refused is a failed bearer request: 401 on every method and origin.
	refused bool
	// bearer is a request that skips the CSRF check.
	bearer bool
}

var credentials = []credential{
	{name: "neither", auth: func(fixtures) []string { return nil }},
	{name: "cookie only", cookie: true, auth: func(fixtures) []string { return nil }, as: webSubject},
	{name: "cookie and basic", cookie: true, auth: func(fixtures) []string { return []string{"Basic dXNlcjpwYXNz"} }, as: webSubject},
	{name: "bearer only", auth: func(f fixtures) []string { return []string{"Bearer " + f.token} }, as: appSubject, bearer: true},
	{name: "lower case bearer", auth: func(f fixtures) []string { return []string{"bearer " + f.token} }, as: appSubject, bearer: true},
	{name: "bearer and cookie", cookie: true, auth: func(f fixtures) []string { return []string{"Bearer " + f.token} }, as: appSubject, bearer: true},
	{name: "junk bearer and cookie", cookie: true, auth: func(fixtures) []string { return []string{"Bearer example_junk_token"} }, refused: true, bearer: true},
	{name: "empty bearer and cookie", cookie: true, auth: func(fixtures) []string { return []string{"Bearer"} }, refused: true, bearer: true},
	{name: "token with a bad checksum and cookie", cookie: true, auth: func(f fixtures) []string {
		return []string{"Bearer " + f.token[:len(f.token)-1] + flipChar(f.token[len(f.token)-1])}
	}, refused: true, bearer: true},
	{name: "two headers and cookie", cookie: true, auth: func(f fixtures) []string {
		return []string{"Bearer " + f.token, "Bearer " + f.token}
	}, refused: true, bearer: true},
	{name: "two headers without cookie", auth: func(f fixtures) []string {
		return []string{"Bearer " + f.token, "Basic dXNlcjpwYXNz"}
	}, refused: true, bearer: true},
}

func flipChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

// TestBearerCSRFContract sends the cross product of credential, method,
// origin and CSRF token through the real route chain, CORS, rate limit,
// CSRF, session and handler, and pins every outcome. ADR 0001 is the spec.
func TestBearerCSRFContract(t *testing.T) {
	t.Parallel()

	for _, cred := range credentials {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, origin := range []string{sameOrigin, evilOrigin} {
				for _, withCSRF := range []bool{false, true} {
					if method == http.MethodGet && withCSRF {
						continue
					}
					name := cred.name + "/" + method + "/" + origin
					if withCSRF {
						name += "/csrf token"
					}
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						a, clock := newClockedApp(t)
						f := newFixtures(t, a, clock)
						h := a.handler()

						var body *strings.Reader
						if method == http.MethodPost {
							body = strings.NewReader(`{"text":"contract note"}`)
						} else {
							body = strings.NewReader("")
						}
						req := httptest.NewRequestWithContext(t.Context(), method, "/notes", body)
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Origin", origin)
						if origin == sameOrigin {
							req.Header.Set("Sec-Fetch-Site", "same-origin")
						} else {
							req.Header.Set("Sec-Fetch-Site", "cross-site")
						}
						if withCSRF {
							req.Header.Set(csrf.HeaderName, f.csrfToken)
						}
						if cred.cookie {
							req.AddCookie(f.cookie)
						}
						for _, v := range cred.auth(f) {
							req.Header.Add("Authorization", v)
						}

						rec := httptest.NewRecorder()
						h.ServeHTTP(rec, req)

						want := expectedStatus(cred, method, origin, withCSRF)
						if rec.Code != want {
							t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
						}
						if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
							t.Errorf("Set-Cookie = %q, want none", got)
						}
						if cred.refused {
							if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
								t.Errorf("WWW-Authenticate = %q, want Bearer", got)
							}
						} else if got := rec.Header().Get("WWW-Authenticate"); got != "" {
							t.Errorf("WWW-Authenticate = %q, want none", got)
						}
						if cred.bearer {
							if got := rec.Header().Get("Cache-Control"); got != "no-store" {
								t.Errorf("Cache-Control = %q, want no-store", got)
							}
						}

						// Whose note it became, if any.
						created := want == http.StatusCreated
						for _, subject := range []string{webSubject, appSubject} {
							has := len(a.notes.list(subject)) == 1
							if has != (created && subject == cred.as) {
								t.Errorf("%s has a note = %v, want %v", subject, has, created && subject == cred.as)
							}
						}
						if want == http.StatusOK && cred.as != "" && !strings.HasPrefix(rec.Body.String(), "[") {
							t.Errorf("body = %s, want the subject's notes", rec.Body.String())
						}
					})
				}
			}
		}
	}
}

// expectedStatus is the contract, written as rules rather than a table, so
// each line names the rule of ADR 0001 it pins.
func expectedStatus(cred credential, method, origin string, withCSRF bool) int {
	// Every token failure is the same 401, on every method and origin, and
	// never falls back to the cookie.
	if cred.refused {
		return http.StatusUnauthorized
	}
	unsafe := method != http.MethodGet
	// A cookie request keeps both CSRF halves: the origin half refuses a
	// cross site unsafe request, the token half one without the token.
	if unsafe && !cred.bearer {
		if origin != sameOrigin {
			return http.StatusForbidden
		}
		if cred.as != "" && !withCSRF {
			return http.StatusForbidden
		}
	}
	// A bearer request skips both halves and authenticates by the token.
	if cred.as == "" {
		return http.StatusUnauthorized
	}
	if unsafe {
		return http.StatusCreated
	}
	return http.StatusOK
}

// TestBearerResponsesAreNotStored pins no-store on a route whose handler
// sets no Cache-Control of its own, so the header can only come from the
// session middleware.
func TestBearerResponsesAreNotStored(t *testing.T) {
	t.Parallel()
	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()

	for _, tc := range []struct {
		name string
		auth string
		want string
	}{
		{"bearer", "Bearer " + f.token, "no-store"},
		{"cookie", "", ""},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil)
		req.AddCookie(f.cookie)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.name, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAFailedBearerIsRefusedOnEveryRoute pins that the 401 does not depend on
// the handler: a public page and the catch all refuse it too.
func TestAFailedBearerIsRefusedOnEveryRoute(t *testing.T) {
	t.Parallel()
	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()

	for _, path := range []string{"/app", "/notes", "/login", "/this-path-was-never-registered"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.AddCookie(f.cookie)
		req.Header.Set("Authorization", "Bearer example_junk_token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("GET %s = %d %q, want 401 with WWW-Authenticate: Bearer", path, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
		if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
			t.Errorf("GET %s Set-Cookie = %q, want none", path, got)
		}
	}
}

// TestAnExpiredTokenNeverFallsBackToTheCookie moves the injected clock past
// the device idle timeout. The cookie session is still valid at that time,
// so a fallback would show.
func TestAnExpiredTokenNeverFallsBackToTheCookie(t *testing.T) {
	t.Parallel()
	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()

	clock.Add(session.DefaultDeviceIdle)
	// A fresh cookie session at the new time, so the cookie is valid.
	f.cookie = newFixtures(t, a, clock).cookie

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/notes", nil)
	req.AddCookie(f.cookie)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}

	cookieOnly := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/notes", nil)
	cookieOnly.AddCookie(f.cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, cookieOnly)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie only status = %d, want 200: the cookie session is valid", rec.Code)
	}
}

// TestBearerLogoutSignsTheDeviceOut pins End on a bearer request through the
// chain: the device is revoked, the cookie session stays, no cookie is set.
func TestBearerLogoutSignsTheDeviceOut(t *testing.T) {
	t.Parallel()
	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.AddCookie(f.cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %q, want none", got)
	}

	again := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/notes", nil)
	again.Header.Set("Authorization", "Bearer "+f.token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, again)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the token after logout = %d, want 401", rec.Code)
	}

	cookieOnly := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/notes", nil)
	cookieOnly.AddCookie(f.cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, cookieOnly)
	if rec.Code != http.StatusOK {
		t.Errorf("the cookie session after a device logout = %d, want 200", rec.Code)
	}
}

// TestPreflightIsAnsweredBeforeAuthentication pins the order: CORS answers a
// preflight before CSRF and before any session read, whatever it carries.
func TestPreflightIsAnsweredBeforeAuthentication(t *testing.T) {
	t.Parallel()
	a, _ := newClockedApp(t)
	h := a.handler()

	for _, tc := range []struct {
		origin string
		want   int
	}{
		{sameOrigin, http.StatusNoContent},
		{evilOrigin, http.StatusForbidden},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/notes", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "authorization")
		req.Header.Set("Authorization", "Bearer example_junk_token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("preflight from %s = %d, want %d", tc.origin, rec.Code, tc.want)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("preflight from %s reached the session middleware: WWW-Authenticate %q", tc.origin, got)
		}
	}
}
