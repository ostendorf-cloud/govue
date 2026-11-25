// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/internal/htmlhead"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// AppConfig turns New into the server of a single page app, such as the
// output of vite build: one index.html and the bundle under assets/. The
// app's router owns every path, so the server answers index.html for every
// GET and HEAD that names no file of the build and no reserved prefix:
//
//   - the files under assets/, by the rules of the prerendered site:
//     immutable for a hashed name, no-cache with an ETag otherwise, a plain
//     404 for an unknown one, never index.html
//   - the PublicFiles, and the three favicon files when Config.ThemeColor
//     is set
//   - robots.txt, which disallows every path unless Robots says otherwise
//   - a reserved prefix, /api/ and ReservedPrefixes, answers the JSON 404
//     of api.NotFoundHandler for every method, so a mistyped API path is
//     never a 200 with HTML
//   - every other path answers index.html with 200, Cache-Control: no-cache
//     and a strong ETag, or no-store with NoStore
//
// No sitemap, no 404 page and no legal page is required, and none is
// served. The path rules of the prerendered site hold, a failing path
// answers a plain 404. There is no slash redirect: /documents/42/ is the
// app's path to route. Config.Language, Config.Redirects and
// Config.Analytics belong to the prerendered site and are refused.
type AppConfig struct {
	// ReservedPrefixes are path prefixes beside /api/, which is always
	// reserved, that answer the JSON 404 and never index.html, such as
	// /auth/ for an OIDC callback the Ingress routes to the API. Each is a
	// slash, one or more plain segments and a trailing slash. The prefix
	// without its trailing slash is reserved too, as the Ingress rule
	// pathType: Prefix matches it.
	ReservedPrefixes []string
	// ThemeBoot injects the theme boot script, with the options of
	// Config.ThemeBoot, right after the head tag of index.html, and adds its
	// hash to script-src. Off, index.html carries no inline script and the
	// policy is not changed.
	ThemeBoot bool
	// NoStore serves index.html with Cache-Control: no-store instead of
	// no-cache. Off, a browser keeps index.html and revalidates it with its
	// ETag on every load, so a new deployment reaches it at once.
	NoStore bool
	// Robots is the body of /robots.txt. Empty disallows every path: the
	// paths of an app are no pages to index.
	Robots string
}

// The fixed names and values of app mode.
const (
	indexFile = "index.html"
	// appRobots is the robots.txt of an app without AppConfig.Robots.
	appRobots = "User-agent: *\nDisallow: /\n"
	// cacheNoStore is the Cache-Control of index.html with AppConfig.NoStore.
	cacheNoStore = "no-store"
	// apiPrefix is the reserved prefix of every app.
	apiPrefix = devAPIPrefix
)

// app is the build of a single page app, in memory.
type app struct {
	index  *file
	robots *file
	assets map[string]*file
	public map[string]*file
}

