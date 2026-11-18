// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/session"
)

func signupSecret(t *testing.T, body []byte) string {
	t.Helper()

	var signup account.SignupResult
	if err := json.Unmarshal(body, &signup); err != nil || signup.Enrolment.Secret == "" {
		t.Fatalf("signup body = %s, err %v", body, err)
	}

	return signup.Enrolment.Secret
}

// enrol signs login up, confirms the secret with the cookie the request
// already carries, and returns the secret and the cookie the confirmation set.
func enrol(t *testing.T, a *app, clock *testClock, login string, carrying *http.Cookie) *http.Cookie {
	t.Helper()

	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/signup",
		body: map[string]string{"login": login, "password": acctPassword},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("signup %s = %d %s, want 201", login, w.Code, w.Body.String())
	}
	secret := signupSecret(t, w.Body.Bytes())

	clock.Add(time.Minute)
	// A request that carries a session needs its CSRF token, login or not.
	var token string
	if carrying != nil {
		token = tokenOf(t, a, carrying)
	}
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/confirm", cookie: carrying, token: token,
		body: creds(login, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("confirm %s = %d %s, want 200", login, w.Code, w.Body.String())
	}

	return sessionCookie(a, w)
}

// A login does not end the session the request already carries. Logging in as
// B while logged in as A must not leave A alive.
func TestAccountsALoginEndsTheSessionTheRequestCarried(t *testing.T) {
	t.Parallel()
	a, clock := newAccountApp(t, true, 100)

	cookieA := enrol(t, a, clock, "alice@example.com", nil)
	if cookieA == nil || notesStatus(t, a, cookieA) != http.StatusOK {
		t.Fatal("the first account has no working session")
	}

	cookieB := enrol(t, a, clock, "bob@example.com", cookieA)
	if cookieB == nil || notesStatus(t, a, cookieB) != http.StatusOK {
		t.Fatal("the second account has no working session")
	}
	if got := notesStatus(t, a, cookieA); got != http.StatusUnauthorized {
		t.Fatalf("the first session after a login as another account = %d, want 401", got)
	}
}

// failingStore answers every read with an error, as a session store that is
// down does.
type failingStore struct{ session.Store }

func (failingStore) Get(context.Context, string) (session.Session, error) {
	return session.Session{}, errors.New("session store is down")
}

// A session store that cannot answer is a 503, never a logged out person.
func TestAccountsPasswordChangeAnswers503WhenTheSessionStoreFails(t *testing.T) {
	t.Parallel()
	a, _ := newAccountApp(t, true, 100)

	broken, err := session.New(session.Config{
		Store:          failingStore{Store: session.NewMemoryStore(a.cfg.now, 10)},
		AllowPlainHTTP: a.cfg.allowPlainHTTP,
		Now:            a.cfg.now,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	a.sessions = broken

	id, err := session.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/password",
		cookie: &http.Cookie{Name: broken.CookieName(), Value: id},
		body:   map[string]string{"current_password": acctPassword, "new_password": acctNew},
	})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("password change = %d %s, want 503", w.Code, w.Body.String())
	}
}

// The handlers trust the same cross origin callers as the CSRF protector.
func TestAccountsTrustTheConfiguredOrigins(t *testing.T) {
	t.Parallel()
	a, _ := newAccountApp(t, true, 100)

	login := func(origin string) int {
		return a.serve(t, accountCall{
			method: http.MethodPost, path: accountPrefix + "/login", site: "-", origin: origin,
			body: creds("nobody@example.com", acctPassword, "000000"),
		}).Code
	}

	// The configured origin reaches the service, which refuses the guess.
	if got := login(sameOrigin); got != http.StatusUnauthorized {
		t.Fatalf("login from the allowed origin = %d, want 401", got)
	}
	if got := login(evilOrigin); got != http.StatusForbidden {
		t.Fatalf("login from another origin = %d, want 403", got)
	}
}
