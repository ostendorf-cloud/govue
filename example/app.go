// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/appassoc"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/oidc"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/session"
	"github.com/ostendorf-cloud/govue/session/devicehttp"
)

// maxRateLimitKeys bounds the number of distinct client addresses the rate
// limiter tracks at once, so a flood of one-shot addresses cannot grow its
// map without bound; see the ratelimit package for how that bound works.
const maxRateLimitKeys = 100_000

// maxSessions bounds the number of sessions the memory store tracks at once,
// so an anonymous session minted per unauthenticated caller cannot grow its
// map without bound; see the session package for how that bound works.
const maxSessions = 10_000

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

	// accounts are the local account handlers, with their own stricter rate
	// limit. See buildAccounts.
	accounts      *account.Handlers
	authLimiter   *ratelimit.Limiter
	authRateLimit *middleware.RateLimit

	// devices are the token handlers of a native app, over the same accounts.
	// See buildDevices.
	devices *devicehttp.Handlers

	health *health.Registry

	httpMetrics    *metrics.HTTP
	metricsHandler http.Handler

	limiter   *ratelimit.Limiter
	rateLimit *middleware.RateLimit
	cors      *middleware.CORS
	// appOrigins and appCORS are the native app mode. Both are zero and nil
	// unless APP_ORIGINS is set. appOrigins feeds Issue and appCORS and
	// nothing else.
	appOrigins middleware.AppOrigins
	appCORS    *middleware.AppCORS

	security  middleware.Security
	accessLog middleware.AccessLog

	// analytics counts page views through Umami. It is never nil after
	// newApp, and inert unless UMAMI_URL and UMAMI_WEBSITE_ID are set.
	analytics *analytics.Client

	notes *noteStore

	icons favicon.Icons

	// assoc serves the app association files. It is nil unless the
	// configuration names an app.
	assoc *appassoc.Handlers

	// site and languageRedirect serve the prerendered language pages. Both
	// are nil without SITE_DIR.
	site             *site
	languageRedirect http.Handler
	// siteRoot is the open site directory, nil without SITE_DIR. Pages are
	// read from it at startup and assets on every request, so it stays open
	// until close.
	siteRoot *os.Root
}

// analyticsCloseTimeout is how long close waits for the queued page views to
// reach Umami before it gives up on them.
const analyticsCloseTimeout = 5 * time.Second

// close sends the page views still queued, within analyticsCloseTimeout, and
// releases the site directory. It is safe to call without SITE_DIR and
// without Umami.
func (a *app) close() error {
	var problems []error
	if a.analytics != nil {
		ctx, cancel := context.WithTimeout(context.Background(), analyticsCloseTimeout)
		defer cancel()
		problems = append(problems, a.analytics.Close(ctx))
	}
	if a.siteRoot != nil {
		problems = append(problems, a.siteRoot.Close())
	}
	return errors.Join(problems...)
}

// themeColor is the accent of the example theme, the colour browsers tint
// their toolbar with.
const themeColor = "#904769"

