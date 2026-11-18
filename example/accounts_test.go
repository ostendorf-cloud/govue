// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/logging"
)

// Fake values only. None of them is anybody's password or login.
const (
	acctLogin    = "carol@example.com"
	acctPassword = "fake-example-password-one"
	acctNew      = "fake-example-password-two"
)

// newAccountApp is an app on a clock the test controls, with signup open when
// asked and the given burst for the routes that check a password.
func newAccountApp(t *testing.T, signup bool, authBurst int) (*app, *testClock) {
	t.Helper()

	env := testEnv(t, 1000)
	env["AUTH_RATE_LIMIT_BURST"] = strconv.Itoa(authBurst)
	if signup {
		env["ALLOW_SIGNUP"] = "true"
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

// accountCall is one request to the app's handler, as a browser on the same
// origin sends it.
type accountCall struct {
	method string
	path   string
	body   any
	cookie *http.Cookie
	token  string
	// site is the Sec-Fetch-Site header, same-origin when empty and left out
	// when it is "-".
	site string
	// origin is the Origin header, left out when empty.
	origin string
	// authorization is the Authorization header, left out when empty.
	authorization string
}

func (a *app) serve(t *testing.T, c accountCall) *httptest.ResponseRecorder {
	t.Helper()

	var body string
	if c.body != nil {
		raw, err := json.Marshal(c.body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body = string(raw)
	}
	r := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(body))
	if c.body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	site := c.site
	if site == "" {
		site = "same-origin"
	}
	if site != "-" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	if c.authorization != "" {
		r.Header.Set("Authorization", c.authorization)
	}
	if c.origin != "" {
		r.Header.Set("Origin", c.origin)
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	if c.token != "" {
		r.Header.Set(csrf.HeaderName, c.token)
	}

	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)

	return w
}

// sessionCookie returns the session cookie an answer set, or nil.
func sessionCookie(a *app, w *httptest.ResponseRecorder) *http.Cookie {
	// The last Set-Cookie of the name is the one a browser keeps. A clearing
	// cookie after the live one would delete the new session.
	var last *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == a.sessions.CookieName() {
			last = c
		}
	}
	if last == nil || last.Value == "" {
		return nil
	}

	return last
}

// tokenOf is the CSRF token of the session a cookie carries.
func tokenOf(t *testing.T, a *app, c *http.Cookie) string {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.AddCookie(c)
	s, ok, err := a.sessions.Read(t.Context(), r)
	if err != nil || !ok {
		t.Fatalf("read session: %v, found %t", err, ok)
	}

	return csrf.Token(a.cfg.csrfKey, s.ID)
}

func totpNow(t *testing.T, secret string, clock *testClock) string {
	t.Helper()

	code, err := account.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}

	return code
}

func creds(login, password, code string) map[string]string {
	return map[string]string{"login": login, "password": password, "code": code}
}

// notesStatus is what the session behind a cookie gets from a route that needs
// one.
func notesStatus(t *testing.T, a *app, c *http.Cookie) int {
	t.Helper()

	return a.serve(t, accountCall{method: http.MethodGet, path: "/notes", cookie: c}).Code
}

func TestAccountsSignupConfirmLoginAndPasswordChangeOverHTTP(t *testing.T) {
	t.Parallel()
	a, clock := newAccountApp(t, true, 100)

	// Signup: no session yet, and the secret is shown once.
	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/signup",
		body: map[string]string{"login": acctLogin, "password": acctPassword},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("signup = %d %s, want 201", w.Code, w.Body.String())
	}
	if sessionCookie(a, w) != nil {
		t.Fatal("signup set a session cookie")
	}
	var signup account.SignupResult
	if err := json.Unmarshal(w.Body.Bytes(), &signup); err != nil || signup.Enrolment.Secret == "" {
		t.Fatalf("signup body = %s, err %v", w.Body.String(), err)
	}
	secret := signup.Enrolment.Secret

	// Login is refused until the secret is confirmed.
	if w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/login",
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	}); w.Code != http.StatusUnauthorized {
		t.Fatalf("login before the confirmation = %d, want 401", w.Code)
	}

	// Confirmation proves both factors and starts the first session.
	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/confirm",
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s, want 200", w.Code, w.Body.String())
	}
	first := sessionCookie(a, w)
	if first == nil || notesStatus(t, a, first) != http.StatusOK {
		t.Fatal("confirm did not start a working session")
	}

	// Login with the next code. A second session, as a second device has.
	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/login",
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d %s, want 200", w.Code, w.Body.String())
	}
	second := sessionCookie(a, w)
	if second == nil || notesStatus(t, a, second) != http.StatusOK {
		t.Fatal("login did not start a working session")
	}

	// A password change needs the session's CSRF token like any unsafe route.
	change := map[string]string{"current_password": acctPassword, "new_password": acctNew}
	if w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/password",
		body: change, cookie: second,
	}); w.Code != http.StatusForbidden {
		t.Fatalf("password change without the token = %d, want 403", w.Code)
	}
	if w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/password",
		body: change,
	}); w.Code != http.StatusUnauthorized {
		t.Fatalf("password change without a session = %d, want 401", w.Code)
	}
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/password",
		body: change, cookie: second, token: tokenOf(t, a, second),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("password change = %d %s, want 200", w.Code, w.Body.String())
	}
	var changed account.PasswordChangeResult
	if err := json.Unmarshal(w.Body.Bytes(), &changed); err != nil || !changed.PasswordChanged || !changed.SessionsEnded {
		t.Fatalf("password change body = %s, want changed and every session ended", w.Body.String())
	}

	// Every session of the account is gone, the caller's own included.
	for name, c := range map[string]*http.Cookie{"first": first, "second": second} {
		if got := notesStatus(t, a, c); got != http.StatusUnauthorized {
			t.Fatalf("%s session after the change = %d, want 401", name, got)
		}
	}

	// The old password is dead and the new one logs in.
	clock.Add(time.Minute)
	if w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/login",
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	}); w.Code != http.StatusUnauthorized {
		t.Fatalf("login with the old password = %d, want 401", w.Code)
	}
	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/login",
		body: creds(acctLogin, acctNew, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login with the new password = %d %s, want 200", w.Code, w.Body.String())
	}
	third := sessionCookie(a, w)

	// Logout ends this session only, and needs the token as well.
	if w := a.serve(t, accountCall{method: http.MethodPost, path: accountPrefix + "/logout", cookie: third}); w.Code != http.StatusForbidden {
		t.Fatalf("logout without the token = %d, want 403", w.Code)
	}
	if w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/logout", cookie: third,
		token: tokenOf(t, a, third),
	}); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", w.Code)
	}
	if got := notesStatus(t, a, third); got != http.StatusUnauthorized {
		t.Fatalf("session after logout = %d, want 401", got)
	}
}

