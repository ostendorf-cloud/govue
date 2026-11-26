// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// appIndex is the index.html vite build writes for a single page app: a
// head with the module script and the stylesheet of the bundle, and an empty
// mount point. It carries no inline script.
const appIndex = `<!doctype html>
<html lang="de">
  <head>
    <meta charset="UTF-8" />
    <title>solobill</title>
    <script type="module" crossorigin src="/assets/index-BXa3k2_9.js"></script>
    <link rel="stylesheet" crossorigin href="/assets/index-Cq1x8ZzA.css">
  </head>
  <body><div id="app"></div></body>
</html>
`

// appBuildOf is the output of vite build for an app: index.html and assets/
// and nothing a server must serve beyond them, plus a dotfile and the Vite
// manifest a server must never serve.
func appBuildOf() fstest.MapFS {
	return fstest.MapFS{
		"index.html":          {Data: []byte(appIndex)},
		hashedJS:              {Data: []byte("console.log('app')")},
		hashedCSS:             {Data: []byte("body{}")},
		plainSVG:              {Data: []byte("<svg/>")},
		".vite/manifest.json": {Data: []byte(`{"secret":"manifest"}`)},
		".env":                {Data: []byte("SECRET=1")},
	}
}

// withFavicons adds the favicon set and a public file to an app build.
func withFavicons(build fstest.MapFS) fstest.MapFS {
	build[favicon.SVGFile] = &fstest.MapFile{Data: []byte("<svg/>")}
	build[favicon.PNGFile] = &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n")}
	build[favicon.TouchFile] = &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n")}
	return build
}

func appConfig(build fstest.MapFS, logs io.Writer) frontend.Config {
	cfg := testConfig(build, logs)
	cfg.ThemeColor = ""
	cfg.PublicFiles = nil
	cfg.App = &frontend.AppConfig{}
	return cfg
}

func newApp(t *testing.T, tune func(*frontend.Config)) *frontend.Server {
	t.Helper()
	cfg := appConfig(appBuildOf(), io.Discard)
	if tune != nil {
		tune(&cfg)
	}
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestAppStartsWithIndexAndAssetsOnly(t *testing.T) {
	t.Parallel()
	build := fstest.MapFS{
		"index.html": {Data: []byte(appIndex)},
		hashedJS:     {Data: []byte("x")},
	}
	if _, err := frontend.New(appConfig(build, io.Discard)); err != nil {
		t.Fatalf("New refused an app build of index.html and assets/: %v", err)
	}
	// The same build is no prerendered site: without App it is refused.
	_, err := frontend.New(testConfig(withFavicons(build), io.Discard))
	if err == nil || !strings.Contains(err.Error(), "no sitemap.xml") {
		t.Errorf("without App the app build gave %v, want the missing sitemap", err)
	}
}

func TestAppRefusesAnUnusableIndex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		change   func(fstest.MapFS)
		themed   bool
		want     string
		notExist bool
	}{
		{
			"no index.html", func(b fstest.MapFS) { delete(b, "index.html") }, false,
			"the build has no usable index.html", true,
		},
		{"index.html without head", func(b fstest.MapFS) {
			b["index.html"] = &fstest.MapFile{Data: []byte(`<div id="app"></div><header>x</header>`)}
		}, false, "index.html has no head element", false},
		{"head only in a comment", func(b fstest.MapFS) {
			b["index.html"] = &fstest.MapFile{Data: []byte(`<!-- <head> --><body></body>`)}
		}, false, "index.html has no head element", false},
		{"index.html is a symbolic link", func(b fstest.MapFS) {
			b["real.html"] = b["index.html"]
			b["index.html"] = &fstest.MapFile{Data: []byte("real.html"), Mode: fs.ModeSymlink}
		}, false, "the build has no usable index.html: not a regular file", false},
		{"index.html is a directory", func(b fstest.MapFS) {
			delete(b, "index.html")
			b["index.html/x.html"] = &fstest.MapFile{Data: []byte(appIndex)}
		}, false, "the build has no usable index.html: not a regular file", false},
		{"own icons with the favicon set", func(b fstest.MapFS) {
			b["index.html"] = &fstest.MapFile{Data: []byte(`<html><head><link rel="icon" href="/x.svg"></head></html>`)}
		}, true, "index.html already carries icon links", false},
		{
			"missing favicon file", func(b fstest.MapFS) { delete(b, favicon.TouchFile) }, true,
			favicon.TouchFile, false,
		},
		{"asset with unknown extension", func(b fstest.MapFS) {
			b["assets/page.html"] = &fstest.MapFile{Data: []byte("<html>")}
		}, false, "asset assets/page.html has an extension that is not served", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			build := withFavicons(appBuildOf())
			tt.change(build)
			cfg := appConfig(build, io.Discard)
			if tt.themed {
				cfg.ThemeColor = themeColor
			}
			_, err := frontend.New(cfg)
			if err == nil {
				t.Fatal("New accepted the build")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not say %q:\n%v", tt.want, err)
			}
			if errors.Is(err, fs.ErrNotExist) != tt.notExist {
				t.Errorf("errors.Is(err, fs.ErrNotExist) = %v, want %v", !tt.notExist, tt.notExist)
			}
		})
	}
}

