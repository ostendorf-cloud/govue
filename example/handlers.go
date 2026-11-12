// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/oidc"
	"github.com/ostendorf-cloud/govue/session"
)

// loginCookieName carries the signed Login state between the redirect to the
// identity provider and the callback. It is scoped to /login, so it never
// travels with a request anywhere else in the application.
const loginCookieName = "oidc-login"

// loginCookieLifetime bounds how long a login may take once started. Longer
// than that, the state is gone and the browser starts over.
const loginCookieLifetime = 10 * time.Minute

// note is one item in the example's in-memory store. It exists to give the
// notes handlers something to read and write; it is example data, not a
// feature of this framework.
type note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// noteStore holds the example notes behind a mutex, scoped to the subject
// that owns each one. A real application keeps this in a database instead.
type noteStore struct {
	mu     sync.Mutex
	notes  map[string][]note
	nextID int
}

func (s *noteStore) list(subject string) []note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]note, len(s.notes[subject]))
	copy(out, s.notes[subject])
	return out
}

func (s *noteStore) add(subject, text string) note {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notes == nil {
		s.notes = map[string][]note{}
	}
	s.nextID++
	n := note{ID: strconv.Itoa(s.nextID), Text: text}
	s.notes[subject] = append(s.notes[subject], n)
	return n
}

// requireSession reads the session a request carries, or returns the api
// error a handler can return unchanged: unauthenticated without one, and
// unavailable when the store itself could not answer, so an outage is never
// reported as a logged out user.
func (a *app) requireSession(r *http.Request) (session.Session, error) {
	s, ok, err := a.sessions.Read(r.Context(), r)
	if err != nil {
		return session.Session{}, api.Unavailable("session store is unavailable")
	}
	if !ok {
		return session.Session{}, api.Unauthenticated("log in first")
	}
	return s, nil
}

// listNotes answers the notes of whoever is logged in, scoped to their
// subject: the identity provider's stable account id, never the email or
// username either could still be reassigned to somebody else.
func (a *app) listNotes(w http.ResponseWriter, r *http.Request) error {
	s, err := a.requireSession(r)
	if err != nil {
		return err
	}
	return api.Write(w, http.StatusOK, a.notes.list(s.Subject))
}

// createNoteRequest is the body createNote decodes.
type createNoteRequest struct {
	Text string `json:"text"`
}

// createNote adds a note under the logged in subject. It is an unsafe
// method, so it only runs once the route chain's CORS, rate limit and CSRF
// checks have already passed.
func (a *app) createNote(w http.ResponseWriter, r *http.Request) error {
	s, err := a.requireSession(r)
	if err != nil {
		return err
	}
	var body createNoteRequest
	if err := api.Decode(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Text) == "" {
		return api.Invalid("text must not be empty")
	}
	return api.Write(w, http.StatusCreated, a.notes.add(s.Subject, body.Text))
}

// home renders the one page this example serves that is not JSON. Its
// inline script runs only because the response's Content-Security-Policy
// carries the nonce middleware.Nonce hands back here; without reading it
// from the context, the inline tag would simply not execute under
// middleware.DefaultSecurity's policy.
func (a *app) home(w http.ResponseWriter, r *http.Request) error {
	nonce := middleware.Nonce(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := fmt.Fprintf(w, "<!doctype html><html><head><title>govue example</title></head>"+
		"<body><p>govue example application.</p>"+
		"<script nonce=%q>console.log(\"loaded under the nonce\");</script>"+
		"</body></html>", nonce)
	if err != nil {
		return fmt.Errorf("example: write home page: %w", err)
	}
	return nil
}

// loginStart begins an OIDC login: it mints the login state, signs it into
// the short lived cookie session.Codec exists for, and redirects the browser
// to the provider. next is where the browser returns to after the callback;
// only a same site path is accepted, so this redirect can never be pointed
// off site.
func (a *app) loginStart(w http.ResponseWriter, r *http.Request) error {
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}

	login, err := oidc.NewLogin(next)
	if err != nil {
		return api.Internal(err)
	}
	value, err := a.loginCodec.Encode(login, loginCookieLifetime)
	if err != nil {
		return api.Internal(err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginCookieName,
		Value:    value,
		Path:     "/login",
		MaxAge:   int(loginCookieLifetime.Seconds()),
		HttpOnly: true,
		Secure:   !a.cfg.allowPlainHTTP,
		SameSite: http.SameSiteLaxMode,
	})

	authURL, err := a.oidc.AuthCodeURL(r.Context(), login)
	if err != nil {
		// oidc.Provider already logged the cause; the client only needs to
		// know a retry now will not help.
		return api.Unavailable("the identity provider is not available")
	}
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

// loginCallback finishes the login the provider redirected back from: the
// cookie loginStart set is decoded and cleared first, so a replay of this
// URL finds nothing to act on, the state is checked against it, and the
// code is exchanged for the verified identity.
func (a *app) loginCallback(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(loginCookieName)
	if err != nil {
		return api.Unauthenticated("login expired, please try again")
	}
	var login oidc.Login
	decodeErr := a.loginCodec.Decode(cookie.Value, &login)
	clearLoginCookie(w, a.cfg.allowPlainHTTP)
	if decodeErr != nil {
		return api.Unauthenticated("login expired, please try again")
	}

	if !login.MatchesState(r.URL.Query().Get("state")) {
		return api.Unauthenticated("login does not match this browser")
	}

	identity, err := a.oidc.Exchange(r.Context(), r.URL.Query().Get("code"), login)
	if err != nil {
		return api.Unauthenticated("login could not be completed, please try again")
	}

	if _, err := a.sessions.Start(r.Context(), w, session.Session{
		Subject:  identity.Subject,
		Username: identity.Username,
		Name:     identity.Name,
		Email:    identity.Email,
		Groups:   identity.Groups,
	}); err != nil {
		return api.Internal(err)
	}

	next := login.Next
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
	return nil
}

// clearLoginCookie removes the login-in-flight cookie once it has been read,
// whether it decoded or not: a cookie that failed to decode is just as done
// as one that succeeded.
func clearLoginCookie(w http.ResponseWriter, allowPlainHTTP bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginCookieName,
		Value:    "",
		Path:     "/login",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   !allowPlainHTTP,
		SameSite: http.SameSiteLaxMode,
	})
}

// logout ends the session the request carries. It is an unsafe method, so
// the route chain's CSRF check already ran: a logout a page cannot forge is
// exactly as much a state change as any other.
func (a *app) logout(w http.ResponseWriter, r *http.Request) error {
	if err := a.sessions.End(r.Context(), w, r); err != nil {
		return api.Internal(err)
	}
	api.NoContent(w)
	return nil
}