func TestAccountsRefuseACrossSiteLoginWithoutASession(t *testing.T) {
	t.Parallel()
	a, _ := newAccountApp(t, true, 100)

	for _, path := range []string{"/signup", "/confirm", "/login", "/password", "/logout"} {
		w := a.serve(t, accountCall{
			method: http.MethodPost, path: accountPrefix + path, site: "cross-site",
			body: creds(acctLogin, acctPassword, "000000"),
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("cross site %s = %d %s, want 403", path, w.Code, w.Body.String())
		}
	}
}

func TestAccountsSignupIsNotMountedUnlessTheApplicationOpensIt(t *testing.T) {
	t.Parallel()
	a, _ := newAccountApp(t, false, 100)

	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/signup",
		body: map[string]string{"login": acctLogin, "password": acctPassword},
	})
	if w.Code == http.StatusCreated || w.Code == http.StatusOK {
		t.Fatalf("closed signup = %d, want a refusal", w.Code)
	}
}

func TestAccountsRateLimitLoginAndSignup(t *testing.T) {
	t.Parallel()

	t.Run("login", func(t *testing.T) {
		t.Parallel()
		a, _ := newAccountApp(t, true, 2)

		call := accountCall{method: http.MethodPost, path: accountPrefix + "/login", body: creds("nobody@example.com", acctPassword, "000000")}
		for i := range 2 {
			if w := a.serve(t, call); w.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d = %d, want 401", i+1, w.Code)
			}
		}
		w := a.serve(t, call)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
			t.Fatalf("attempt 3 = %d, Retry-After %q, want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
		}
	})

	t.Run("signup", func(t *testing.T) {
		t.Parallel()
		a, _ := newAccountApp(t, true, 1)

		first := accountCall{method: http.MethodPost, path: accountPrefix + "/signup", body: map[string]string{"login": acctLogin, "password": acctPassword}}
		if w := a.serve(t, first); w.Code != http.StatusCreated {
			t.Fatalf("first signup = %d, want 201", w.Code)
		}
		second := accountCall{method: http.MethodPost, path: accountPrefix + "/signup", body: map[string]string{"login": "dave@example.com", "password": acctPassword}}
		if w := a.serve(t, second); w.Code != http.StatusTooManyRequests {
			t.Fatalf("second signup = %d, want 429", w.Code)
		}
	})

	// Confirm and password change check a password too. Each is mounted
	// behind the stricter limit, and the limit answers before the session
	// or the body is looked at.
	for _, path := range []string{"/confirm", "/password"} {
		t.Run(path[1:], func(t *testing.T) {
			t.Parallel()
			a, _ := newAccountApp(t, true, 1)

			call := accountCall{method: http.MethodPost, path: accountPrefix + path, body: map[string]string{}}
			if w := a.serve(t, call); w.Code == http.StatusTooManyRequests {
				t.Fatalf("first request = 429, want it to reach the handler")
			}
			w := a.serve(t, call)
			if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
				t.Fatalf("second request = %d, Retry-After %q, want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
			}
		})
	}

	t.Run("the general routes are not held by the stricter limit", func(t *testing.T) {
		t.Parallel()
		a, _ := newAccountApp(t, true, 1)

		for range 3 {
			if w := a.serve(t, accountCall{method: http.MethodGet, path: "/notes"}); w.Code != http.StatusUnauthorized {
				t.Fatalf("notes = %d, want 401", w.Code)
			}
		}
	})
}
