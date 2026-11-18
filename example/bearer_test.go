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

	challengeNoToken = "Bearer"
	challengeInvalid = `Bearer error="invalid_token"`
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
	// challenge is the WWW-Authenticate of a refused bearer request: 401 on
	// every method and origin. Empty for a request that is not refused.
	challenge string
	// bearer is a request the shared predicate calls bearer or malformed:
	// it skips the CSRF check and never reads the cookie.
	bearer bool
}

func noAuth(fixtures) []string { return nil }

func fixed(values ...string) func(fixtures) []string {
	return func(fixtures) []string { return values }
}

var credentials = []credential{
	{name: "neither", auth: noAuth},
	{name: "cookie only", cookie: true, auth: noAuth, as: webSubject},
	{name: "basic without cookie", auth: fixed("Basic dXNlcjpwYXNz")},
	// Shapes the predicate calls none: the cookie authenticates and both
	// CSRF halves apply, in session and in csrf alike.
	{name: "cookie and basic", cookie: true, auth: fixed("Basic dXNlcjpwYXNz"), as: webSubject},
	{name: "cookie and a tab after the scheme", cookie: true, auth: func(f fixtures) []string { return []string{"Bearer\t" + f.token} }, as: webSubject},
	{name: "cookie and an empty value", cookie: true, auth: fixed(""), as: webSubject},
	{name: "cookie and a glued scheme", cookie: true, auth: func(f fixtures) []string { return []string{"Bearerx" + f.token} }, as: webSubject},
	{name: "cookie and a comma after the scheme", cookie: true, auth: func(f fixtures) []string { return []string{"Bearer, " + f.token} }, as: webSubject},
	{name: "cookie and a non ASCII scheme", cookie: true, auth: func(f fixtures) []string { return []string{"Bеarer " + f.token} }, as: webSubject},
	// Bearer requests that authenticate by the token.
	{name: "bearer only", auth: func(f fixtures) []string { return []string{"Bearer " + f.token} }, as: appSubject, bearer: true},
	{name: "lower case bearer", auth: func(f fixtures) []string { return []string{"bearer " + f.token} }, as: appSubject, bearer: true},
	{name: "bearer and cookie", cookie: true, auth: func(f fixtures) []string { return []string{"Bearer " + f.token} }, as: appSubject, bearer: true},
	// Bearer and malformed requests that are refused.
	{name: "junk bearer and cookie", cookie: true, auth: fixed("Bearer example_junk_token"), challenge: challengeInvalid, bearer: true},
	{name: "junk bearer", auth: fixed("Bearer example_junk_token"), challenge: challengeInvalid, bearer: true},
	{name: "empty bearer and cookie", cookie: true, auth: fixed("Bearer"), challenge: challengeNoToken, bearer: true},
	{name: "token with a bad checksum and cookie", cookie: true, auth: func(f fixtures) []string {
		return []string{"Bearer " + f.token[:len(f.token)-1] + flipChar(f.token[len(f.token)-1])}
	}, challenge: challengeInvalid, bearer: true},
	{name: "two headers and cookie", cookie: true, auth: func(f fixtures) []string {
		return []string{"Bearer " + f.token, "Bearer " + f.token}
	}, challenge: challengeInvalid, bearer: true},
	{name: "two headers without cookie", auth: func(f fixtures) []string {
		return []string{"Bearer " + f.token, "Basic dXNlcjpwYXNz"}
	}, challenge: challengeInvalid, bearer: true},
}

func flipChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

// fetchSite is the browser's origin signal: same origin, cross site, or none
// at all, as from a client that is no browser.
type fetchSite string

const (
	sameSite  fetchSite = "same origin"
	crossSite fetchSite = "cross site"
	noBrowser fetchSite = "no browser headers"
)

// shape is the request apart from its credential.
type shape struct {
	method string
	path   string
	site   fetchSite
	csrf   bool
}