func TestAppAnswersIndexForEveryRouteOfTheApp(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	for _, target := range []string{
		"/", "/documents/42", "/documents/42/", "/t/acme/login", "/t/acme/login?next=%2Fdocuments",
		"/index.html", "/favicon.svg", "/robots", "/apidocs", "/API/v1/x", "/assetsx/y", "/de/über",
		"/main.js", "/favicon.ico", "/Assets/index-BXa3k2_9.js",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			res := do(t, h, http.MethodGet, target, nil)
			got := body(t, res)
			if res.StatusCode != http.StatusOK || got != appIndex {
				t.Fatalf("status %d, body %q, want 200 with index.html unchanged", res.StatusCode, got)
			}
			for name, want := range map[string]string{
				"Content-Type":  "text/html; charset=utf-8",
				"Cache-Control": "no-cache",
			} {
				if res.Header.Get(name) != want {
					t.Errorf("%s = %q, want %q", name, res.Header.Get(name), want)
				}
			}
			if etag := res.Header.Get("ETag"); !strings.HasPrefix(etag, `"`) || len(etag) != 66 {
				t.Errorf("ETag = %q, want a strong quoted SHA-256", etag)
			}
			if res.Header.Get("Location") != "" {
				t.Errorf("the fallback redirected to %q", res.Header.Get("Location"))
			}
		})
	}
}

func TestAppIndexWithNoStore(t *testing.T) {
	t.Parallel()
	h := newApp(t, func(c *frontend.Config) { c.App.NoStore = true }).Handler()
	res := do(t, h, http.MethodGet, "/documents/42", nil)
	if got := body(t, res); res.StatusCode != http.StatusOK || got != appIndex {
		t.Fatalf("status %d, want 200 with index.html", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	// The assets keep their own rule.
	if cc := do(t, h, http.MethodGet, "/"+hashedJS, nil).Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q under NoStore", cc)
	}
}

func TestAppIndexAnswersHeadAndRevalidates(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	head := do(t, h, http.MethodHead, "/documents/42", nil)
	if head.StatusCode != http.StatusOK || body(t, head) != "" {
		t.Errorf("HEAD status = %d, want 200 without a body", head.StatusCode)
	}
	if head.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("HEAD Content-Type = %q", head.Header.Get("Content-Type"))
	}
	etag := do(t, h, http.MethodGet, "/", nil).Header.Get("ETag")
	again := do(t, h, http.MethodGet, "/t/acme/login", http.Header{"If-None-Match": {etag}})
	if again.StatusCode != http.StatusNotModified || body(t, again) != "" {
		t.Errorf("status = %d, want 304 without a body", again.StatusCode)
	}
}

func TestAppServesAssetsByTheirRules(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	tests := []struct {
		path, contentType, cache string
	}{
		{hashedJS, "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{hashedCSS, "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{plainSVG, "image/svg+xml", "no-cache"},
		{"robots.txt", "text/plain; charset=utf-8", "public, max-age=3600"},
	}
	for _, tt := range tests {
		res := do(t, h, http.MethodGet, "/"+tt.path, nil)
		_ = body(t, res)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d", tt.path, res.StatusCode)
			continue
		}
		if got := res.Header.Get("Content-Type"); got != tt.contentType {
			t.Errorf("%s: Content-Type = %q, want %q", tt.path, got, tt.contentType)
		}
		if got := res.Header.Get("Cache-Control"); got != tt.cache {
			t.Errorf("%s: Cache-Control = %q, want %q", tt.path, got, tt.cache)
		}
		if res.Header.Get("ETag") == "" {
			t.Errorf("%s: no ETag", tt.path)
		}
	}
}