// publicFiles holds the favicon files, generated from favicon.svg with the
// favicon:generate task target. A site embeds its web/public the same way, or
// serves the built frontend's copy of it.
//
//go:embed all:web/public
var publicFiles embed.FS

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
		Store:          session.NewMemoryStore(cfg.now, maxSessions),
		AllowPlainHTTP: cfg.allowPlainHTTP,
		Now:            cfg.now,
		// Device sessions are on, so a bearer request authenticates by its
		// token. The memory store signs every app out on a restart. A
		// product with apps writes a shared store.
		Devices: session.DeviceConfig{Store: session.NewMemoryDeviceStore(cfg.now, maxSessions)},
		// A failed bearer request gets this application's one error shape.
		// The middleware has already set WWW-Authenticate and no-store.
		OnBearerRefusal: func(w http.ResponseWriter, r *http.Request) {
			api.WriteError(w, r, api.Unauthenticated("the token is not valid"))
		},
	})
	fail(err)
	a.sessions = sessions

	loginCodec, err := session.NewCodec(session.CodecConfig{Key: cfg.sessionKey, AllowPlainHTTP: cfg.allowPlainHTTP})
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
	// App mode: a native app's bearer request skips the CSRF check. That is
	// safe only because every route sits behind the session middleware,
	// which never reads the cookie on such a request. See (*app).handler.
	a.protector.ExemptBearer()

	provider, err := oidc.New(oidc.Config{
		Issuer:       cfg.oidcIssuer,
		ClientID:     cfg.oidcClientID,
		ClientSecret: cfg.oidcClientSecret,
		RedirectURL:  cfg.oidcRedirectURL,
	})
	fail(err)
	a.oidc = provider

	// The favicon files are embedded from web/public, the one path a site keeps
	// them at. A missing or empty file fails startup here, naming the file.
	public, err := fs.Sub(publicFiles, "web/public")
	if err != nil {
		fail(fmt.Errorf("example: favicon directory: %w", err))
	} else {
		icons, err := favicon.Load(public, themeColor)
		fail(err)
		a.icons = icons
	}

	// The language site is the output of the prerender step. The sitemap in
	// it lists the pages, so Go needs no copy of the route table.
	if cfg.siteDir != "" {
		redirect, err := language.Redirect(language.Config{Languages: siteLanguages})
		fail(err)
		a.languageRedirect = redirect
		// OpenRoot, not DirFS: the root refuses a symbolic link that leaves
		// the directory, so a page or an asset can never be read from
		// outside it. A page that links out fails startup, naming the page.
		root, err := os.OpenRoot(cfg.siteDir)
		fail(err)
		if err == nil {
			a.siteRoot = root
			site, err := loadSite(root.FS(), siteLanguages, a.icons)
			fail(err)
			a.site = site
		}
	}

	if !cfg.assoc.IsEmpty() {
		assoc, err := appassoc.New(cfg.assoc)
		fail(err)
		a.assoc = assoc
	}

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

	fail(a.buildAppOrigins())
	fail(a.buildAccounts())

	cors, err := middleware.NewCORS(middleware.CORSConfig{
		Origins: cfg.allowedOrigins,
		// csrf.HeaderName is added so a cross-origin preflight allows the
		// header the protector requires on the real request that follows.
		Headers:     []string{"Content-Type", csrf.HeaderName},
		Credentials: true,
	})
	fail(err)
	a.cors = cors
	fail(a.buildAppCORS())

	a.security = middleware.DefaultSecurity()
	if cfg.allowPlainHTTP {
		// A browser that reached this application over plain HTTP must
		// never be told to upgrade every future visit to HTTPS.
		a.security.HSTS = 0
	}

	a.accessLog = middleware.AccessLog{ClientIP: a.clientIPs}

	// Page view counting. The same resolver as the access log and the rate
	// limiter decides the client address, and the dropped views are counted
	// in the registry the admin listener serves. Both an unset UMAMI_URL and
	// UMAMI_WEBSITE_ID give an inert client, so nothing is sent.
	viewsAnalytics, err := analytics.New(cfg.umami, analytics.Options{
		Hostname:   cfg.siteHost,
		Logger:     logger,
		HTTPClient: cfg.umamiHTTP,
		ClientIP:   a.clientIPs,
		OnDrop: metrics.NewCounter(registry, "analytics_views_dropped_total",
			"Page views lost to a full queue or a shutdown before they reached Umami."),
	})
	fail(err)
	a.analytics = viewsAnalytics

	if err := errors.Join(problems...); err != nil {
		_ = a.close()
		return nil, fmt.Errorf("example: build app: %w", err)
	}
	return a, nil
}

