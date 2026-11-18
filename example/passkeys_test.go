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
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
)

// The relying party of the test environment: the host of its ALLOWED_ORIGINS.
const (
	passkeyRPID   = "app.example.test"
	passkeyOrigin = "https://app.example.test"
)

func newPasskeyApp(t *testing.T, authBurst int) (*app, *testClock) {
	t.Helper()

	env := testEnv(t, 1000)
	env["AUTH_RATE_LIMIT_BURST"] = strconv.Itoa(authBurst)
	env["ALLOW_SIGNUP"] = "true"
	env["PASSKEY_RP_ID"] = passkeyRPID
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

// passkeyCall sends one same origin JSON POST with any cookies and a CSRF
// token, as a browser on the application's page does.
func (a *app) passkeyCall(t *testing.T, path string, body any, token string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if token != "" {
		r.Header.Set(csrf.HeaderName, token)
	}
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func passkeyChallenge(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == passkey.ChallengeCookieName && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no challenge cookie: %d %s", w.Code, w.Body)
	return nil
}

// signedUp signs an account up and confirms it, and returns the session the
// confirmation started.
func signedUp(t *testing.T, a *app, clock *testClock) *http.Cookie {
	t.Helper()
	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/signup",
		body: map[string]string{"login": acctLogin, "password": acctPassword},
	})
	var signup account.SignupResult
	if err := json.Unmarshal(w.Body.Bytes(), &signup); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("signup = %d %s", w.Code, w.Body)
	}
	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/confirm",
		body: creds(acctLogin, acctPassword, totpNow(t, signup.Enrolment.Secret, clock)),
	})
	c := sessionCookie(a, w)
	if w.Code != http.StatusOK || c == nil {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	return c
}

func TestPasskeyRegisterAndLoginOverHTTP(t *testing.T) {
	t.Parallel()
	a, clock := newPasskeyApp(t, 100)
	first := signedUp(t, a, clock)
	token := tokenOf(t, a, first)

	auth, err := passkeytest.NewAuthenticator(passkeyOrigin, passkeyRPID)
	if err != nil {
		t.Fatal(err)
	}

	// Registration needs the session and its CSRF token, like any unsafe
	// route of a logged in browser.
	if w := a.passkeyCall(t, passkeyPrefix+"/register/begin", map[string]any{}, "", first); w.Code != http.StatusForbidden {
		t.Fatalf("register begin without the CSRF token = %d, want 403", w.Code)
	}
	w := a.passkeyCall(t, passkeyPrefix+"/register/begin", map[string]any{}, token, first)
	if w.Code != http.StatusOK {
		t.Fatalf("register begin = %d %s", w.Code, w.Body)
	}
	challenge := passkeyChallenge(t, w)
	credential, err := auth.Create(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	w = a.passkeyCall(t, passkeyPrefix+"/register/finish",
		map[string]any{"credential": credential, "name": "Laptop"}, token, first, challenge)
	if w.Code != http.StatusCreated {
		t.Fatalf("register finish = %d %s", w.Code, w.Body)
	}

	// Login: no session, no token, a discoverable credential.
	clock.Add(time.Minute)
	w = a.passkeyCall(t, passkeyPrefix+"/login/begin", map[string]any{}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login begin = %d %s", w.Code, w.Body)
	}
	challenge = passkeyChallenge(t, w)
	assertion, err := auth.Get(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	w = a.passkeyCall(t, passkeyPrefix+"/login/finish", map[string]any{"credential": assertion}, "", challenge)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"login":"`+acctLogin+`"}` {
		t.Fatalf("login finish = %d %s", w.Code, w.Body)
	}
	second := sessionCookie(a, w)
	if second == nil || notesStatus(t, a, second) != http.StatusOK {
		t.Fatal("the passkey login did not start a working session")
	}

	// Both logins started the same session for the same account.
	read := func(c *http.Cookie) (string, string) {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.AddCookie(c)
		s, ok, err := a.sessions.Read(t.Context(), r)
		if err != nil || !ok {
			t.Fatalf("read session: %v %t", err, ok)
		}
		return s.Subject, s.Username
	}
	totpSubject, totpUser := read(first)
	passkeySubject, passkeyUser := read(second)
	if totpSubject != passkeySubject || totpUser != passkeyUser || totpUser != acctLogin {
		t.Fatalf("totp session %q %q, passkey session %q %q", totpSubject, totpUser, passkeySubject, passkeyUser)
	}

	// The list sees the passkey. The last one may go: the account keeps its
	// password and TOTP.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, passkeyPrefix, nil)
	r.AddCookie(second)
	lw := httptest.NewRecorder()
	a.handler().ServeHTTP(lw, r)
	var list passkey.ListResult
	if err := json.Unmarshal(lw.Body.Bytes(), &list); err != nil || len(list.Passkeys) != 1 || list.Passkeys[0].Name != "Laptop" {
		t.Fatalf("list = %d %s", lw.Code, lw.Body)
	}
	w = a.passkeyCall(t, passkeyPrefix+"/delete", map[string]any{"id": list.Passkeys[0].ID}, tokenOf(t, a, second), second)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
}

func TestPasskeyRoutesNeedTheAuthRateLimit(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"/register/begin", "/register/finish", "/login/begin", "/login/finish"} {
		a, _ := newPasskeyApp(t, 1)
		a.passkeyCall(t, passkeyPrefix+route, map[string]any{}, "")
		w := a.passkeyCall(t, passkeyPrefix+route, map[string]any{}, "")
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
			t.Fatalf("second call of %s = %d, want 429 with Retry-After", route, w.Code)
		}
	}
}

func TestPasskeysAreOffWithoutRPID(t *testing.T) {
	t.Parallel()
	a, _ := newAccountApp(t, false, 100)
	if w := a.passkeyCall(t, passkeyPrefix+"/login/begin", map[string]any{}, ""); w.Code != http.StatusNotFound {
		t.Fatalf("login begin without PASSKEY_RP_ID = %d, want 404", w.Code)
	}
}

func TestPasskeyConfigRefusedAtStartup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change map[string]string
		want   string
	}{
		{"rp id without allowed origins", map[string]string{"PASSKEY_RP_ID": passkeyRPID, "ALLOWED_ORIGINS": ""}, "needs ALLOWED_ORIGINS"},
		{"rp id not above the origins", map[string]string{"PASSKEY_RP_ID": "other.example.test"}, "not rp id"},
		{"origin over plain http", map[string]string{"PASSKEY_RP_ID": passkeyRPID, "ALLOWED_ORIGINS": "http://app.example.test"}, "not https"},
		{"rp id with a scheme", map[string]string{"PASSKEY_RP_ID": passkeyOrigin}, "rp id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, 1000)
			for k, v := range tt.change {
				env[k] = v
			}
			cfg, err := loadConfig(func(key string) string { return env[key] })
			if err == nil {
				_, err = newApp(cfg, logging.New(&strings.Builder{}, cfg.logLevel))
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error with %q", err, tt.want)
			}
		})
	}
}