// TestAppAnswersAPlain404 covers every path that must never become
// index.html: an unknown asset, the assets directory, and a path that fails
// the path rules, a dotfile and the Vite manifest included.
func TestAppAnswersAPlain404(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	for _, target := range []string{
		"/assets", "/assets/", "/assets/missing.js", "/assets/index-BXa3k2_9.js/", "/assets/fonts/",
		"/.env", "/.vite/manifest.json", "/.well-known/security.txt", "/documents/%2e%2e/x",
		"/documents/a%2fb", "//evil.com", "/documents//42", "/a\\b", "/documents/%00",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			res := do(t, h, http.MethodGet, target, nil)
			got := body(t, res)
			if res.StatusCode != http.StatusNotFound || got != "404 page not found\n" {
				t.Fatalf("status %d, body %q, want the plain 404", res.StatusCode, got)
			}
			if ct := res.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
				t.Errorf("Content-Type = %q", ct)
			}
			if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
				t.Errorf("Cache-Control = %q", cc)
			}
			if strings.Contains(got, "SECRET") || strings.Contains(got, "manifest") || res.Header.Get("Location") != "" {
				t.Errorf("served a file of the build or redirected")
			}
		})
	}
}

// apiError is the error shape of the api package.
type apiError struct {
	Error struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func wantJSONNotFound(t *testing.T, res *http.Response) {
	t.Helper()
	got := body(t, res)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, body %q, want the JSON 404", res.StatusCode, got)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var e apiError
	if err := json.Unmarshal([]byte(got), &e); err != nil || e.Error.Code != "not_found" {
		t.Errorf("body %q is not the not_found error shape: %v", got, err)
	}
	if e.Error.RequestID == "" {
		t.Errorf("the error carries no request id: %s", got)
	}
}

func TestAppReservesTheAPIPrefix(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete} {
		for _, target := range []string{"/api/v1/unknown", "/api/", "/api", "/api/v1/documents?x=1"} {
			t.Run(method+" "+target, func(t *testing.T) {
				t.Parallel()
				res := do(t, h, method, target, nil)
				if method == http.MethodHead {
					// A HEAD carries the status and the headers only.
					_ = body(t, res)
					if res.StatusCode != http.StatusNotFound || res.Header.Get("Content-Type") != "application/json; charset=utf-8" {
						t.Errorf("status %d, Content-Type %q", res.StatusCode, res.Header.Get("Content-Type"))
					}
					return
				}
				wantJSONNotFound(t, res)
			})
		}
	}
}

func TestAppReservesTheConfiguredPrefixes(t *testing.T) {
	t.Parallel()
	h := newApp(t, func(c *frontend.Config) {
		c.App.ReservedPrefixes = []string{"/auth/", "/oauth2/callback/"}
	}).Handler()
	for _, target := range []string{"/auth/oidc/callback?code=x", "/auth", "/auth/", "/oauth2/callback/x", "/oauth2/callback"} {
		wantJSONNotFound(t, do(t, h, http.MethodGet, target, nil))
	}
	// A path that only starts with the same letters is a route of the app.
	for _, target := range []string{"/authors", "/oauth2", "/oauth2/other"} {
		if res := do(t, h, http.MethodGet, target, nil); res.StatusCode != http.StatusOK || body(t, res) != appIndex {
			t.Errorf("%s: status %d, want index.html", target, res.StatusCode)
		}
	}
}

