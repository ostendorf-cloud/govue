// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/session"
)

// newDevicelessApp is the example chain, ExemptBearer on, with a session
// manager that has no device store: a product whose own gateway or JWT uses
// the Authorization header. It returns a cookie session and its CSRF token.
func newDevicelessApp(t *testing.T) (*app, *http.Cookie, string) {
	t.Helper()
	a, clock := newClockedApp(t)
	m, err := session.New(session.Config{Store: session.NewMemoryStore(clock.Now, 100), Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	// The protector's Expected reads a.sessions, so it follows the swap.
	a.sessions = m
	rec := httptest.NewRecorder()
	s, err := m.Start(t.Context(), rec, session.Session{Subject: webSubject})
	if err != nil {
		t.Fatal(err)
	}
	return a, rec.Result().Cookies()[0], csrf.Token(a.cfg.csrfKey, s.ID)
}

// TestDevicelessMatrix pins the chain without a device store: a bearer or
// malformed request is anonymous, never refused with a challenge, and never
// rides the cookie. Every other request is a cookie request as before.
func TestDevicelessMatrix(t *testing.T) {
	t.Parallel()

	shapes := []struct {
		method, path, site string
		token              bool
	}{
		{http.MethodGet, "/notes", "same-origin", false},
		{http.MethodPost, "/notes", "same-origin", false},
		{http.MethodPost, "/notes", "same-origin", true},
		{http.MethodPost, "/notes", "cross-site", true},
		{http.MethodDelete, "/notes", "", false},
		{http.MethodPost, "/logout", "cross-site", false},
	}
	cookieRow := [6]int{200, 403, 201, 403, 403, 403}
	// Anonymous: no session for the notes, the catch all for DELETE, and a
	// clean 401 for a logout that has no device to sign out.
	anonRow := [6]int{401, 401, 401, 401, 404, 401}
	for _, tc := range []struct {
		name   string
		cookie bool
		auth   []string
		bearer bool
		want   [6]int
	}{
		{"neither", false, nil, false, [6]int{401, 401, 401, 403, 404, 403}},
		{"cookie", true, nil, false, cookieRow},
		{"cookie and basic", true, []string{"Basic eDp5"}, false, cookieRow},
		{"bearer", false, []string{"Bearer a-gateway-token"}, true, anonRow},
		{"bearer and cookie", true, []string{"Bearer a-gateway-token"}, true, anonRow},
		{"empty bearer and cookie", true, []string{"Bearer"}, true, anonRow},
		{"two headers and cookie", true, []string{"Bearer a", "Bearer b"}, true, anonRow},
		{"tab and cookie", true, []string{"Bearer\tx"}, false, cookieRow},
		{"empty value and cookie", true, []string{""}, false, cookieRow},
	} {
		for i, sh := range shapes {
			t.Run(tc.name+"/"+sh.method+" "+sh.path+" "+sh.site, func(t *testing.T) {
				t.Parallel()
				a, cookie, token := newDevicelessApp(t)
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
					req.Header.Set(csrf.HeaderName, token)
				}
				if tc.cookie {
					req.AddCookie(cookie)
				}
				for _, v := range tc.auth {
					req.Header.Add("Authorization", v)
				}
				rec := httptest.NewRecorder()
				a.handler().ServeHTTP(rec, req)
				if rec.Code != tc.want[i] {
					t.Errorf("status = %d, want %d: %s", rec.Code, tc.want[i], rec.Body.String())
				}
				if !tc.bearer {
					return
				}
				if n := len(a.notes.list(webSubject)); n != 0 {
					t.Error("the cookie subject got a note on a bearer request")
				}
				if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
					t.Errorf("Set-Cookie = %q, want none", got)
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q, want no-store", got)
				}
				if got := rec.Header().Get("WWW-Authenticate"); got != "" {
					t.Errorf("WWW-Authenticate = %q, want none without a device store", got)
				}
			})
		}
	}
}