// handler builds the two chains this repository's audit of the ordering
// problem settled on: one outer chain wrapping the mux, and one route chain
// applied to every handler the mux dispatches to. See docs/concepts/govue.md
// for why a single flat chain cannot get both the access log and a
// self-answering middleware right at once.
//
// Health and metrics are not mounted here any more: see (*app).adminHandler
// and docs/concepts/govue.md for why they moved to the second listener
// httpserver.RunAdmin serves them on in main.go.
func (a *app) handler() http.Handler {
	mux := http.NewServeMux()

	// route is the per-handler chain, outermost first. cors, the rate
	// limiter and the csrf protector all answer a request themselves
	// instead of calling the next handler, and that answer only carries
	// the real route label when it runs after the mux has already set
	// r.Pattern on this request: ahead of the mux, in the outer chain
	// below, the same answer would be logged and counted as "unmatched"
	// instead.
	//
	// The session middleware sits after the csrf protector, so a cookie
	// request is checked exactly as before it is read. It answers a failed
	// bearer request with 401 on every route. The csrf protector's
	// ExemptBearer lets a bearer request through only because of that: such
	// a request never authenticates by the cookie. A route mounted without
	// this chain must not sit behind the exempting protector either.
	route := func(h http.Handler) http.Handler {
		return middleware.Chain(h, a.corsMiddleware(), a.rateLimit.Middleware, a.protector.Middleware, a.sessions.Middleware)
	}

	// checked is the route chain with the stricter limit of the routes that
	// check a password. The limit sits before the CSRF protector and the
	// session, like the general one, so a refused guess costs no lookup and
	// no derivation.
	checked := func(h http.Handler) http.Handler {
		return middleware.Chain(h, a.corsMiddleware(), a.rateLimit.Middleware, a.authRateLimit.Middleware,
			a.protector.Middleware, a.sessions.Middleware)
	}
	a.mountAccounts(mux, route, checked)
	a.mountDevices(mux, route, checked)

	mux.Handle("GET /app", route(api.Handle(a.home)))
	mux.Handle("GET /notes", route(api.Handle(a.listNotes)))
	mux.Handle("POST /notes", route(api.Handle(a.createNote)))
	mux.Handle("GET /login", route(api.Handle(a.loginStart)))
	mux.Handle("GET /login/callback", route(api.Handle(a.loginCallback)))
	mux.Handle("POST /logout", route(api.Handle(a.logout)))
	if a.site != nil {
		// The public pages. "GET /{$}" matches the root alone, so a prefixed
		// path never reaches the redirect and a crawler keeps the language
		// it asked for. Every page is an exact pattern: ServeMux then never
		// redirects between "/de/impressum" and "/de/impressum/", and a
		// path outside the sitemap falls to the catch-all below. They sit
		// in the route chain like every other handler, so the rate limiter
		// sees them and the log carries their pattern.
		mux.Handle("GET /{$}", route(a.languageRedirect))
		a.site.mount(mux, route)
	}
	if a.assoc != nil {
		// The app association files sit outside the route chain on purpose.
		// iOS and Android fetch them without a cookie and without redirects,
		// and a platform fetcher shares a few addresses, so the rate limiter
		// would refuse it. The session middleware would answer a malformed
		// Authorization header with 401, and CORS and CSRF have nothing to
		// protect on a public GET. The outer chain still applies: the
		// access log and the security headers. Both patterns are exact, so
		// the catch-all below never wins over them and ServeMux never
		// redirects a trailing slash.
		a.assoc.Handle(mux, nil)
	}
	// The catch-all. Without it an unregistered canonical path never
	// reaches a handler the route chain wraps, so it would skip the rate
	// limiter entirely and be labelled "unmatched" rather than the bounded
	// "/". A non-canonical path, one with a double slash or a "." or ".."
	// segment, never reaches this catch-all either: ServeMux answers it
	// itself with a 307 to the cleaned path, before the route chain runs,
	// so CORS, the rate limiter and the CSRF protector are all skipped for
	// that one response. That is accepted: the 307 carries no body and
	// reaches no application handler, and the client's follow-up request
	// to the canonical path does go through the chain. The logged route
	// label still names that canonical route; ServeMux sets r.Pattern
	// before it hands the request to its own redirect handler.
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
		// Innermost, directly around the mux, because it reads r.Pattern
		// like the two above. It answers nothing itself and never delays a
		// response. It is the mux itself when counting is off.
		a.analytics.Middleware,
	)
}

// adminHandler builds the handler httpserver.RunAdmin serves on the second,
// administrative listener in main.go: the liveness and readiness probes and
// the Prometheus scrape, none of which this application's Ingress ever
// routes to. See docs/concepts/govue.md for the reasoning.
//
// The middleware here is a deliberate, short list, not the outer chain
// above with entries removed: a probe and a scrape are not browser
// traffic, so the security headers and CORS have nothing to protect; they
// are not requests a user can forge, so the CSRF protector has nothing to
// check; and a scraper polling this listener every few seconds must not be
// throttled by the rate limiter or fill the access log the way a public
// request would. a.httpMetrics.Middleware is absent too, deliberately: a
// scrape endpoint instrumenting its own hits would feed Prometheus a metric
// about scraping Prometheus, which is not a loop worth closing. Recover and
// RequestID stay, because a panic here is still worth catching and still
// worth a correlatable log line.
func (a *app) adminHandler() http.Handler {
	mux := http.NewServeMux()
	a.health.Handle(mux)
	mux.Handle("GET /metrics", a.metricsHandler)

	return middleware.Chain(mux,
		middleware.Recover(a.logger),
		middleware.RequestID(a.logger),
	)
}