func TestAppForwardsTheAPIPrefixInDevelopment(t *testing.T) {
	t.Parallel()
	up := &upstream{}
	h := newApp(t, func(c *frontend.Config) {
		c.Development = true
		c.DevAPIProxy = "http://api:8081"
		c.DevAPITransport = up
		c.App.ReservedPrefixes = []string{"/auth/"}
	}).Handler()
	res := do(t, h, http.MethodPost, "/api/v1/documents", nil)
	if got := body(t, res); res.StatusCode != http.StatusOK || got != `{"ok":true}` {
		t.Errorf("/api/ through the proxy: status %d, body %q", res.StatusCode, got)
	}
	if req, _ := up.request(); req == nil || req.URL.Path != "/api/v1/documents" {
		t.Errorf("the API got %v, want /api/v1/documents", req)
	}
	// Only /api/ is forwarded: the other prefixes stay the JSON 404.
	wantJSONNotFound(t, do(t, h, http.MethodGet, "/auth/oidc/callback", nil))
	wantJSONNotFound(t, do(t, h, http.MethodGet, "/api", nil))
	if res := do(t, h, http.MethodGet, "/documents/42", nil); body(t, res) != appIndex {
		t.Error("a route of the app does not answer index.html beside the proxy")
	}
}

func TestAppAppliesTheDevAPIBodyLimit(t *testing.T) {
	t.Parallel()
	const limit = 1024
	tune := func(max int64) func(*frontend.Config) {
		return func(c *frontend.Config) {
			c.Development = true
			c.DevAPIProxy = "http://api:8081"
			c.DevAPITransport = &upstream{}
			c.DevAPIMaxBody = max
		}
	}
	h := newApp(t, tune(limit)).Handler()
	for _, tc := range []struct {
		name   string
		size   int
		status int
	}{
		{"at the limit", limit, http.StatusOK},
		{"above the limit", limit + 1, http.StatusRequestEntityTooLarge},
	} {
		res := send(t, h, http.MethodPost, "/api/v1/documents",
			bytes.NewReader(bytes.Repeat([]byte("x"), tc.size)), nil, "203.0.113.7:4000")
		_ = body(t, res)
		if res.StatusCode != tc.status {
			t.Errorf("%s: status %d, want %d", tc.name, res.StatusCode, tc.status)
		}
	}
	cfg := appConfig(appBuildOf(), io.Discard)
	tune(-1)(&cfg)
	_, err := frontend.New(cfg)
	if err == nil || !strings.Contains(err.Error(), "Config.DevAPIMaxBody") {
		t.Errorf("a negative limit in app mode gave %v, want a Config.DevAPIMaxBody error", err)
	}
}

func TestAppAnswersOtherMethodsWith405(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		for _, target := range []string{"/", "/documents/42", "/" + hashedJS, "/robots.txt"} {
			res := do(t, h, method, target, nil)
			_ = body(t, res)
			if res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, target, res.StatusCode)
			}
			if allow := res.Header.Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s: Allow = %q", method, target, allow)
			}
		}
	}
}

func TestAppRobots(t *testing.T) {
	t.Parallel()
	if got := body(t, do(t, newApp(t, nil).Handler(), http.MethodGet, "/robots.txt", nil)); got != "User-agent: *\nDisallow: /\n" {
		t.Errorf("default robots.txt = %q, want every path disallowed", got)
	}
	const custom = "User-agent: *\nAllow: /\n"
	h := newApp(t, func(c *frontend.Config) { c.App.Robots = custom }).Handler()
	if got := body(t, do(t, h, http.MethodGet, "/robots.txt", nil)); got != custom {
		t.Errorf("robots.txt = %q, want the configured body", got)
	}
}

func TestAppSendsTheSecurityHeadersWithNoInlineScript(t *testing.T) {
	t.Parallel()
	h := newApp(t, nil).Handler()
	targets := []struct{ method, target string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/documents/42"},
		{http.MethodHead, "/documents/42"},
		{http.MethodGet, "/" + hashedJS},
		{http.MethodGet, "/assets/missing.js"},
		{http.MethodGet, "/robots.txt"},
		{http.MethodGet, "/api/v1/unknown"},
		{http.MethodGet, "/.env"},
		{http.MethodPost, "/documents/42"},
	}
	for _, tt := range targets {
		res := do(t, h, tt.method, tt.target, nil)
		got := body(t, res)
		want := map[string]string{
			"Strict-Transport-Security":    "max-age=31536000; includeSubDomains; preload",
			"X-Content-Type-Options":       "nosniff",
			"X-Frame-Options":              "DENY",
			"Referrer-Policy":              "strict-origin-when-cross-origin",
			"Permissions-Policy":           middleware.DefaultPermissionsPolicy,
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
		}
		for name, value := range want {
			if v := res.Header.Get(name); v != value {
				t.Errorf("%s %s: %s = %q, want %q", tt.method, tt.target, name, v, value)
			}
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.HasPrefix(csp, "default-src 'none'; script-src 'self' 'nonce-") {
			t.Errorf("%s %s: CSP = %q, want the default with no hash", tt.method, tt.target, csp)
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "'sha256-") {
			t.Errorf("%s %s: the policy allows an inline script: %q", tt.method, tt.target, csp)
		}
		if strings.Contains(got, "<script>") {
			t.Errorf("%s %s: the answer carries an inline script", tt.method, tt.target)
		}
		if res.Header.Get("Set-Cookie") != "" || res.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s %s: a cookie or no request id", tt.method, tt.target)
		}
	}
}

