// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session/devicehttp"
)

const (
	iosOrigin     = "capacitor://localhost"
	androidOrigin = "https://localhost"
	webOrigin     = "https://app.example.test" // the ALLOWED_ORIGINS of testEnv
)

// appEnv is the environment of an app mode deployment: both Capacitor default
// origins, with the custom scheme opted in.
func appEnv() map[string]string {
	return map[string]string{"APP_ORIGINS": iosOrigin + "," + androidOrigin, "APP_SCHEMES": "capacitor"}
}

// newAppModeApp is newAccountApp with extra environment variables.
func newAppModeApp(t *testing.T, burst int, extra map[string]string) (*app, *testClock) {
	t.Helper()

	env := testEnv(t, burst)
	env["AUTH_RATE_LIMIT_BURST"] = "100"
	env["ALLOW_SIGNUP"] = "true"
	for k, v := range extra {
		env[k] = v
	}
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

// tokenFrom reads the device token out of an issue answer.
func tokenFrom(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()

	var res devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Token == "" {
		t.Fatalf("issue body = %s, err %v", w.Body.String(), err)
	}

	return res.Token
}

// preflight sends the OPTIONS request a browser sends before a cross origin
// call with Authorization, through the real chain.
func preflight(t *testing.T, a *app, origin, method, path string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, path, nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Access-Control-Request-Method", method)
	r.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	for k, v := range extra {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)

	return w
}

func hasACHeader(w *httptest.ResponseRecorder) bool {
	for k := range w.Header() {
		if strings.HasPrefix(k, "Access-Control-") {
			return true
		}
	}

	return false
}

// A real preflight from each app origin, through the real chain, is answered
// inside the CORS handler: 204, Authorization allowed, no credentials, and it
// never reaches the rate limiter, the protector or the session middleware.
func TestAppOriginPreflightThroughTheChain(t *testing.T) {
	t.Parallel()
	// A burst of one: a preflight that reached the rate limiter would be
	// refused on its second request.
	a, _ := newAppModeApp(t, 1, appEnv())

	for _, origin := range []string{iosOrigin, androidOrigin} {
		for _, path := range []string{tokenPath, rotatePath, devicesPath, "/notes"} {
			for i := range 3 {
				// A junk bearer header would get a 401 from the session
				// middleware if the preflight reached it.
				w := preflight(t, a, origin, http.MethodPost, path, map[string]string{"Authorization": "Bearer junk"})
				if w.Code != http.StatusNoContent {
					t.Fatalf("%s preflight %s #%d = %d %s, want 204", origin, path, i, w.Code, w.Body.String())
				}
				h := w.Header()
				if h.Get("Access-Control-Allow-Origin") != origin {
					t.Errorf("allow origin = %q, want %q", h.Get("Access-Control-Allow-Origin"), origin)
				}
				if got := h.Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
					t.Errorf("allow headers = %q", got)
				}
				if got := h.Get("Access-Control-Allow-Methods"); got != "GET, POST, DELETE" {
					t.Errorf("allow methods = %q", got)
				}
				if got := h.Get("Access-Control-Max-Age"); got != "600" {
					t.Errorf("max age = %q", got)
				}
				if _, ok := h["Access-Control-Allow-Credentials"]; ok {
					t.Error("an app origin was sent Access-Control-Allow-Credentials")
				}
				if len(w.Result().Cookies()) != 0 {
					t.Errorf("a preflight set a cookie: %v", w.Result().Cookies())
				}
				if !strings.Contains(strings.Join(h.Values("Vary"), ","), "Origin") {
					t.Errorf("Vary = %v", h.Values("Vary"))
				}
			}
		}
	}
}

