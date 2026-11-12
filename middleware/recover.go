// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ostendorf-cloud/web-kit/api"
	"github.com/ostendorf-cloud/web-kit/logging"
)

// Recover returns a middleware that turns a panic in a later handler into a
// logged line and the api package's opaque 500, instead of an empty
// connection reset and a crashed goroutine.
//
// Recover is meant to sit outermost, ahead of RequestID, because a handler
// can panic before RequestID ever runs. logger is therefore the base logger
// an application built once, not one read from the context: the
// request-scoped logger RequestID puts there may not exist yet. Recover
// still reports a request id when one is available, read from the context
// first and, failing that, from the response header RequestIDHeader that
// RequestID sets before calling the next handler.
//
// http.ErrAbortHandler is re-panicked unchanged: it is another middleware's
// own signal to abort the response, for example a client that went away
// mid-write, and must reach the net/http server exactly as it would without
// Recover in the chain.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				problem := recover()
				if problem == nil {
					return
				}
				if problem == http.ErrAbortHandler { //nolint:errorlint,govet // sentinel value, re-panicked unchanged
					panic(problem)
				}

				attrs := []any{"method", r.Method, "panic", problem}
				if id := recoveredRequestID(r, w); id != "" {
					attrs = append(attrs, "request_id", id)
					if logging.RequestID(r.Context()) == "" {
						// RequestID may sit inside Recover, in which case this
						// handler's own r never saw the context it set on the
						// derived request it called next with. api.WriteError
						// reads the request id from r's context, so it is
						// carried over here from the response header fallback.
						r = r.WithContext(logging.WithRequestID(r.Context(), id))
					}
				}
				// No route and no path: the router never returned, so there is
				// no pattern yet, and a path can carry a token.
				logger.Error("panic recovered", attrs...)

				api.WriteError(w, r, api.Internal(fmt.Errorf("panic: %v", problem)))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// recoveredRequestID reads the request id of r, from its context if one was
// already carried there, and otherwise from the response header RequestID
// sets before it calls the next handler.
func recoveredRequestID(r *http.Request, w http.ResponseWriter) string {
	if id := logging.RequestID(r.Context()); id != "" {
		return id
	}
	return w.Header().Get(RequestIDHeader)
}