// newApp is New with Config.App set.
func newApp(cfg Config) (*Server, error) {
	appCfg := *cfg.App
	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}
	if cfg.Build == nil {
		fail(errors.New("frontend: Config.Build is required"))
	}
	if cfg.Logger == nil {
		fail(errors.New("frontend: Config.Logger is required"))
	}
	if len(cfg.Language.Languages) > 0 || cfg.Language.Default != "" || cfg.Language.Cookie != "" {
		fail(errors.New("frontend: Config.Language is refused in app mode, the app's router owns its paths"))
	}
	if len(cfg.Redirects) > 0 {
		fail(errors.New("frontend: Config.Redirects is refused in app mode, a redirect needs a page of a sitemap"))
	}
	if cfg.Analytics != nil {
		fail(errors.New("frontend: Config.Analytics is refused in app mode, every route of the app has the " +
			"one pattern of the fallback, so a view would say nothing"))
	}
	if cfg.ThemeColor == "" && cfg.ThemeColorDark != "" {
		fail(errors.New("frontend: Config.ThemeColorDark needs Config.ThemeColor"))
	}
	if len(cfg.ThemeBoot) > 0 && !appCfg.ThemeBoot {
		fail(errors.New("frontend: Config.ThemeBoot needs Config.App.ThemeBoot in app mode"))
	}
	// The root files app mode serves itself: robots.txt always, the favicon
	// set only with ThemeColor. Without it a favicon file of the app's own
	// is a PublicFiles entry like any other, and there is no sitemap.
	served := []string{robotsFile}
	servedText := "robots.txt"
	if cfg.ThemeColor != "" {
		served = append(served, faviconFiles...)
		servedText = "robots.txt or a favicon file, which Config.ThemeColor serves"
	}
	publicFiles, err := checkPublicFiles(cfg.PublicFiles, served, servedText)
	fail(err)
	reserved, err := checkReservedPrefixes(appCfg.ReservedPrefixes, append(served, publicFiles...))
	fail(err)
	devAPI, err := checkDevAPIProxy(cfg.DevAPIProxy, cfg.Development)
	fail(err)
	security, err := bootSecurity(cfg.Security, cfg.ThemeBoot, appCfg.ThemeBoot)
	fail(err)

	var icons *favicon.Icons
	if cfg.Build != nil && cfg.ThemeColor != "" {
		loaded, err := favicon.Load(cfg.Build, cfg.ThemeColor)
		if err != nil {
			fail(fmt.Errorf("frontend: Config.Build and Config.ThemeColor: %w", err))
		} else if cfg.ThemeColorDark != "" {
			loaded, err = loaded.WithDarkThemeColor(cfg.ThemeColorDark)
			fail(wrapIf("frontend: Config.ThemeColorDark", err))
		}
		icons = &loaded
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	fixed := []string(nil)
	if icons != nil {
		fixed = faviconFiles
	}
	a, err := loadApp(cfg.Build, appBuild{
		boot: appCfg.ThemeBoot, bootOpts: cfg.ThemeBoot, icons: icons, noStore: appCfg.NoStore,
		robots: appCfg.Robots, fixed: fixed, publicFiles: publicFiles, sourceMaps: cfg.ServeSourceMaps,
	})
	if err != nil {
		return nil, err
	}

	resolver := cfg.ClientIP
	if resolver == nil {
		resolver = clientip.NewResolver(nil)
	}
	route := func(h http.Handler) http.Handler { return h }
	if cfg.RateLimit != nil {
		route = cfg.RateLimit.Middleware
	}
	notFound := http.HandlerFunc(plainNotFound)

	mux := http.NewServeMux()
	mux.Handle("GET /"+robotsFile, route(a.robots.handler()))
	for name, f := range a.public {
		mux.Handle("GET "+name, route(f.handler()))
	}
	mountAssets(mux, a.assets, notFound, route)
	mux.Handle("GET /", route(a.index.handler()))
	mux.Handle("/", route(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { methodNotAllowed(w) })))

	// The reserved prefixes sit on a mux of their own, so the access log and
	// the metrics see their pattern, such as /api/, and no pattern of the
	// fallback has to outrank them.
	reservedMux := http.NewServeMux()
	for _, prefix := range reserved {
		reservedMux.Handle(prefix, route(api.NotFoundHandler()))
		reservedMux.Handle(strings.TrimSuffix(prefix, "/"), route(api.NotFoundHandler()))
	}
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isReserved(r.URL.Path, reserved) {
			reservedMux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	var handler http.Handler = site
	if devAPI != nil {
		handler = withDevAPIProxy(site, newDevAPIProxy(devAPI, cfg.DevAPITransport, resolver, cfg.Logger), route)
	}

	registry := metrics.NewRegistry()
	srv := &Server{logger: cfg.Logger, health: health.New()}
	srv.handler = publicChain(handler, notFound, cfg.Logger, security, metrics.NewHTTP(registry),
		middleware.AccessLog{ClientIP: resolver})
	srv.admin = adminHandler(srv.health, metrics.Handler(registry), cfg.Logger)
	return srv, nil
}