// A real Issue request from each app origin through the real chain: it
// succeeds, the answer is readable cross origin without credentials, and the
// token it carries works.
func TestAppOriginIssueThroughTheChain(t *testing.T) {
	t.Parallel()
	a, clock := newAppModeApp(t, 1000, appEnv())
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)

	for _, origin := range []string{iosOrigin, androidOrigin} {
		clock.Add(time.Minute)
		body := creds(acctLogin, acctPassword, totpNow(t, secret, clock))
		body["device_name"] = "phone " + origin
		w := a.serve(t, accountCall{method: http.MethodPost, path: tokenPath, body: body, site: "cross-site", origin: origin})
		if w.Code != http.StatusCreated {
			t.Fatalf("issue from %s = %d %s, want 201", origin, w.Code, w.Body.String())
		}
		h := w.Header()
		if h.Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("allow origin = %q, want %q", h.Get("Access-Control-Allow-Origin"), origin)
		}
		if _, ok := h["Access-Control-Allow-Credentials"]; ok {
			t.Error("the issue answer carries Access-Control-Allow-Credentials")
		}
		if h.Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control = %q", h.Get("Cache-Control"))
		}
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("issue set a cookie: %v", w.Result().Cookies())
		}
		if !strings.Contains(strings.Join(h.Values("Vary"), ","), "Origin") {
			t.Errorf("Vary = %v", h.Values("Vary"))
		}

		// The token works, from the same origin, without a CSRF token.
		token := tokenFrom(t, w)
		w = a.serve(t, accountCall{
			method: http.MethodGet, path: "/notes", authorization: bearerHeader(token), site: "cross-site", origin: origin,
		})
		if w.Code != http.StatusOK {
			t.Errorf("GET /notes with the token from %s = %d, want 200", origin, w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("a bearer answer is not readable from %s", origin)
		}
	}
}

// The app origin is no web origin. A cookie request from it is refused on the
// routes that change state, whatever token it carries, and an answer to a safe
// request from it never allows credentials, so a browser does not expose it.
func TestAppOriginIsNoWebOrigin(t *testing.T) {
	t.Parallel()
	a, clock := newAppModeApp(t, 1000, appEnv())
	_, cookie := newAccount(t, a, clock, acctLogin, acctPassword)
	token := tokenOf(t, a, cookie)

	for _, origin := range []string{iosOrigin, androidOrigin} {
		for _, rt := range []struct{ method, path string }{
			{http.MethodPost, "/notes"},
			{http.MethodPost, "/logout"},
			{http.MethodPost, accountPrefix + "/password"},
			{http.MethodDelete, authPrefix + "/devices/abc"},
			{http.MethodDelete, authPrefix + "/token"},
			{http.MethodPost, authPrefix + "/token/rotate"},
			{http.MethodPost, "/no-such-route"},
		} {
			w := a.serve(t, accountCall{
				method: rt.method, path: rt.path, cookie: cookie, token: token,
				site: "cross-site", origin: origin, body: map[string]string{"text": "x"},
			})
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s with a cookie and a valid CSRF token from %s = %d, want 403", rt.method, rt.path, origin, w.Code)
			}
		}

		// The issue route is exempt from the origin half only: a request with
		// a session cookie and no CSRF token is refused.
		w := a.serve(t, accountCall{
			method: http.MethodPost, path: tokenPath, cookie: cookie, site: "cross-site", origin: origin,
			body: creds(acctLogin, acctPassword, "000000"),
		})
		if w.Code != http.StatusForbidden {
			t.Errorf("issue with a cookie and no CSRF token from %s = %d, want 403", origin, w.Code)
		}

		// A safe request passes the protector as for any origin. The answer
		// allows no credentials, so the browser keeps it from the page.
		w = a.serve(t, accountCall{method: http.MethodGet, path: "/notes", cookie: cookie, site: "cross-site", origin: origin})
		if _, ok := w.Header()["Access-Control-Allow-Credentials"]; ok {
			t.Errorf("a cookie GET answer for %s allows credentials", origin)
		}
	}
}

// The issue route trusts the app origins and no other cross site origin: the
// protector refuses an unlisted origin itself, before the handler's own check,
// which stays a second layer. The message of the protector tells the two apart.
func TestIssueFromAnUnlistedOriginIsRefusedByTheProtector(t *testing.T) {
	t.Parallel()
	a, clock := newAppModeApp(t, 1000, appEnv())
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)

	for _, origin := range []string{evilOrigin, webOrigin, "capacitor://other", "https://localhost:8443", "null"} {
		clock.Add(time.Minute)
		w := a.serve(t, accountCall{
			method: http.MethodPost, path: tokenPath, site: "cross-site", origin: origin,
			body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
		})
		if w.Code != http.StatusForbidden {
			t.Errorf("issue from %q = %d %s, want 403", origin, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "cross origin or stale request") {
			t.Errorf("issue from %q was refused by %s, want the protector", origin, w.Body.String())
		}
		if hasACHeader(w) && origin != webOrigin {
			t.Errorf("issue from %q got an Access-Control header: %v", origin, w.Header())
		}
	}
}