func shapes() []shape {
	var out []shape
	for _, s := range []fetchSite{sameSite, crossSite, noBrowser} {
		out = append(out, shape{http.MethodGet, "/notes", s, false})
		for _, withCSRF := range []bool{false, true} {
			out = append(out,
				shape{http.MethodPost, "/notes", s, withCSRF},
				// DELETE is registered nowhere, so the catch all answers it.
				shape{http.MethodDelete, "/notes", s, withCSRF},
				shape{http.MethodPost, "/logout", s, withCSRF})
		}
	}
	return out
}

// TestBearerCSRFContract sends the cross product of credential, method, path,
// origin and CSRF token through the real route chain, CORS, rate limit,
// CSRF, session and handler, and pins every outcome. ADR 0001 is the spec.
// It is the real guard against drift between session and csrf: both packages
// see every header shape here and have to agree on it.
func TestBearerCSRFContract(t *testing.T) {
	t.Parallel()

	for _, cred := range credentials {
		for _, rs := range shapes() {
			name := cred.name + "/" + rs.method + " " + rs.path + "/" + string(rs.site)
			if rs.csrf {
				name += "/csrf token"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				a, clock := newClockedApp(t)
				f := newFixtures(t, a, clock)
				h := a.handler()

				body := ""
				if rs.method == http.MethodPost && rs.path == "/notes" {
					body = `{"text":"contract note"}`
				}
				req := httptest.NewRequestWithContext(t.Context(), rs.method, rs.path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				switch rs.site {
				case sameSite:
					req.Header.Set("Origin", sameOrigin)
					req.Header.Set("Sec-Fetch-Site", "same-origin")
				case crossSite:
					req.Header.Set("Origin", evilOrigin)
					req.Header.Set("Sec-Fetch-Site", "cross-site")
				}
				if rs.csrf {
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

				want := expectedStatus(cred, rs)
				if rec.Code != want {
					t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
				}
				checkCookies(t, a, cred, rs, rec)
				if got := rec.Header().Get("WWW-Authenticate"); got != cred.challenge {
					t.Errorf("WWW-Authenticate = %q, want %q", got, cred.challenge)
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
				if want == http.StatusOK && !strings.HasPrefix(rec.Body.String(), "[") {
					t.Errorf("body = %s, want the subject's notes", rec.Body.String())
				}
			})
		}
	}
}

// checkCookies pins that a bearer request never sets or clears a cookie, and
// that a cookie logout clears the session cookie and nothing else does.
func checkCookies(t *testing.T, a *app, cred credential, rs shape, rec *httptest.ResponseRecorder) {
	t.Helper()
	cookies := rec.Result().Cookies()
	if cred.bearer || rec.Code != http.StatusNoContent || rs.path != "/logout" {
		if len(cookies) != 0 {
			t.Errorf("Set-Cookie = %v, want none", cookies)
		}
		return
	}
	if len(cookies) != 1 || cookies[0].Name != a.sessions.CookieName() || cookies[0].MaxAge >= 0 {
		t.Errorf("Set-Cookie = %v, want the session cookie cleared", cookies)
	}
}

// expectedStatus is the contract, written as rules rather than a table, so
// each line names the rule of ADR 0001 it pins.
func expectedStatus(cred credential, rs shape) int {
	// Every token failure is the same 401, on every method and origin, and
	// never falls back to the cookie.
	if cred.challenge != "" {
		return http.StatusUnauthorized
	}
	unsafe := rs.method != http.MethodGet
	// A request that is not a bearer request keeps both CSRF halves: the
	// origin half refuses a cross site unsafe request, the token half one
	// with a cookie session and without the token.
	if unsafe && !cred.bearer {
		if rs.site == crossSite {
			return http.StatusForbidden
		}
		if cred.as != "" && !rs.csrf {
			return http.StatusForbidden
		}
	}
	// A bearer request skips both halves and authenticates by the token.
	switch {
	case rs.method == http.MethodDelete:
		return http.StatusNotFound
	case rs.path == "/logout":
		return http.StatusNoContent
	case cred.as == "":
		return http.StatusUnauthorized
	case unsafe:
		return http.StatusCreated
	}
	return http.StatusOK
}

// reviewMatrix is the security review's own matrix, written independently of
// expectedStatus, kept as a second opinion on the same chain.
func TestReviewMatrix(t *testing.T) {
	t.Parallel()

	type rshape struct {
		method, path, site string
		token              bool
	}
	rshapes := []rshape{
		{http.MethodGet, "/notes", "same-origin", false},
		{http.MethodPost, "/notes", "same-origin", false},
		{http.MethodPost, "/notes", "same-origin", true},
		{http.MethodPost, "/notes", "cross-site", true},
		{http.MethodDelete, "/notes", "", false},
		{http.MethodPost, "/logout", "cross-site", false},
	}
	for _, tc := range []struct {
		name   string
		cookie bool
		auth   func(f fixtures) []string
		want   [6]int
	}{
		{"neither", false, noAuth, [6]int{401, 401, 401, 403, 404, 403}},
		{"cookie", true, noAuth, [6]int{200, 403, 201, 403, 403, 403}},
		{"cookie+basic", true, fixed("Basic eDp5"), [6]int{200, 403, 201, 403, 403, 403}},
		{"bearer", false, func(f fixtures) []string { return []string{"Bearer " + f.token} }, [6]int{200, 201, 201, 201, 404, 204}},
		{"bearer+cookie", true, func(f fixtures) []string { return []string{"Bearer " + f.token} }, [6]int{200, 201, 201, 201, 404, 204}},
		{"junk+cookie", true, fixed("Bearer junk"), [6]int{401, 401, 401, 401, 401, 401}},
		{"junk", false, fixed("Bearer junk"), [6]int{401, 401, 401, 401, 401, 401}},
		{"two+cookie", true, func(f fixtures) []string { return []string{"Bearer " + f.token, "Bearer " + f.token} }, [6]int{401, 401, 401, 401, 401, 401}},
		{"tab+cookie", true, func(f fixtures) []string { return []string{"Bearer\t" + f.token} }, [6]int{200, 403, 201, 403, 403, 403}},
		{"empty+cookie", true, fixed(""), [6]int{200, 403, 201, 403, 403, 403}},
	} {
		for i, sh := range rshapes {
			t.Run(tc.name+"/"+sh.method+" "+sh.path+" "+sh.site, func(t *testing.T) {
				t.Parallel()
				a, clock := newClockedApp(t)
				f := newFixtures(t, a, clock)
				body := ""
				if sh.method == http.MethodPost && sh.path == "/notes" {
					body = `{"text":"n"}`
				}
				req := httptest.NewRequestWithContext(t.Context(), sh.method, sh.path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				switch sh.site {
				case "same-origin":
					req.Header.Set("Origin", sameOrigin)
					req.Header.Set("Sec-Fetch-Site", "same-origin")
				case "cross-site":
					req.Header.Set("Origin", evilOrigin)
					req.Header.Set("Sec-Fetch-Site", "cross-site")
				}
				if sh.token {
					req.Header.Set(csrf.HeaderName, f.csrfToken)
				}
				if tc.cookie {
					req.AddCookie(f.cookie)
				}
				for _, v := range tc.auth(f) {
					req.Header.Add("Authorization", v)
				}
				rec := httptest.NewRecorder()
				a.handler().ServeHTTP(rec, req)
				if rec.Code != tc.want[i] {
					t.Errorf("status = %d, want %d: %s", rec.Code, tc.want[i], rec.Body.String())
				}
			})
		}
	}
}

// TestNoAuthorizationShapeRidesTheCookie sends more than a hundred
// Authorization shapes with a valid cookie as a cross site POST without a
// CSRF token. None of them may create a note.
func TestNoAuthorizationShapeRidesTheCookie(t *testing.T) {
	t.Parallel()

	schemes := []string{"Bearer", "bearer", "BEARER", "bEaReR", "Basic", "Bearerx", "Bear", "Bеarer", "B\xe9arer", "", "Bearer,"}
	seps := []string{" ", "  ", "\t", "", " \t"}
	toks := []string{"junk", "", "x, Bearer y"}
	var cases [][]string
	for _, s := range schemes {
		for _, sp := range seps {
			for _, tk := range toks {
				cases = append(cases, []string{s + sp + tk})
			}
		}
	}
	cases = append(cases,
		[]string{"Bearer a", "Bearer b"}, []string{"Basic a", "Basic b"},
		[]string{"", ""}, []string{"Basic a", "Bearer b"})
	if len(cases) < 100 {
		t.Fatalf("only %d shapes", len(cases))
	}

	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()
	for _, values := range cases {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/notes", strings.NewReader(`{"text":"n"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", evilOrigin)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.AddCookie(f.cookie)
		for _, v := range values {
			req.Header.Add("Authorization", v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%q: status %d, want 401 or 403", values, rec.Code)
		}
	}
	if n := len(a.notes.list(webSubject)); n != 0 {
		t.Errorf("the cookie subject got %d notes", n)
	}
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

// TestAnAssetForABearerRequestIsNotStored pins that the asset handler's
// immutable cache header does not apply to a bearer request.
func TestAnAssetForABearerRequestIsNotStored(t *testing.T) {
	t.Parallel()
	ts := writeTestSite(t)
	a := newSiteApp(t, ts.dir)
	token, _, err := a.sessions.IssueDevice(t.Context(), appSubject, "phone", time.Now())
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	h := a.handler()

	for _, tc := range []struct {
		name string
		auth http.Header
		want string
	}{
		{"bearer", http.Header{"Authorization": {"Bearer " + token}}, "no-store"},
		{"anonymous", nil, "public, max-age=31536000, immutable"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			res := do(t, h, method, "/assets/app-abc123.js", tc.auth)
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s %s: status = %d", tc.name, method, res.StatusCode)
			}
			if got := res.Header.Get("Cache-Control"); got != tc.want {
				t.Errorf("%s %s: Cache-Control = %q, want %q", tc.name, method, got, tc.want)
			}
		}
	}
}

// TestAFailedBearerIsRefusedOnEveryRoute pins that the 401 does not depend on
// the handler: a public page, the login routes and the catch all refuse it
// too.
func TestAFailedBearerIsRefusedOnEveryRoute(t *testing.T) {
	t.Parallel()
	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()

	for _, path := range []string{"/app", "/notes", "/login", "/login/callback?state=x&code=y", "/this-path-was-never-registered"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.AddCookie(f.cookie)
		req.AddCookie(&http.Cookie{Name: a.loginCodec.LoginCookieName(), Value: "anything"})
		req.Header.Set("Authorization", "Bearer example_junk_token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != challengeInvalid {
			t.Errorf("GET %s = %d %q, want 401 with %s", path, rec.Code, rec.Header().Get("WWW-Authenticate"), challengeInvalid)
		}
		if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
			t.Errorf("GET %s Set-Cookie = %q, want none", path, got)
		}
	}
}

// TestTheLoginCallbackLeavesTheLoginCookieOfABearerRequest pins the callback
// on the codec's login cookie: a valid bearer request neither reads nor
// clears it and gets no session cookie, a cookie request reads and clears it.
func TestTheLoginCallbackLeavesTheLoginCookieOfABearerRequest(t *testing.T) {
	t.Parallel()
	a, clock := newClockedApp(t)
	f := newFixtures(t, a, clock)
	h := a.handler()

	start := httptest.NewRecorder()
	h.ServeHTTP(start, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login?next=/app", nil))
	var login *http.Cookie
	for _, c := range start.Result().Cookies() {
		if c.Name == a.loginCodec.LoginCookieName() {
			login = c
		}
	}
	if login == nil {
		// The identity provider is unreachable in a test, so the start may
		// answer 503 after it set the cookie. Without a cookie the rest
		// cannot run.
		t.Fatalf("GET /login set no login cookie: %d %q", start.Code, start.Header().Values("Set-Cookie"))
	}

	bearer := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/callback?state=x&code=y", nil)
	bearer.AddCookie(login)
	bearer.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearer)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bearer callback status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("bearer callback Set-Cookie = %q, want none", got)
	}

	cookieOnly := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/callback?state=x&code=y", nil)
	cookieOnly.AddCookie(login)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, cookieOnly)
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == a.loginCodec.LoginCookieName() && c.MaxAge < 0 {
			cleared = true
		}
	}
	if rec.Code != http.StatusUnauthorized || !cleared {
		t.Errorf("cookie callback = %d cleared %v, want 401 with the login cookie cleared", rec.Code, cleared)
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
