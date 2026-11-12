// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"net/http"
)

// contextKey is unexported and a struct type, so nothing outside this package
// can put a value under it or read one out by guessing the key.
type contextKey struct{}

// outcome is the whole result of one [Manager.Read], kept as a single context
// value so that the session and the store error cannot be written or read
// apart from one another.
type outcome struct {
	session Session
	ok      bool
	err     error
}

// Middleware reads the session the request carries and puts the outcome into
// the request context, where [FromContext] and [ErrorFromContext] find it. It
// composes with middleware.Chain: the signature is the one csrf.Protector's
// middleware and that package's own middlewares have.
//
// [Manager.Read] runs exactly once per request however many handlers ask for
// the session afterwards, which is the point: reading it per handler means one
// store round trip per read, and a read can also write the last use back.
//
// The value is the session as it was at the start of the request. A handler
// that calls [Manager.Start], [Manager.Rotate] or [Manager.End] changes the
// session, not the context, so [FromContext] keeps returning the value from
// before the change for the rest of that request. The handler already holds
// what those methods returned.
//
// This middleware refuses nothing and writes no response. It is not an
// authorisation gate, and making it one would be wrong twice over: a public
// route behind the same chain would start answering 503 because of a middleware
// whose only job was to read a cookie, and the gate belongs where the
// application knows what a route requires. Nothing is lost by that, because
// [FromContext] reports false for a store that could not answer, so an
// application's own check is fail closed whether it reads the error or not.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok, err := m.Read(r.Context(), r)
		ctx := context.WithValue(r.Context(), contextKey{}, outcome{session: s, ok: ok, err: err})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromContext returns the session [Manager.Middleware] read, and reports
// whether the request carries one at all.
//
// False covers a request without a cookie, a session past either timeout, a
// store that could not answer, and a request the middleware never ran on. That
// last one is deliberate: a handler mounted without the middleware looks logged
// out rather than looking logged in as somebody, so forgetting to mount it
// fails closed.
func FromContext(ctx context.Context) (Session, bool) {
	got, ok := ctx.Value(contextKey{}).(outcome)
	if !ok {
		return Session{}, false
	}
	return got.session, got.ok
}

// ErrorFromContext returns the error [Manager.Read] reported, which is only
// ever a store that could not be reached, and nil otherwise.
//
// It exists because [Manager.Read] distinguishes an ordinary logged out request
// from a store that could not answer, and a middleware that dropped that
// distinction would send every user to the identity provider at the moment the
// infrastructure is already failing, with nothing in the application's log to
// see it by. A handler that cares answers 503 instead of a login redirect.
func ErrorFromContext(ctx context.Context) error {
	got, ok := ctx.Value(contextKey{}).(outcome)
	if !ok {
		return nil
	}
	return got.err
}