// wrapIf wraps err with prefix, nil stays nil.
func wrapIf(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

// checkReservedPrefixes checks AppConfig.ReservedPrefixes and returns every
// reserved prefix, /api/ first. A prefix must be a slash, plain segments and
// a trailing slash, and must not reserve /assets/ or a file served at the
// root. A refused value is never quoted, the error gives its position.
func checkReservedPrefixes(prefixes, served []string) ([]string, error) {
	out := []string{apiPrefix}
	var problems []error
	for i, prefix := range prefixes {
		bare := strings.TrimSuffix(prefix, "/")
		switch {
		case !reservedPrefixOK(prefix):
			problems = append(problems, fmt.Errorf("frontend: Config.App.ReservedPrefixes entry %d must be a slash, "+
				"one or more plain segments and a trailing slash, such as /auth/, with no empty, dot, percent, "+
				"backslash, brace, space or control segment and no ? or # (the value is not shown)", i+1))
		case bare == "/"+assetsDir || strings.HasPrefix(prefix, "/"+assetsDir+"/"):
			problems = append(problems, fmt.Errorf("frontend: Config.App.ReservedPrefixes entry %d is under /%s/, "+
				"where the build's files are served (the value is not shown)", i+1, assetsDir))
		case slices.Contains(served, strings.TrimPrefix(bare, "/")):
			problems = append(problems, fmt.Errorf("frontend: Config.App.ReservedPrefixes entry %d names a file that "+
				"is served at the root, robots.txt, a favicon file or a public file (the value is not shown)", i+1))
		case slices.Contains(out, prefix):
			problems = append(problems, fmt.Errorf("frontend: Config.App.ReservedPrefixes entry %d repeats %s or an "+
				"earlier entry (the value is not shown)", i+1, apiPrefix))
		default:
			out = append(out, prefix)
		}
	}
	return out, errors.Join(problems...)
}

// reservedPrefixOK reports whether prefix is a slash, one or more plain
// segments and a trailing slash, with nothing the path rules or the
// ServeMux pattern syntax would read otherwise.
func reservedPrefixOK(prefix string) bool {
	if len(prefix) < 3 || prefix[0] != '/' || !strings.HasSuffix(prefix, "/") || !utf8.ValidString(prefix) ||
		strings.ContainsAny(prefix, "?#") {
		return false
	}
	for _, seg := range strings.Split(prefix[1:len(prefix)-1], "/") {
		if !plainSegment(seg) {
			return false
		}
	}
	return true
}

// isReserved reports whether p is a reserved prefix, with or without its
// trailing slash, or lies under one.
func isReserved(p string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if p == prefix[:len(prefix)-1] || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// appBuild is what loadApp needs of the configuration.
type appBuild struct {
	boot        bool
	bootOpts    []themeboot.Option
	icons       *favicon.Icons
	noStore     bool
	robots      string
	fixed       []string
	publicFiles []string
	sourceMaps  bool
}

// loadApp reads the build of an app. It returns every problem at once,
// naming the file.
func loadApp(build fs.FS, b appBuild) (*app, error) {
	a := &app{}
	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}
	index, err := loadIndex(build, b)
	fail(err)
	a.index = index
	a.assets, err = loadAssets(build, b.sourceMaps)
	fail(err)
	a.public, err = loadPublic(build, b.fixed, b.publicFiles)
	fail(err)
	robots := b.robots
	if robots == "" {
		robots = appRobots
	}
	a.robots = newFile([]byte(robots), "text/plain; charset=utf-8", cacheHour)
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return a, nil
}

// loadIndex reads index.html and writes the favicon links and the boot
// script into it, as configured. A page without a head element is refused
// in every case: the markup would have no place, and a Vite build always
// writes one. With the favicon set, a page that carries icon links or a
// theme-color tag of its own is refused, it would serve them twice.
func loadIndex(build fs.FS, b appBuild) (*file, error) {
	body, err := readRegular(build, indexFile)
	if err != nil {
		return nil, fmt.Errorf("frontend: the build has no usable %s: %w", indexFile, err)
	}
	at := htmlhead.AfterHeadTag(string(body))
	if at < 0 {
		return nil, fmt.Errorf("frontend: %s has no head element", indexFile)
	}
	if b.icons != nil && favicon.Present(body) {
		return nil, fmt.Errorf("frontend: %s already carries icon links or a theme-color tag", indexFile)
	}
	switch {
	case b.boot:
		opts := append([]themeboot.Option(nil), b.bootOpts...)
		if b.icons != nil {
			opts = append(opts, themeboot.WithFavicon(*b.icons))
		}
		body = themeboot.InjectStatic(body, opts...)
	case b.icons != nil:
		body = []byte(string(body[:at]) + b.icons.Links() + string(body[at:]))
	}
	cache := cacheNoCache
	if b.noStore {
		cache = cacheNoStore
	}
	return newFile(body, "text/html; charset=utf-8", cache), nil
}

// plainNotFound answers 404 in plain text: an unknown asset and a path that
// fails the path rules in app mode, which has no 404 page.
func plainNotFound(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", cacheNoCache)
	w.WriteHeader(http.StatusNotFound)
	// A failed write means the client went away, there is nobody to tell.
	_, _ = io.WriteString(w, "404 page not found\n")
}
