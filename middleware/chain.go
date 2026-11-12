// Package middleware is the http.Handler wrapping this framework's
// applications compose themselves from: a chain helper, request id and panic
// recovery, an access log, a rate limiter, and the security and CORS header
// middlewares.
//
// Every middleware here takes its dependencies, including a *slog.Logger,
// through a field or a constructor argument. None of them builds its own
// logger, per this repository's own rule.
package middleware

import "net/http"

// Middleware wraps a handler with additional behaviour, running code before
// and after the next handler in the chain.
type Middleware func(http.Handler) http.Handler

// Chain wraps h with middlewares, the first argument outermost: it runs
// first on the way in and last on the way out. Chain(h, A, B) behaves like
// A(B(h)).
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