// Another spelling of the issue path does not get the trust of the route: the
// protector matches the escaped path and refuses an app origin on it, whatever
// the mux makes of the spelling.
func TestIssueRouteTrustIsKeyedOnTheEscapedPath(t *testing.T) {
	t.Parallel()
	a, clock := newAppModeApp(t, 1000, appEnv())
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)

	for _, path := range []string{
		authPrefix + "%2Ftoken", authPrefix + "%2ftoken", authPrefix + "/%74oken", "/API/V1/AUTH/TOKEN",
	} {
		clock.Add(time.Minute)
		w := a.serve(t, accountCall{
			method: http.MethodPost, path: path, site: "cross-site", origin: iosOrigin,
			body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
		})
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cross origin or stale request") {
			t.Errorf("POST %s from an app origin = %d %s, want the 403 of the protector", path, w.Code, w.Body.String())
		}
	}
}

// The web policy is unchanged: its origin keeps credentials and the CSRF
// header, gets no Authorization, and an unlisted origin gets nothing. An app
// origin gets none of the web policy.
func TestWebCORSIsUnchangedByAppMode(t *testing.T) {
	t.Parallel()
	a, _ := newAppModeApp(t, 1000, appEnv())

	w := preflight(t, a, webOrigin, http.MethodPost, "/notes", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("web preflight = %d, want 204", w.Code)
	}
	h := w.Header()
	if h.Get("Access-Control-Allow-Origin") != webOrigin || h.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("web preflight headers = %v", h)
	}
	if got := h.Get("Access-Control-Allow-Headers"); got != "Content-Type, "+csrf.HeaderName {
		t.Errorf("web allow headers = %q", got)
	}

	for _, origin := range []string{evilOrigin, "null", "capacitor://other", "https://localhost:8443"} {
		w := preflight(t, a, origin, http.MethodPost, "/notes", nil)
		if w.Code != http.StatusForbidden || hasACHeader(w) {
			t.Errorf("preflight from %q = %d with headers %v, want 403 and none", origin, w.Code, w.Header())
		}
	}

	// An app origin does not become a web origin: a web preflight header set
	// never carries credentials for it, and the web origin gets no app policy.
	w = a.serve(t, accountCall{method: http.MethodGet, path: "/notes", site: "cross-site", origin: webOrigin})
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("the web origin lost its credentials")
	}
	if strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Error("the web policy allows Authorization")
	}
}

// App mode is off by default: without APP_ORIGINS an app origin is any other
// cross site origin.
func TestAppModeIsOffByDefault(t *testing.T) {
	t.Parallel()
	a, clock := newAppModeApp(t, 1000, nil)
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)

	w := preflight(t, a, iosOrigin, http.MethodPost, tokenPath, nil)
	if w.Code != http.StatusForbidden || hasACHeader(w) {
		t.Errorf("preflight = %d, headers %v, want 403 and none", w.Code, w.Header())
	}

	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: tokenPath, site: "cross-site", origin: iosOrigin,
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("issue from an app origin without APP_ORIGINS = %d, want 403", w.Code)
	}
}

// The web list is not the app list: ALLOWED_ORIGINS never reaches Issue.
func TestWebOriginsNeverReachTheAppList(t *testing.T) {
	t.Parallel()
	a, clock := newAppModeApp(t, 1000, appEnv())
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)

	if got := a.appOrigins.List(); len(got) != 2 || got[0] != iosOrigin || got[1] != androidOrigin {
		t.Errorf("app origins = %v", got)
	}
	if a.appOrigins.Contains(webOrigin) {
		t.Error("the web origin is on the app list")
	}

	clock.Add(time.Minute)
	w := a.serve(t, accountCall{
		method: http.MethodPost, path: tokenPath, site: "cross-site", origin: webOrigin,
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("issue from the web origin = %d, want 403", w.Code)
	}
}