func TestAppWithThemeBootAndFavicons(t *testing.T) {
	t.Parallel()
	cfg := appConfig(withFavicons(appBuildOf()), io.Discard)
	cfg.ThemeColor = themeColor
	cfg.ThemeColorDark = "#000000"
	cfg.ThemeBoot = []themeboot.Option{themeboot.WithDefaultMode(themeboot.ModeDark)}
	cfg.App.ThemeBoot = true
	cfg.PublicFiles = nil
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/documents/42", nil)
	page := body(t, res)
	script := "<script>" + themeboot.Script(cfg.ThemeBoot...) + "</script>"
	if !strings.Contains(page, `<head>`+`<link rel="icon" href="/favicon.svg"`) || !strings.Contains(page, script) {
		t.Fatalf("index.html lacks the icon links and the boot script after the head tag:\n%s", page)
	}
	if strings.Index(page, script) < strings.Index(page, `media="(prefers-color-scheme: dark)"`) {
		t.Error("the boot script comes before the theme-color tags it pins")
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' "+themeboot.Hash(cfg.ThemeBoot...)) {
		t.Errorf("script-src lacks the hash of the served script: %s", csp)
	}
	for _, name := range []string{favicon.SVGFile, favicon.PNGFile, favicon.TouchFile} {
		res := do(t, s.Handler(), http.MethodGet, "/"+name, nil)
		if got := body(t, res); res.StatusCode != http.StatusOK || got == appIndex {
			t.Errorf("/%s: status %d, want the favicon file", name, res.StatusCode)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=86400" {
			t.Errorf("/%s: Cache-Control = %q", name, cc)
		}
	}
}

func TestAppWithFaviconsOnly(t *testing.T) {
	t.Parallel()
	cfg := appConfig(withFavicons(appBuildOf()), io.Discard)
	cfg.ThemeColor = themeColor
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/", nil)
	page := body(t, res)
	icons, err := favicon.Load(withFavicons(appBuildOf()), themeColor)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(appIndex, "<head>", "<head>"+icons.Links(), 1); page != want {
		t.Errorf("index.html = %q, want the icon links right after the head tag", page)
	}
	if strings.Contains(page, "<script>") {
		t.Error("the boot script was injected without App.ThemeBoot")
	}
	if csp := res.Header.Get("Content-Security-Policy"); strings.Contains(csp, "'sha256-") {
		t.Errorf("a hash without the boot script: %s", csp)
	}
}

func TestAppWithoutFaviconsServesNoFaviconFile(t *testing.T) {
	t.Parallel()
	// The favicon files are in the build, but without ThemeColor they are
	// no files of the app: the path is a route of the router.
	cfg := appConfig(withFavicons(appBuildOf()), io.Discard)
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/"+favicon.SVGFile, nil)
	if got := body(t, res); got != appIndex {
		t.Errorf("/%s without ThemeColor = %q, want index.html", favicon.SVGFile, got)
	}
}

