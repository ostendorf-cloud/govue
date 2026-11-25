// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package frontend serves the built frontend of a site as a server of its
// own, for a site that runs its frontend and its API as two images. It
// serves the Vite build with the prerendered pages written into it, with
// govue's security headers, and nothing else: no session, no cookie, no API
// route. The API runs in a second binary of the same site.
//
// New reads the whole build into memory at startup and refuses to start
// when it is incomplete. Every URL the server answers with 200 is a key of a
// table built then, so no request path is ever joined to a file name and no
// directory is ever listed:
//
//   - every loc of sitemap.xml, an exact path under a configured language,
//     from its <path>/index.html, with Cache-Control: no-cache and a strong
//     ETag
//   - the files under assets/, the hashed bundle of the build, immutable for
//     a hashed name and no-cache with an ETag otherwise, source maps only
//     with Config.ServeSourceMaps
//   - the three favicon files and the PublicFiles at the root of the build
//   - sitemap.xml as built, and a robots.txt that names it
//   - / as a redirect to the visitor's language (language.Redirect), and the
//     legal aliases of language.HandleLegal
//
// Every page and every 404 page gets the theme boot script and the favicon
// links injected at startup, through themeboot.InjectStatic. The script
// carries no nonce, since the body is the same on every response, and
// script-src carries themeboot.Hash instead.
//
// A non canonical path answers 404 before routing: an encoded slash,
// backslash, dot, percent or NUL, a decoded backslash, percent, control
// character or invalid UTF-8, a double slash, and a segment that starts with
// a dot, which covers "." and "..", dotfiles and .vite. The slash form of a
// page, /de/imprint/ for /de/imprint and /de for /de/, answers a 301 to the
// canonical path. Every other unknown path answers the 404 page of the
// language its first segment names, else of the default language.
//
// Config.App switches New to app mode, the server of a single page app such
// as the output of vite build: index.html for every path the app's router
// owns, the assets by the same rules, a JSON 404 under /api/ and the other
// reserved prefixes, and no sitemap, 404 page or legal page. See AppConfig.
//
// Run serves the handler and the admin listener with httpserver.RunAdmin:
// health, readiness and metrics on the admin listener only, the access log,
// the metrics middleware and the client IP resolver on the public one, the
// chain order of example/.
//
// See "Frontend server" in docs/concepts/govue.md for the image split, the
// Ingress and the API on another origin.
package frontend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// Config configures New. Build, ThemeColor and Logger are required, and in
// app mode, with App set, ThemeColor is optional.
type Config struct {
	// App, when set, serves a single page app instead of a prerendered
	// site: index.html for every path that is not a file of the build or a
	// reserved prefix, see AppConfig. Nil serves the prerendered site.
	App *AppConfig
	// Build is the output of the frontend build: Vite's dist directory with
	// the output of govue-prerender --out-dir written into it. An embedded
	// directory comes from fs.Sub of an embed.FS. A directory on disk comes
	// from os.OpenRoot(dir).FS(), never os.DirFS, which follows a symbolic
	// link out of the directory. New reads it once and keeps no handle.
	Build fs.FS
	// Language holds the languages, the default and the cookie of the
	// language redirect at / and of the legal aliases, with the rules of
	// language.Redirect. The zero value is de and en with de as the default.
	Language language.Config
	// ThemeColor is the theme colour of the favicon set, a #rgb or #rrggbb
	// value, see favicon.Load. The three favicon files must sit at the root
	// of Build.
	ThemeColor string
	// ThemeColorDark, when set, is the theme colour of the dark mode, and
	// ThemeColor that of the light mode. Every page then carries one
	// theme-color tag per mode, and the boot script pins both to the colour
	// of a stored mode. Empty keeps one tag for both modes.
	ThemeColorDark string
	// ThemeBoot holds the options of the theme boot script, such as
	// themeboot.WithStorageKey or themeboot.WithDefaultMode for a site
	// that prerenders a pinned mode. The hash in script-src follows them.
	// The favicon links are added by New.
	ThemeBoot []themeboot.Option
	// PublicFiles names further files at the root of Build that are served
	// at /<name>, such as og-image.png. Each is a plain file name of letters,
	// digits, '.', '_' and '-', not starting with a dot, with an extension
	// of the content type table, and not an HTML file.
	PublicFiles []string
	// ServeSourceMaps serves the *.map files under assets/ as
	// application/json. Off by default, a map is then skipped and answers
	// 404: it publishes the original sources, and a build with
	// build.sourcemap 'hidden' writes maps the site means to keep private.
	// Only a site that wants its sources public sets it.
	ServeSourceMaps bool
	// Redirects send the old paths of moved pages to their pages with a
	// 301 that keeps the query, such as /de/agb to /de/terms. A target must
	// be a page of the sitemap. A source must pass the path rules and must
	// not be a route already: a page, its slash form, /, an alias, a file
	// or anything under /assets/. A redirect is never counted as a view.
	Redirects []Redirect
	// Security is the header middleware. Nil is middleware.DefaultSecurity.
	// New adds themeboot.Hash to a copy of its policy's script-src, so the
	// policy must set script-src. A nil Policy sends no CSP at all, which is
	// the site's decision through this field.
	Security *middleware.Security
	// ClientIP resolves the client address of the access log and of
	// RateLimit. Nil trusts no proxy and logs the peer address.
	ClientIP *clientip.Resolver
	// Analytics counts page views, nil counts nothing. New places its
	// Middleware directly around the mux, inside the access log, and its
	// NotModified on each page, so a page answered with 200 or revalidated
	// with 304 is counted by its route pattern, never by its path. A
	// redirect, an alias, a 404, an asset, robots.txt and the sitemap are
	// never counted. The drop counter analytics_views_dropped_total is on
	// the frontend's own metrics, so the site passes no OnDrop. Run closes
	// the client after the listeners stopped. A site that serves Handler
	// itself closes it.
	Analytics *analytics.Client
	// Development allows the options that are for a developer's machine
	// only, today DevAPIProxy. The package never reads it from the
	// environment, the site decides. Off in production.
	Development bool
	// DevAPIProxy, with Development set, forwards every request under
	// /api/ to this URL, so the browser talks to the frontend and the API
	// on one origin in a compose stack, as the Ingress does in production.
	// It must be http or https on a loopback address, localhost or a single
	// label host such as a compose service name, with an optional port and
	// path prefix, and no userinfo, query or fragment. Without Development
	// it is refused, and empty /api/ stays a 404.
	DevAPIProxy string
	// DevAPITransport is the transport of DevAPIProxy. Nil is one with a
	// five second dial and a thirty second header timeout that ignores the
	// proxy environment. A test passes one that answers in memory.
	DevAPITransport http.RoundTripper
	// RateLimit, when set, is applied to every route. Nil applies none.
	RateLimit *middleware.RateLimit
	// Logger receives the access log and the server's own lines.
	Logger *slog.Logger
}