// Startup refuses a configuration that mixes the lists, and names the origin.
func TestAppOriginsStartupErrors(t *testing.T) {
	t.Parallel()

	const label = "APP_ORIGINS and APP_SCHEMES: middleware: "
	for _, tc := range []struct {
		name  string
		env   map[string]string
		named string
		// accepted is true when the error names an accepted value on
		// purpose: an overlap or a duplicate. Every other case refuses the
		// value, and it must not appear.
		accepted bool
	}{
		{"an overlap with the web list", map[string]string{"APP_ORIGINS": webOrigin}, webOrigin, true},
		{
			"an overlap in another spelling",
			map[string]string{"APP_ORIGINS": androidOrigin, "ALLOWED_ORIGINS": "HTTPS://Localhost/"},
			androidOrigin, true,
		},
		{"a duplicate", map[string]string{"APP_ORIGINS": androidOrigin + "," + androidOrigin}, androidOrigin, true},
		// A refused value is never shown: the error names the variable, the
		// position and the rule.
		{"a wildcard", map[string]string{"APP_ORIGINS": "https://*.example.test"}, label + "app origin 1 holds a wildcard", false},
		{"null", map[string]string{"APP_ORIGINS": "null"}, label + "app origin 1 is the null origin", false},
		{"a path", map[string]string{"APP_ORIGINS": "https://app2.example.test/x"}, label + "app origin 1 must be lower case", false},
		{"a custom scheme not opted in", map[string]string{"APP_ORIGINS": iosOrigin}, label + "app origin 1 has a scheme that is neither https", false},
		{"http", map[string]string{"APP_ORIGINS": "http://localhost"}, label + "app origin 1 has a scheme that is neither https", false},
		{"the default https port", map[string]string{"APP_ORIGINS": "https://app2.example.test:443"}, label + "app origin 1 names the default port 443", false},
		{"a Unicode host", map[string]string{"APP_ORIGINS": "https://аpple.com"}, label + "app origin 1 has a host that is not ASCII", false},
		{"a token as a scheme", map[string]string{"APP_ORIGINS": androidOrigin, "APP_SCHEMES": "S3cr3tTOKEN"}, label + "app scheme 1 must be", false},
		{"a scheme without an origin", map[string]string{"APP_SCHEMES": "capacitor"}, "APP_SCHEMES", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := testEnv(t, 10)
			for k, v := range tc.env {
				env[k] = v
			}
			cfg, err := loadConfig(func(key string) string { return env[key] })
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			_, err = newApp(cfg, logging.New(&strings.Builder{}, cfg.logLevel))
			if err == nil {
				t.Fatal("newApp accepted the configuration")
			}
			if !strings.Contains(err.Error(), tc.named) {
				t.Errorf("error %q does not name %q", err, tc.named)
			}
			if tc.accepted {
				return
			}
			// Every refused value, raw and quoted, stays out of the error,
			// unless it is a word of the rule itself, such as null.
			for _, key := range []string{"APP_ORIGINS", "APP_SCHEMES"} {
				for _, value := range strings.Split(tc.env[key], ",") {
					if value == "" || strings.Contains(tc.named, value) {
						continue
					}
					if strings.Contains(err.Error(), value) {
						t.Errorf("error %q shows the refused value %q", err, value)
					}
				}
			}
		})
	}
}

// The preflight of an app origin carries nothing a rate limit could count: it
// stays 204 after the burst is spent by real requests.
// A page of an app origin reads the wait of a 429 only when Retry-After is
// exposed. The request goes through the full chain.
func TestAppRoutesExposeRetryAfterOnA429(t *testing.T) {
	t.Parallel()
	a, _ := newAppModeApp(t, 1, appEnv())
	do := func(origin string) *httptest.ResponseRecorder {
		return a.serve(t, accountCall{method: http.MethodGet, path: "/notes", site: "cross-site", origin: origin})
	}

	if w := do(iosOrigin); w.Code == http.StatusTooManyRequests {
		t.Fatalf("first request = %d, want no 429", w.Code)
	}
	w := do(iosOrigin)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("second request = %d, Retry-After %q, want 429 with one", w.Code, w.Header().Get("Retry-After"))
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); got != "Retry-After" {
		t.Errorf("expose headers = %q, want Retry-After", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != iosOrigin {
		t.Errorf("allow origin = %q, want %q", got, iosOrigin)
	}

	// A preflight is not rate limited and carries no expose header.
	if p := preflight(t, a, iosOrigin, http.MethodGet, "/notes", nil); p.Header().Get("Access-Control-Expose-Headers") != "" {
		t.Errorf("preflight carries expose headers: %v", p.Header())
	}
}

func TestAppOriginPreflightIsNotRateLimited(t *testing.T) {
	t.Parallel()
	a, _ := newAppModeApp(t, 1, appEnv())

	for i := range 20 {
		if w := preflight(t, a, iosOrigin, http.MethodDelete, tokenPath, nil); w.Code != http.StatusNoContent {
			t.Fatalf("preflight #%d = %d, want 204", i, w.Code)
		}
	}
}