func TestAppServesPublicFiles(t *testing.T) {
	t.Parallel()
	build := appBuildOf()
	build[ogImage] = &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\nog")}
	cfg := appConfig(build, io.Discard)
	cfg.PublicFiles = []string{ogImage}
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/"+ogImage, nil)
	if got := body(t, res); res.StatusCode != http.StatusOK || got != "\x89PNG\r\n\x1a\nog" {
		t.Errorf("/%s: status %d, want the file", ogImage, res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestAppWithoutThemeColorServesItsOwnFaviconAndSitemap(t *testing.T) {
	t.Parallel()
	build := withFavicons(appBuildOf())
	build["favicon.ico"] = &fstest.MapFile{Data: []byte("ico")}
	build["sitemap.xml"] = &fstest.MapFile{Data: []byte("<urlset/>")}
	cfg := appConfig(build, io.Discard)
	cfg.PublicFiles = []string{favicon.SVGFile, favicon.PNGFile, "favicon.ico", "sitemap.xml"}
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatalf("New refused the app's own favicon and sitemap: %v", err)
	}
	for name, want := range map[string]string{
		favicon.SVGFile: "image/svg+xml", favicon.PNGFile: "image/png",
		"favicon.ico": "image/x-icon", "sitemap.xml": "application/xml; charset=utf-8",
	} {
		res := do(t, s.Handler(), http.MethodGet, "/"+name, nil)
		if got := body(t, res); res.StatusCode != http.StatusOK || got != string(build[name].Data) {
			t.Errorf("/%s: status %d, body %q, want the file", name, res.StatusCode, got)
		}
		if ct := res.Header.Get("Content-Type"); ct != want {
			t.Errorf("/%s: Content-Type = %q, want %q", name, ct, want)
		}
	}
	// No favicon links are written without ThemeColor.
	if page := body(t, do(t, s.Handler(), http.MethodGet, "/", nil)); page != appIndex {
		t.Errorf("index.html changed without ThemeColor: %s", page)
	}
}

func TestAppPublicFilesServedAlready(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		themed bool
		files  []string
		value  string
		want   string
	}{
		{
			"favicon with ThemeColor", true,
			[]string{favicon.SVGFile},
			favicon.SVGFile,
			"Config.PublicFiles entry 1 names a file that is served already, robots.txt or a favicon file, " +
				"which Config.ThemeColor serves",
		},
		{
			"touch icon with ThemeColor", true,
			[]string{"a.txt", favicon.TouchFile},
			favicon.TouchFile,
			"Config.PublicFiles entry 2 names a file that is served already, robots.txt or a favicon file",
		},
		{"robots without ThemeColor", false, []string{"robots.txt"}, "", "Config.PublicFiles entry 1 names a file " +
			"that is served already, robots.txt (the value is not shown)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := appConfig(withFavicons(appBuildOf()), io.Discard)
			if tt.themed {
				cfg.ThemeColor = themeColor
			}
			cfg.PublicFiles = tt.files
			_, err := frontend.New(cfg)
			if tt.value == "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %v, want %q", err, tt.want)
				}
				return
			}
			configtexttest.AssertHidden(t, err, tt.value, tt.want)
		})
	}
}

// TestPrerenderedSiteStillRefusesTheFilesItServes pins the PublicFiles
// check of the prerendered site, which app mode does not change.
func TestPrerenderedSiteStillRefusesTheFilesItServes(t *testing.T) {
	t.Parallel()
	const want = "names a file that is served already, a favicon file, the sitemap or robots.txt " +
		"(the value is not shown)"
	for _, name := range []string{favicon.SVGFile, favicon.PNGFile, favicon.TouchFile, "sitemap.xml", "robots.txt"} {
		cfg := testConfig(buildOf(routes), io.Discard)
		cfg.PublicFiles = []string{name}
		_, err := frontend.New(cfg)
		if err == nil || !strings.Contains(err.Error(), "Config.PublicFiles entry 1 "+want) {
			t.Errorf("%s: error = %v, want the refusal", name, err)
		}
	}
}