// viewsDroppedMetric names the counter of page views lost before they reached
// Umami, the name example/ uses.
const viewsDroppedMetric = "analytics_views_dropped_total"

// AnalyticsCloseTimeout bounds how long Run waits, after the listeners
// stopped, for the queued page views to reach Umami.
const AnalyticsCloseTimeout = 5 * time.Second

// Server is a frontend server built by New.
type Server struct {
	logger  *slog.Logger
	health  *health.Registry
	handler http.Handler
	admin   http.Handler
	// views is Config.Analytics, nil when the site counts nothing.
	views *analytics.Client
}

// New reads cfg.Build and returns the server. It returns every problem at
// once: a missing field, an invalid configuration value, never quoted, and
// an incomplete build, naming the file. It refuses a build without a 404
// page, de/404.html, for every language, without the imprint and privacy
// page in every language, with a terms page in some languages only, and
// without sitemap.xml. A terms page in every language mounts /terms and
// /agb. With Config.App it builds the server of a single page app, see
// AppConfig.
func New(cfg Config) (*Server, error) {
	if cfg.App != nil {
		return newApp(cfg)
	}
	var problems []error
	if cfg.Build == nil {
		problems = append(problems, errors.New("frontend: Config.Build is required"))
	}
	if cfg.Logger == nil {
		problems = append(problems, errors.New("frontend: Config.Logger is required"))
	}
	codes, def, err := cfg.Language.Resolve()
	if err != nil {
		problems = append(problems, fmt.Errorf("frontend: Config.Language: %w", err))
	}
	publicFiles, err := checkPublicFiles(cfg.PublicFiles, sitePublicFixed, sitePublicServed)
	if err != nil {
		problems = append(problems, err)
	}
	if err := checkRedirectSources(cfg.Redirects); err != nil {
		problems = append(problems, err)
	}
	devAPI, err := checkDevAPIProxy(cfg.DevAPIProxy, cfg.Development)
	if err != nil {
		problems = append(problems, err)
	}

	security, err := bootSecurity(cfg.Security, cfg.ThemeBoot, true)
	if err != nil {
		problems = append(problems, err)
	}

	var icons favicon.Icons
	if cfg.Build != nil {
		icons, err = favicon.Load(cfg.Build, cfg.ThemeColor)
		if err != nil {
			problems = append(problems, fmt.Errorf("frontend: Config.Build and Config.ThemeColor: %w", err))
		}
	}
	if cfg.ThemeColorDark != "" {
		icons, err = icons.WithDarkThemeColor(cfg.ThemeColorDark)
		if err != nil {
			problems = append(problems, fmt.Errorf("frontend: Config.ThemeColorDark: %w", err))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	opts := append(append([]themeboot.Option(nil), cfg.ThemeBoot...), themeboot.WithFavicon(icons))
	s, err := loadSite(cfg.Build, codes, def, opts, publicFiles, cfg.ServeSourceMaps)
	if err != nil {
		return nil, err
	}
	redirect, err := language.Redirect(cfg.Language)
	if err != nil {
		return nil, fmt.Errorf("frontend: Config.Language: %w", err)
	}

	resolver := cfg.ClientIP
	if resolver == nil {
		resolver = clientip.NewResolver(nil)
	}
	route := func(h http.Handler) http.Handler { return h }
	if cfg.RateLimit != nil {
		route = cfg.RateLimit.Middleware
	}

	// views counts page views: Middleware around the mux for a page answered
	// with 200, NotModified on each page for one revalidated with 304. Both
	// return the handler unchanged for a nil or inert client.
	views := cfg.Analytics
	if views == nil {
		views = &analytics.Client{}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /{$}", route(redirect))
	s.mount(mux, route, views.NotModified)
	legal := language.LegalConfig{Config: cfg.Language, Terms: s.terms}
	if err := language.HandleLegal(mux, legal, route); err != nil {
		return nil, fmt.Errorf("frontend: legal routes: %w", err)
	}
	if err := s.mountRedirects(mux, cfg.Redirects, route); err != nil {
		return nil, err
	}
	mux.Handle("/", route(s.catchAll()))

	registry := metrics.NewRegistry()
	httpMetrics := metrics.NewHTTP(registry)
	accessLog := middleware.AccessLog{ClientIP: resolver}
	if views.Enabled() {
		// The frontend owns the registry, so it owns the drop counter: it
		// reads the client's own count, and the site passes no OnDrop.
		metrics.NewCounterFunc(registry, viewsDroppedMetric,
			"Page views lost to a full queue or a shutdown before they reached Umami.",
			func() float64 { return float64(views.Stats().Dropped) })
	}

	srv := &Server{logger: cfg.Logger, health: health.New(), views: cfg.Analytics}
	// The view counter wraps the mux directly, so the pattern the mux sets is
	// still seen.
	site := views.Middleware(mux)
	if devAPI != nil {
		site = withDevAPIProxy(site, newDevAPIProxy(devAPI, cfg.DevAPITransport, resolver, cfg.Logger), route)
	}
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { s.writeNotFound(w, s.fallback) })
	srv.handler = publicChain(site, notFound, cfg.Logger, security, httpMetrics, accessLog)
	srv.admin = adminHandler(srv.health, metrics.Handler(registry), cfg.Logger)
	return srv, nil
}

// bootSecurity returns the security middleware of the site: configured, nil
// for middleware.DefaultSecurity. With boot set, a copy of its policy gets
// the hash of the theme boot script with opts in script-src, and a policy
// without script-src is refused. The site's own policy is never changed.
func bootSecurity(configured *middleware.Security, opts []themeboot.Option, boot bool) (middleware.Security, error) {
	security := middleware.DefaultSecurity()
	if configured != nil {
		security = *configured
	}
	if !boot || security.Policy == nil {
		return security, nil
	}
	if !security.Policy.Has("script-src") {
		return security, errors.New("frontend: Config.Security.Policy sets no script-src, " +
			"which the hash of the theme boot script needs")
	}
	security.Policy = security.Policy.Clone().Add("script-src", themeboot.Hash(opts...))
	return security, nil
}

// publicChain is the outer chain of example/: Recover first, then RequestID
// and the security headers, which replace the request, then the two
// middlewares that read r.Pattern. The path check sits between the access
// log and site and passes the request on unchanged, so r.Pattern still
// reaches both. A path that fails it answers notFound.
func publicChain(site, notFound http.Handler, logger *slog.Logger, security middleware.Security,
	httpMetrics *metrics.HTTP, accessLog middleware.AccessLog,
) http.Handler {
	return middleware.Chain(canonicalOnly(site, notFound),
		middleware.Recover(logger),
		middleware.RequestID(logger),
		security.Middleware,
		httpMetrics.Middleware,
		accessLog.Middleware,
	)
}

// adminHandler serves health, readiness and the metrics, behind Recover and
// RequestID only.
func adminHandler(h *health.Registry, metricsHandler http.Handler, logger *slog.Logger) http.Handler {
	adminMux := http.NewServeMux()
	h.Handle(adminMux)
	adminMux.Handle("GET /metrics", metricsHandler)
	return middleware.Chain(adminMux,
		middleware.Recover(logger),
		middleware.RequestID(logger),
	)
}

// Handler is the public handler: the pages, the assets and the redirects,
// behind the security headers, the metrics middleware and the access log.
// It serves no health, readiness or metrics route.
func (s *Server) Handler() http.Handler { return s.handler }

// AdminHandler serves GET /healthz, GET /readyz and GET /metrics, behind
// Recover and RequestID only. It belongs on the admin listener, which the
// Ingress never routes.
func (s *Server) AdminHandler() http.Handler { return s.admin }

// Health is the readiness registry. Run drains it before shutdown. A site
// may add a check, a frontend ordinarily has none.
func (s *Server) Health() *health.Registry { return s.health }

// Run serves Handler on addr and AdminHandler on adminAddr with
// httpserver.RunAdmin until ctx is cancelled, and fails readiness before the
// drain delay. adminAddr is required: without it no probe and no metric
// would be served. Once the listeners stopped, Run closes Config.Analytics
// within AnalyticsCloseTimeout, so the queued views are sent, and returns
// both errors joined.
func (s *Server) Run(ctx context.Context, addr, adminAddr string) error {
	if adminAddr == "" {
		return errors.New("frontend: run: the admin address is required, the probes and the metrics live there")
	}
	err := httpserver.RunAdmin(ctx, httpserver.Config{
		Addr:           addr,
		Handler:        s.handler,
		Logger:         s.logger,
		BeforeShutdown: s.health.Drain,
	}, httpserver.AdminConfig{
		Addr:    adminAddr,
		Handler: s.admin,
	})
	// The listeners have stopped, so the run context is done: Close gets a
	// fresh one, bounded by AnalyticsCloseTimeout.
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), AnalyticsCloseTimeout)
	defer cancel()
	return errors.Join(err, s.Close(closeCtx))
}

// Close sends the page views still queued in Config.Analytics and stops its
// workers, within ctx. Run calls it once the listeners stopped. A site that
// serves Handler on its own listener calls it after that listener stopped,
// with a bounded context. It is a no-op without Config.Analytics and safe to
// call more than once.
func (s *Server) Close(ctx context.Context) error {
	if s.views == nil {
		return nil
	}
	if err := s.views.Close(ctx); err != nil {
		return fmt.Errorf("frontend: close analytics: %w", err)
	}
	return nil
}
