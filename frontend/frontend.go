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
//     a hashed name and no-cache with an ETag otherwise
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

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// Config configures New. Build, ThemeColor and Logger are required.
type Config struct {
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
	// ThemeBoot holds the options of the theme boot script, such as
	// themeboot.WithStorageKey. The favicon links are added by New.
	ThemeBoot []themeboot.Option
	// PublicFiles names further files at the root of Build that are served
	// at /<name>, such as og-image.png. Each is a plain file name of letters,
	// digits, '.', '_' and '-', not starting with a dot, with an extension
	// of the content type table, and not an HTML file.
	PublicFiles []string
	// Security is the header middleware. Nil is middleware.DefaultSecurity.
	// New adds themeboot.Hash to a copy of its policy's script-src, so the
	// policy must set script-src. A nil Policy sends no CSP at all, which is
	// the site's decision through this field.
	Security *middleware.Security
	// ClientIP resolves the client address of the access log and of
	// RateLimit. Nil trusts no proxy and logs the peer address.
	ClientIP *clientip.Resolver
	// RateLimit, when set, is applied to every route. Nil applies none.
	RateLimit *middleware.RateLimit
	// Logger receives the access log and the server's own lines.
	Logger *slog.Logger
}

// Server is a frontend server built by New.
type Server struct {
	logger  *slog.Logger
	health  *health.Registry
	handler http.Handler
	admin   http.Handler
}

// New reads cfg.Build and returns the server. It returns every problem at
// once: a missing field, an invalid configuration value, never quoted, and
// an incomplete build, naming the file. It refuses a build without a 404
// page, de/404.html, for every language, without the imprint and privacy
// page in every language, with a terms page in some languages only, and
// without sitemap.xml. A terms page in every language mounts /terms and
// /agb.
func New(cfg Config) (*Server, error) {
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
	publicFiles, err := checkPublicFiles(cfg.PublicFiles)
	if err != nil {
		problems = append(problems, err)
	}

	security := middleware.DefaultSecurity()
	if cfg.Security != nil {
		security = *cfg.Security
	}
	if security.Policy != nil {
		if !security.Policy.Has("script-src") {
			problems = append(problems, errors.New("frontend: Config.Security.Policy sets no script-src, "+
				"which the hash of the theme boot script needs"))
		} else {
			security.Policy = security.Policy.Clone().Add("script-src", themeboot.Hash(cfg.ThemeBoot...))
		}
	}

	var icons favicon.Icons
	if cfg.Build != nil {
		icons, err = favicon.Load(cfg.Build, cfg.ThemeColor)
		if err != nil {
			problems = append(problems, fmt.Errorf("frontend: Config.Build and Config.ThemeColor: %w", err))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	opts := append(append([]themeboot.Option(nil), cfg.ThemeBoot...), themeboot.WithFavicon(icons))
	s, err := loadSite(cfg.Build, codes, def, opts, publicFiles)
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

	mux := http.NewServeMux()
	mux.Handle("GET /{$}", route(redirect))
	s.mount(mux, route)
	legal := language.LegalConfig{Config: cfg.Language, Terms: s.terms}
	if err := language.HandleLegal(mux, legal, route); err != nil {
		return nil, fmt.Errorf("frontend: legal routes: %w", err)
	}
	mux.Handle("/", route(s.catchAll()))

	registry := metrics.NewRegistry()
	httpMetrics := metrics.NewHTTP(registry)
	accessLog := middleware.AccessLog{ClientIP: resolver}

	srv := &Server{logger: cfg.Logger, health: health.New()}
	// The outer chain of example/: Recover first, then RequestID and the
	// security headers, which replace the request, then the two middlewares
	// that read r.Pattern. The path check sits between the access log and
	// the mux and passes the request on unchanged, so the pattern the mux
	// sets is still seen.
	srv.handler = middleware.Chain(s.canonicalOnly(mux),
		middleware.Recover(cfg.Logger),
		middleware.RequestID(cfg.Logger),
		security.Middleware,
		httpMetrics.Middleware,
		accessLog.Middleware,
	)

	adminMux := http.NewServeMux()
	srv.health.Handle(adminMux)
	adminMux.Handle("GET /metrics", metrics.Handler(registry))
	srv.admin = middleware.Chain(adminMux,
		middleware.Recover(cfg.Logger),
		middleware.RequestID(cfg.Logger),
	)
	return srv, nil
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
// would be served.
func (s *Server) Run(ctx context.Context, addr, adminAddr string) error {
	if adminAddr == "" {
		return errors.New("frontend: run: the admin address is required, the probes and the metrics live there")
	}
	return httpserver.RunAdmin(ctx, httpserver.Config{
		Addr:           addr,
		Handler:        s.handler,
		Logger:         s.logger,
		BeforeShutdown: s.health.Drain,
	}, httpserver.AdminConfig{
		Addr:    adminAddr,
		Handler: s.admin,
	})
}