func TestAppRefusesTheOptionsOfAPrerenderedSite(t *testing.T) {
	t.Parallel()
	// An inert client is refused too: the field is the site's decision to
	// count, which app mode cannot honour.
	views := &analytics.Client{}
	tests := []struct {
		name   string
		change func(*frontend.Config)
		want   string
	}{
		{
			"language", func(c *frontend.Config) { c.Language = language.Config{Default: "en", Languages: []string{"en"}} },
			"Config.Language is refused in app mode",
		},
		{
			"language cookie", func(c *frontend.Config) { c.Language = language.Config{Cookie: "lang"} },
			"Config.Language is refused in app mode",
		},
		{
			"redirects", func(c *frontend.Config) { c.Redirects = []frontend.Redirect{{From: "/a", To: "/b"}} },
			"Config.Redirects is refused in app mode",
		},
		{"analytics", func(c *frontend.Config) { c.Analytics = views }, "Config.Analytics is refused in app mode"},
		{
			"dark colour alone", func(c *frontend.Config) { c.ThemeColorDark = "#000000" },
			"Config.ThemeColorDark needs Config.ThemeColor",
		},
		{"boot options without the boot script", func(c *frontend.Config) {
			c.ThemeBoot = []themeboot.Option{themeboot.WithStorageKey("k")}
		}, "Config.ThemeBoot needs Config.App.ThemeBoot"},
		{"policy without script-src and the boot script", func(c *frontend.Config) {
			security := middleware.DefaultSecurity()
			security.Policy = middleware.NewPolicy().Set("default-src", "'self'")
			c.Security = &security
			c.App.ThemeBoot = true
		}, "sets no script-src"},
		{"no build", func(c *frontend.Config) { c.Build = nil }, "Config.Build is required"},
		{"no logger", func(c *frontend.Config) { c.Logger = nil }, "Config.Logger is required"},
		{
			"proxy outside development", func(c *frontend.Config) { c.DevAPIProxy = "http://api:8081" },
			"Config.DevAPIProxy is for development only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := appConfig(appBuildOf(), io.Discard)
			tt.change(&cfg)
			_, err := frontend.New(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestAppAcceptsAPolicyWithoutScriptSrcWithoutTheBootScript(t *testing.T) {
	t.Parallel()
	security := middleware.DefaultSecurity()
	security.Policy = middleware.NewPolicy().Set("default-src", "'self'")
	h := newApp(t, func(c *frontend.Config) { c.Security = &security }).Handler()
	if csp := do(t, h, http.MethodGet, "/", nil).Header.Get("Content-Security-Policy"); csp != "default-src 'self'" {
		t.Errorf("CSP = %q, want the site's policy unchanged", csp)
	}
}

func TestAppRefusesAReservedPrefixWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	const secret = "hunter2"
	tests := []struct {
		name     string
		prefixes []string
		value    string
		want     string
	}{
		{"no trailing slash", []string{"/" + secret}, secret, "ReservedPrefixes entry 1 must be"},
		{"no leading slash", []string{secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"root", []string{"/"}, "", "ReservedPrefixes entry 1 must be"},
		{"empty segment", []string{"/auth//" + secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"dot segment", []string{"/auth/../" + secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"dotfile", []string{"/." + secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"percent", []string{"/a%2f" + secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"pattern syntax", []string{"/{" + secret + "}/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"query", []string{"/auth?" + secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		{"space", []string{"/a " + secret + "/"}, secret, "ReservedPrefixes entry 1 must be"},
		// The fixed names /assets/ and /api/ are part of the message itself.
		{"assets", []string{"/auth/", "/assets/"}, "", "ReservedPrefixes entry 2 is under /assets/"},
		{"under assets", []string{"/assets/" + secret + "/"}, secret, "ReservedPrefixes entry 1 is under /assets/"},
		{"robots.txt", []string{"/robots.txt/"}, "/robots.txt/", "ReservedPrefixes entry 1 names a file"},
		{"api again", []string{"/api/"}, "", "ReservedPrefixes entry 1 repeats /api/"},
		{"twice", []string{"/" + secret + "/", "/" + secret + "/"}, secret, "ReservedPrefixes entry 2 repeats"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := appConfig(appBuildOf(), io.Discard)
			cfg.App.ReservedPrefixes = tt.prefixes
			_, err := frontend.New(cfg)
			if tt.value == "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %v, want %q", err, tt.want)
				}
				return
			}
			configtexttest.AssertHidden(t, err, tt.value, tt.want)
		})
	}
}

func TestAppReservedPrefixMayNotShadowAServedFile(t *testing.T) {
	t.Parallel()
	cfg := appConfig(withFavicons(appBuildOf()), io.Discard)
	cfg.ThemeColor = themeColor
	cfg.App.ReservedPrefixes = []string{"/" + favicon.SVGFile + "/"}
	_, err := frontend.New(cfg)
	configtexttest.AssertHidden(t, err, favicon.SVGFile, "ReservedPrefixes entry 1 names a file")
}

func TestAppAccessLogCarriesThePatternNotThePath(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	cfg := appConfig(appBuildOf(), &logs)
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The sentinels are no bare numbers: the scrape holds Go runtime values, and one of them can
	// contain the digits of a bare number by chance.
	_ = body(t, do(t, s.Handler(), http.MethodGet, "/documents/id-4711-secret?token=abc", nil))
	_ = body(t, do(t, s.Handler(), http.MethodGet, "/api/v1/customers/id-4712-secret", nil))
	var routes []any
	for _, raw := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(raw, &entry) == nil && entry["route"] != nil {
			routes = append(routes, entry["route"])
		}
	}
	if len(routes) != 2 || routes[0] != "GET /" || routes[1] != "/api/" {
		t.Errorf("routes = %v, want the fallback pattern and /api/", routes)
	}
	if strings.Contains(logs.String(), "id-4711-secret") || strings.Contains(logs.String(), "id-4712-secret") ||
		strings.Contains(logs.String(), "token=abc") {
		t.Errorf("the log holds the path or the query:\n%s", logs.String())
	}
	scrape := body(t, do(t, s.AdminHandler(), http.MethodGet, "/metrics", nil))
	if !strings.Contains(scrape, `route="GET /"`) || strings.Contains(scrape, "id-4711-secret") {
		t.Errorf("the metrics do not count the fallback by its pattern:\n%s", scrape)
	}
}

func TestAppAdminHandlerServesProbesOnly(t *testing.T) {
	t.Parallel()
	s := newApp(t, nil)
	for _, target := range []string{"/healthz", "/readyz", "/metrics"} {
		if res := do(t, s.AdminHandler(), http.MethodGet, target, nil); res.StatusCode != http.StatusOK {
			t.Errorf("admin %s: status = %d", target, res.StatusCode)
		}
		// On the public handler they are routes of the app, never the probe.
		if got := body(t, do(t, s.Handler(), http.MethodGet, target, nil)); got != appIndex {
			t.Errorf("public %s = %q, want index.html", target, got)
		}
	}
	s.Health().Drain()
	if res := do(t, s.AdminHandler(), http.MethodGet, "/readyz", nil); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz after Drain: status = %d, want 503", res.StatusCode)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Errorf("Close without analytics: %v", err)
	}
}

func TestAppRateLimitAppliesToEveryRoute(t *testing.T) {
	t.Parallel()
	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{
		Limiter:  ratelimit.New(1, time.Hour, 100),
		ClientIP: clientip.NewResolver(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(t, func(c *frontend.Config) { c.RateLimit = limit }).Handler()
	targets := []string{"/documents/42", "/" + hashedJS, "/robots.txt", "/api/v1/x", "/assets/missing.js"}
	for _, target := range targets {
		_ = body(t, do(t, h, http.MethodGet, target, nil))
	}
	for _, target := range targets {
		if res := do(t, h, http.MethodGet, target, nil); res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%s after the burst: status = %d, want 429", target, res.StatusCode)
		}
	}
}

// TestPrerenderedSiteIsUnchangedByAppMode pins the answers of a prerendered
// site, App nil, on the paths app mode answers differently.
func TestPrerenderedSiteIsUnchangedByAppMode(t *testing.T) {
	t.Parallel()
	h := newServer(t).Handler()
	for target, want := range map[string]int{
		"/documents/42":   http.StatusNotFound,
		"/api/v1/unknown": http.StatusNotFound,
		"/api":            http.StatusNotFound,
		"/index.html":     http.StatusNotFound,
		"/de/imprint/":    http.StatusMovedPermanently,
		"/":               http.StatusFound,
	} {
		res := do(t, h, http.MethodGet, target, nil)
		got := body(t, res)
		if res.StatusCode != want {
			t.Errorf("%s: status %d, want %d", target, res.StatusCode, want)
		}
		if want == http.StatusNotFound && (!strings.Contains(got, "nicht gefunden") ||
			res.Header.Get("Content-Type") != "text/html; charset=utf-8") {
			t.Errorf("%s: %q is not the HTML 404 page", target, got)
		}
	}
	if got := body(t, do(t, h, http.MethodGet, "/robots.txt", nil)); !strings.HasPrefix(got, "User-agent: *\nAllow: /\n") {
		t.Errorf("robots.txt = %q, want the prerendered one", got)
	}
}
