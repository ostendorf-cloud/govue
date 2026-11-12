// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ostendorf-cloud/web-kit/api"
	"github.com/ostendorf-cloud/web-kit/clientip"
	"github.com/ostendorf-cloud/web-kit/csrf"
	"github.com/ostendorf-cloud/web-kit/health"
	"github.com/ostendorf-cloud/web-kit/metrics"
	"github.com/ostendorf-cloud/web-kit/middleware"
	"github.com/ostendorf-cloud/web-kit/oidc"
	"github.com/ostendorf-cloud/web-kit/ratelimit"
	"github.com/ostendorf-cloud/web-kit/session"
)

// maxRateLimitKeys bounds the number of distinct client addresses the rate
// limiter tracks at once, so a flood of one-shot addresses cannot grow its
// map without bound; see the ratelimit package for how that bound works.
const maxRateLimitKeys = 100_000

// app holds every dependency this example wires together. It is the source
// of truth this repository points at for the order a middleware chain has
// to run in; see (*app).handler.
type app struct {
	logger *slog.Logger
	cfg    config

	clientIPs *clientip.Resolver

	sessions   *session.Manager
	loginCodec *session.Codec
	protector  *csrf.Protector

	oidc *oidc.Provider

	health *health.Registry

	httpMetrics    *metrics.HTTP
	metricsHandler http.Handler

	limiter   *ratelimit.Limiter
	rateLimit *middleware.RateLimit
	cors      *middleware.CORS

	security  middleware.Security
	accessLog middleware.AccessLog

	notes *noteStore
}

// newApp constructs every dependency from cfg. It returns every construction
// problem at once, the same way loadConfig does, rather than stopping at the
// first one.
func newApp(cfg config, logger *slog.Logger) (*app, error) {
	a := &app{
		logger:    logger,
		cfg:       cfg,
		clientIPs: clientip.NewResolver(cfg.trustedProxies),
		notes:     &noteStore{},
	}

	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	sessions, err := session.New(session.Config{
		Store:          session.NewMemoryStore(nil),
		AllowPlainHTTP: cfg.allowPlainHTTP,
	})
	fail(err)
	a.sessions = sessions

	loginCodec, err := session.NewCodec(session.CodecConfig{Key: cfg.sessionKey})
	fail(err)
	a.loginCodec = loginCodec

	// Expected is the one place this application connects the session
	// layer to csrf, which never imports session itself: it reads the
	// session a request carries and derives that session's token.
	protector, err := csrf.New(func(r *http.Request) (string, bool) {
		s, ok, err := a.sessions.Read(r.Context(), r)
		if err != nil || !ok {
			return "", false
		}
		return csrf.Token(cfg.csrfKey, s.ID), true
	})
	fail(err)
	a.protector = protector
	// The default plain text refusal is replaced so a refused request gets
	// this application's one error shape like everything else, never a
	// response body that looks different depending on which middleware
	// refused it.
	a.protector.OnRefusal(func(w http.ResponseWriter, r *http.Request, _ error) {
		api.WriteError(w, r, api.PermissionDenied("cross origin or stale request, please reload"))
	})
	for _, origin := range cfg.allowedOrigins {
		fail(a.protector.TrustOrigin(origin))
	}

	provider, err := oidc.New(oidc.Config{
		Issuer:       cfg.oidcIssuer,
		ClientID:     cfg.oidcClientID,
		ClientSecret: cfg.oidcClientSecret,
		RedirectURL:  cfg.oidcRedirectURL,
	})
	fail(err)
	a.oidc = provider

	a.health = health.New()
	// A real dependency, a database or the identity provider, would be
	// pinged here. The example has none: the in-memory notes store cannot
	// fail, so this check only demonstrates that readiness runs it and
	// reports it by name.
	a.health.Add("notes-store", 0, func(context.Context) error { return nil })

	registry := metrics.NewRegistry()
	a.httpMetrics = metrics.NewHTTP(registry)
	a.metricsHandler = metrics.Handler(registry)

	a.limiter = ratelimit.New(cfg.rateLimitBurst, cfg.rateLimitPer, maxRateLimitKeys)
	rateLimit, err := middleware.NewRateLimit(middleware.RateLimitConfig{Limiter: a.limiter, ClientIP: a.clientIPs})
	fail(err)
	a.rateLimit = rateLimit

	cors, err := middleware.NewCORS(middleware.CORSConfig{
		Origins: cfg.allowedOrigins,
		// csrf.HeaderName is added so a cross-origin preflight allows the
		// header the protector requires on the real request that follows.
		Headers:     []string{"Content-Type", csrf.HeaderName},
		Credentials: true,
	})
	fail(err)
	a.cors = cors

	a.security = middleware.DefaultSecurity()
	if cfg.allowPlainHTTP {
		// A browser that reached this application over plain HTTP must
		// never be told to upgrade every future visit to HTTPS.
		a.security.HSTS = 0
	}

	a.accessLog = middleware.AccessLog{ClientIP: a.clientIPs}

	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("example: build app: %w", err)
	}
	return a, nil
}

// handler builds the two chains this repository's audit of the ordering
// problem settled on: one outer chain wrapping the mux, and one route chain
// applied to every handler the mux dispatches to. See docs/concepts/web-kit.md
// for why a single flat chain cannot get both the access log and a
// self-answering middleware right at once.
func (a *app) handler() http.Handler {
	mux := http.NewServeMux()

	// Health and metrics are mounted bare, outside the route chain: a load
	// balancer polling /healthz must never be rate limited, and a scrape
	// must never be refused for a missing CSRF token.
	a.health.Handle(mux)
	mux.Handle("GET /metrics", a.metricsHandler)

	// route is the per-handler chain, outermost first. cors, the rate
	// limiter and the csrf protector all answer a request themselves
	// instead of calling the next handler, and that answer only carries
	// the real route label when it runs after the mux has already set
	// r.Pattern on this request: ahead of the mux, in the outer chain
	// below, the same answer would be logged and counted as "unmatched"
	// instead.
	route := func(h http.Handler) http.Handler {
		return middleware.Chain(h, a.cors.Middleware, a.rateLimit.Middleware, a.protector.Middleware)
	}

	mux.Handle("GET /app", route(api.Handle(a.home)))
	mux.Handle("GET /notes", route(api.Handle(a.listNotes)))
	mux.Handle("POST /notes", route(api.Handle(a.createNote)))
	mux.Handle("GET /login", route(api.Handle(a.loginStart)))
	mux.Handle("GET /login/callback", route(api.Handle(a.loginCallback)))
	mux.Handle("POST /logout", route(api.Handle(a.logout)))
	// The catch-all. Without it an unregistered path never reaches a
	// handler the route chain wraps, so it would skip the rate limiter
	// entirely and be labelled "unmatched" rather than the bounded "/".
	mux.Handle("/", route(api.NotFoundHandler()))

	// The outer chain, outermost first. Recover sits first so it catches a
	// panic from everything below it, RequestID included. RequestID and
	// the security middleware both call (*http.Request).WithContext, which
	// returns a different *http.Request value than the one the mux
	// matched against, so both have to sit outside the metrics and access
	// log middlewares below: those two read r.Pattern, which http.ServeMux
	// sets in place on the exact request value it was handed, not on a
	// copy made afterwards.
	return middleware.Chain(mux,
		middleware.Recover(a.logger),
		middleware.RequestID(a.logger),
		a.security.Middleware,
		a.httpMetrics.Middleware,
		a.accessLog.Middleware,
	)
}
