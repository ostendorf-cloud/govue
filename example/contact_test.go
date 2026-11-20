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

	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/pow"
)

// contactTestKey is 32 bytes in hex, built at run time so no literal looks like a credential.
var contactTestKey = strings.Repeat("0a1b", 16)

func newContactApp(t *testing.T, withKey bool) (*app, *testClock, *strings.Builder) {
	t.Helper()
	env := testEnv(t, 1000)
	if withKey {
		env["CONTACT_POW_KEY"] = contactTestKey
	}
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	clock := &testClock{now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}
	cfg.now = clock.Now
	var logs strings.Builder
	a, err := newApp(cfg, logging.New(&logs, cfg.logLevel))
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return a, clock, &logs
}

func contactDo(t *testing.T, a *app, method, path string, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func solveContact(t *testing.T, a *app, clock *testClock) pow.Solution {
	t.Helper()
	w := contactDo(t, a, http.MethodGet, contactPrefix+"/challenge", "", nil)
	var c pow.Challenge
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatalf("challenge = %d %s", w.Code, w.Body)
	}
	clock.Add(5 * time.Second)
	for n := int64(0); n <= c.MaxNumber; n++ {
		if pow.Hash(c.Salt, n) == c.Challenge {
			return pow.Solution{Algorithm: c.Algorithm, Challenge: c.Challenge, Number: n, Salt: c.Salt, Signature: c.Signature}
		}
	}
	t.Fatal("no solution")
	return pow.Solution{}
}

func TestContactFormSendsThroughTheFakeSender(t *testing.T) {
	t.Parallel()
	a, clock, logs := newContactApp(t, true)
	body, err := json.Marshal(map[string]any{
		"name": "Zora Quillfeather", "email": "zora@example.org", "message": "Hello from the test.",
		"pow": solveContact(t, a, clock),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A stray bearer header and a session cookie are not read: the form is
	// stateless and sits outside the session middleware.
	w := contactDo(t, a, http.MethodPost, contactPrefix, string(body), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer not-a-device-token")
	})
	if w.Code != http.StatusOK {
		t.Fatalf("submit = %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("cookie set: %q", got)
	}
	if a.contactSender.n != 1 || a.contactSender.last.ReplyTo != "zora@example.org" {
		t.Fatalf("sender = %d %+v", a.contactSender.n, a.contactSender.last)
	}
	if strings.Contains(logs.String(), "Quillfeather") || strings.Contains(logs.String(), "zora@") {
		t.Fatalf("log holds the content:\n%s", logs)
	}
}

func TestContactFormRefusesACrossSitePost(t *testing.T) {
	t.Parallel()
	a, clock, _ := newContactApp(t, true)
	body, _ := json.Marshal(map[string]any{
		"name": "Zora", "email": "zora@example.org", "message": "Hi", "pow": solveContact(t, a, clock),
	})
	w := contactDo(t, a, http.MethodPost, contactPrefix, string(body), func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	})
	if w.Code != http.StatusForbidden || a.contactSender.n != 0 {
		t.Fatalf("submit = %d %s", w.Code, w.Body)
	}
}

func TestContactFormWithoutKeyAnswers503AndTheProcessStarts(t *testing.T) {
	t.Parallel()
	a, _, _ := newContactApp(t, false)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, contactPrefix + "/challenge", ""},
		{http.MethodPost, contactPrefix, `{"name":"a","email":"a@example.org","message":"m"}`},
	} {
		w := contactDo(t, a, tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "not_configured") {
			t.Fatalf("%s = %d %s", tc.path, w.Code, w.Body)
		}
	}
}
