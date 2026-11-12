// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"
	"time"

	"github.com/ostendorf-cloud/web-kit/api"
	"github.com/ostendorf-cloud/web-kit/clientip"
	"github.com/ostendorf-cloud/web-kit/ratelimit"
)

// sharedRateLimitKey is the bucket a RateLimit with no ClientIP resolver puts
// every request under. That is almost never what an application wants: it
// rate-limits every client together, so one busy or hostile client can
// exhaust the whole budget for everyone else. Set ClientIP to key by client
// instead.
const sharedRateLimitKey = "shared"

// RateLimit answers a denied request itself instead of calling the next
// handler, through a *ratelimit.Limiter an application builds and owns.
type RateLimit struct {
	// Limiter holds the actual token buckets. Required.
	Limiter *ratelimit.Limiter
	// ClientIP resolves the key to rate limit by. Nil keys every request
	// under one shared bucket; see sharedRateLimitKey.
	ClientIP *clientip.Resolver
	// Respond answers a denied request. Nil defaults to writing
	// api.TooManyRequests(retryAfter) in the api package's error shape.
	Respond func(w http.ResponseWriter, r *http.Request, retryAfter time.Duration)
}

// Middleware returns the rate limit handler.
func (l RateLimit) Middleware(next http.Handler) http.Handler {
	respond := l.Respond
	if respond == nil {
		respond = defaultRateLimitRespond
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, wait := l.Limiter.Take(l.key(r))
		if !allowed {
			respond(w, r, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// key returns the bucket key for r: the resolved client when ClientIP is
// set, and the one shared key for every request otherwise.
func (l RateLimit) key(r *http.Request) string {
	if l.ClientIP == nil {
		return sharedRateLimitKey
	}
	return clientip.Key(l.ClientIP.Resolve(r))
}

// defaultRateLimitRespond answers a denied request in the api package's
// error shape, with the limiter's own wait as the Retry-After hint.
func defaultRateLimitRespond(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	api.WriteError(w, r, api.TooManyRequests(retryAfter))
}
