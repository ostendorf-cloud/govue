// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/themeboot"
)

func TestEveryLocAnswersItsPageWithNoCacheAndAnETag(t *testing.T) {
	h := newServer(t).Handler()
	for _, code := range []string{"de", "en"} {
		for _, route := range routes {
			v := variant(code, route)
			t.Run(v, func(t *testing.T) {
				res := do(t, h, http.MethodGet, escape(v), nil)
				got := body(t, res)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", res.StatusCode)
				}
				if !strings.Contains(got, "page:"+v+"<") {
					t.Errorf("body is not the page of %s: %s", v, got)
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
				if res.Header.Get("Location") != "" || res.Header.Get("Last-Modified") != "" {
					t.Errorf("unexpected Location or Last-Modified: %v", res.Header)
				}
			})
		}
	}
}

func TestPageRevalidatesWithItsETag(t *testing.T) {
	h := newServer(t).Handler()
	first := do(t, h, http.MethodGet, "/de/imprint", nil)
	_ = body(t, first)
	etag := first.Header.Get("ETag")

	again := do(t, h, http.MethodGet, "/de/imprint", http.Header{"If-None-Match": {etag}})
	if again.StatusCode != http.StatusNotModified || body(t, again) != "" {
		t.Errorf("status = %d, want 304 without a body", again.StatusCode)
	}
	if other := do(t, h, http.MethodGet, "/en/imprint", nil).Header.Get("ETag"); other == etag {
		t.Error("two pages share one ETag")
	}
}

func TestHeadAnswersWithoutABody(t *testing.T) {
	h := newServer(t).Handler()
	res := do(t, h, http.MethodHead, "/de/", nil)
	if res.StatusCode != http.StatusOK || body(t, res) != "" {
		t.Errorf("HEAD status = %d, want 200 without a body", res.StatusCode)
	}
}

func TestPagesCarryTheBootScriptAllowedByTheirHash(t *testing.T) {
	h := newServer(t).Handler()
	for _, target := range []string{"/de/", "/en/privacy", "/de/missing"} {
		res := do(t, h, http.MethodGet, target, nil)
		page := body(t, res)
		const open = "<head><script>"
		start := strings.Index(page, open)
		end := strings.Index(page, "</script>")
		if start < 0 || end < start {
			t.Fatalf("%s: no boot script right after the head tag: %s", target, page)
		}
		script := page[start+len(open) : end]
		if script != themeboot.Script() {
			t.Errorf("%s: the inline script is not the boot script", target)
		}
		sum := sha256.Sum256([]byte(script))
		source := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self' "+source) {
			t.Errorf("%s: script-src does not allow the inline script %s: %s", target, source, csp)
		}
		if strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s: the policy allows any inline script: %s", target, csp)
		}
		if !strings.Contains(page, `<link rel="icon" href="/favicon.svg"`) {
			t.Errorf("%s: no favicon link: %s", target, page)
		}
	}
}

func TestThemeBootOptionsReachScriptAndHash(t *testing.T) {
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.ThemeBoot = []themeboot.Option{themeboot.WithStorageKey("site:theme")}
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/de/", nil)
	if !strings.Contains(body(t, res), "getItem('site:theme')") {
		t.Error("the page does not read the configured storage key")
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, themeboot.Hash(cfg.ThemeBoot...)) {
		t.Errorf("the policy does not carry the hash of the configured script: %s", csp)
	}
}

func TestConfiguredSecurityIsNotChanged(t *testing.T) {
	security := middleware.DefaultSecurity()
	security.HSTS = 0
	before := security.Policy.String()
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Security = &security
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/de/", nil)
	if res.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS = 0 of the site's Security is not applied")
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), themeboot.Hash()) {
		t.Error("the site's policy did not get the hash")
	}
	if security.Policy.String() != before {
		t.Errorf("New changed the site's own policy: %s", security.Policy.String())
	}
}

func TestEveryResponseCarriesTheSecurityHeadersAndNoCookie(t *testing.T) {
	h := newServer(t).Handler()
	targets := []struct{ method, target string }{
		{http.MethodGet, "/de/"},
		{http.MethodGet, "/" + hashedJS},
		{http.MethodGet, "/" + favicon.SVGFile},
		{http.MethodGet, "/sitemap.xml"},
		{http.MethodGet, "/robots.txt"},
		{http.MethodGet, "/"},
		{http.MethodGet, "/de/imprint/"},
		{http.MethodGet, "/impressum"},
		{http.MethodGet, "/de/missing"},
		{http.MethodGet, "/de/%2e%2e/x"},
		{http.MethodPost, "/de/"},
		{http.MethodGet, "/api/v1/notes"},
	}
	for _, tt := range targets {
		res := do(t, h, tt.method, tt.target, nil)
		_ = body(t, res)
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
			if got := res.Header.Get(name); got != value {
				t.Errorf("%s %s: %s = %q, want %q", tt.method, tt.target, name, got, value)
			}
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.HasPrefix(csp, "default-src 'none'; script-src 'self' "+themeboot.Hash()+" 'nonce-") {
			t.Errorf("%s %s: CSP = %q", tt.method, tt.target, csp)
		}
		if res.Header.Get("Set-Cookie") != "" {
			t.Errorf("%s %s sets a cookie", tt.method, tt.target)
		}
		if res.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s %s carries no request id", tt.method, tt.target)
		}
	}
}

func TestAssetsAreCachedByTheirName(t *testing.T) {
	h := newServer(t).Handler()
	tests := []struct {
		path, contentType, cache string
	}{
		{hashedJS, "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{hashedCSS, "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{nestedFont, "font/woff2", "public, max-age=31536000, immutable"},
		{plainSVG, "image/svg+xml", "no-cache"},
		{favicon.SVGFile, "image/svg+xml", "public, max-age=86400"},
		{favicon.PNGFile, "image/png", "public, max-age=86400"},
		{ogImage, "image/png", "public, max-age=86400"},
		{"sitemap.xml", "application/xml; charset=utf-8", "public, max-age=3600"},
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

func TestSitemapIsServedAsBuiltAndRobotsNamesIt(t *testing.T) {
	build := buildOf(routes)
	s, err := frontend.New(testConfig(build, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if got := body(t, do(t, s.Handler(), http.MethodGet, "/sitemap.xml", nil)); got != string(build["sitemap.xml"].Data) {
		t.Errorf("sitemap changed on the way:\n%s", got)
	}
	want := "User-agent: *\nAllow: /\n\nSitemap: " + origin + "/sitemap.xml\n"
	if got := body(t, do(t, s.Handler(), http.MethodGet, "/robots.txt", nil)); got != want {
		t.Errorf("robots.txt = %q, want %q", got, want)
	}
}

func TestRootRedirectsByAcceptLanguage(t *testing.T) {
	h := newServer(t).Handler()
	for header, want := range map[string]string{"en-GB,en;q=0.9": "/en/", "de-AT": "/de/", "fr": "/de/", "": "/de/"} {
		res := do(t, h, http.MethodGet, "/", http.Header{"Accept-Language": {header}})
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != want {
			t.Errorf("Accept-Language %q: %d to %q, want 302 to %q", header, res.StatusCode, res.Header.Get("Location"), want)
		}
		if vary := res.Header.Values("Vary"); !strings.Contains(strings.Join(vary, ","), "Accept-Language") {
			t.Errorf("Accept-Language %q: Vary = %v", header, vary)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("Accept-Language %q: Cache-Control = %q", header, res.Header.Get("Cache-Control"))
		}
	}
}

func TestSlashFormRedirectsToTheCanonicalPath(t *testing.T) {
	h := newServer(t).Handler()
	tests := map[string]string{
		"/de":                  "/de/",
		"/en":                  "/en/",
		"/de/imprint/":         "/de/imprint",
		"/en/%C3%BCber/":       "/en/%C3%BCber",
		"/de/a%26b/":           "/de/a%26b",
		"/de/imprint/?x=1%202": "/de/imprint?x=1%202",
		"/de?utm=a&b=ü":        "/de/?utm=a&b=%C3%BC",
	}
	for target, want := range tests {
		res := do(t, h, http.MethodGet, target, nil)
		if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q, want 301 to %q", target, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
}

func TestLegalAliasesRedirectToServedPages(t *testing.T) {
	h := newServer(t).Handler()
	for alias, want := range map[string]string{"/impressum": "/de/imprint", "/datenschutz": "/de/privacy", "/privacy": "/de/privacy"} {
		res := do(t, h, http.MethodGet, alias, nil)
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q, want 302 to %q", alias, res.StatusCode, res.Header.Get("Location"), want)
			continue
		}
		if page := do(t, h, http.MethodGet, want, nil); page.StatusCode != http.StatusOK {
			t.Errorf("%s leads to %s, which answers %d", alias, want, page.StatusCode)
		}
	}
	if res := do(t, h, http.MethodGet, "/agb", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("/agb without a terms page: status = %d, want 404", res.StatusCode)
	}
}

func TestTermsInEveryLanguageMountTheTermsAliases(t *testing.T) {
	s, err := frontend.New(testConfig(buildOf(append([]string{"/terms"}, routes...)), io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/agb", nil)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/de/terms" {
		t.Errorf("/agb: %d to %q, want 302 to /de/terms", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestUnknownPathsAnswerThe404PageOfTheirLanguage(t *testing.T) {
	h := newServer(t).Handler()
	tests := map[string]string{
		"/en/missing":          "not found",
		"/de/missing":          "nicht gefunden",
		"/fr/":                 "nicht gefunden",
		"/about":               "nicht gefunden",
		"/index.html":          "nicht gefunden",
		"/de/index.html":       "nicht gefunden",
		"/de/secret":           "nicht gefunden",
		"/de/secret/":          "nicht gefunden",
		"/de/404.html":         "nicht gefunden",
		"/en/imprint/x":        "not found",
		"/assets":              "nicht gefunden",
		"/assets/":             "nicht gefunden",
		"/assets/fonts":        "nicht gefunden",
		"/assets/fonts/":       "nicht gefunden",
		"/assets/missing.js":   "nicht gefunden",
		"/api/v1/notes":        "nicht gefunden",
		"/favicon.ico":         "nicht gefunden",
		"/sitemap.xml/":        "nicht gefunden",
		"/" + hashedJS + "/":   "nicht gefunden",
		"/terms":               "nicht gefunden",
		"/de/über":             "page:/de/über",
		"/healthz":             "nicht gefunden",
		"/metrics":             "nicht gefunden",
		"/readyz":              "nicht gefunden",
		"/de/imprint/index":    "nicht gefunden",
		"/de/imprint.html":     "nicht gefunden",
		"/DE/imprint":          "nicht gefunden",
		"/de/IMPRINT":          "nicht gefunden",
		"/en/404":              "not found",
		"/en/assets/" + "x.js": "not found",
	}
	for target, want := range tests {
		res := do(t, h, http.MethodGet, target, nil)
		got := body(t, res)
		if want == "page:/de/über" {
			// A raw UTF-8 path is the same page as its encoded form.
			if res.StatusCode != http.StatusOK || !strings.Contains(got, want) {
				t.Errorf("%s: %d %s", target, res.StatusCode, got)
			}
			continue
		}
		if res.StatusCode != http.StatusNotFound || !strings.Contains(got, want) {
			t.Errorf("%s: status %d, body %q, want 404 with %q", target, res.StatusCode, got, want)
		}
		if strings.Contains(got, secretText) || res.Header.Get("Location") != "" {
			t.Errorf("%s: served something the sitemap does not list or redirected", target)
		}
		if res.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", target, res.Header.Get("Cache-Control"))
		}
	}
}

func TestNoAPIAndNoOtherMethod(t *testing.T) {
	h := newServer(t).Handler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		for _, target := range []string{"/de/", "/de/imprint", "/" + hashedJS, "/api/v1/notes", "/imprint", "/missing"} {
			res := do(t, h, method, target, nil)
			_ = body(t, res)
			if res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, target, res.StatusCode)
			}
			if allow := res.Header.Get("Allow"); !strings.Contains(allow, "GET") || strings.Contains(allow, method) {
				t.Errorf("%s %s: Allow = %q", method, target, allow)
			}
		}
	}
}

func TestAdminHandlerServesProbesAndMetricsOnly(t *testing.T) {
	s := newServer(t)
	_ = body(t, do(t, s.Handler(), http.MethodGet, "/de/", nil))

	admin := s.AdminHandler()
	for _, target := range []string{"/healthz", "/readyz"} {
		if res := do(t, admin, http.MethodGet, target, nil); res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d", target, res.StatusCode)
		}
	}
	metricsBody := body(t, do(t, admin, http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricsBody, `route="GET /de/{$}"`) {
		t.Errorf("the metrics do not count the page by its pattern:\n%s", metricsBody)
	}
	if res := do(t, admin, http.MethodGet, "/de/", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("the admin handler serves a page: %d", res.StatusCode)
	}

	s.Health().Drain()
	if res := do(t, admin, http.MethodGet, "/readyz", nil); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz after Drain: status = %d, want 503", res.StatusCode)
	}
}

func TestAccessLogCarriesThePatternAndTheClient(t *testing.T) {
	var logs bytes.Buffer
	cfg := testConfig(buildOf(routes), &logs)
	cfg.ClientIP = clientip.NewResolver(nil)
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, do(t, s.Handler(), http.MethodGet, "/en/%C3%BCber?token=abc", nil))

	var line map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(raw, &entry) == nil && entry["route"] != nil {
			line = entry
		}
	}
	if line == nil {
		t.Fatalf("no access log line:\n%s", logs.String())
	}
	if line["route"] != "GET /en/über" {
		t.Errorf("route = %v, want the pattern", line["route"])
	}
	if strings.Contains(logs.String(), "token=abc") {
		t.Error("the access log holds the query")
	}
	if line["client"] == nil {
		t.Errorf("the access log has no client: %v", line)
	}
}

// sourceMap is a hidden source map of the kind Vite's build.sourcemap
// 'hidden' writes: the original sources, no reference from the bundle.
const sourceMap = `{"version":3,"sources":["src/secret.ts"],"sourcesContent":["const apiKey = 'original source'"]}`

func buildWithSourceMap() fstest.MapFS {
	build := buildOf(routes)
	build[hashedJS+".map"] = &fstest.MapFile{Data: []byte(sourceMap)}
	build["assets/fonts/inter-Ab12cd34.woff2.map"] = &fstest.MapFile{Data: []byte(sourceMap)}
	return build
}

func TestSourceMapsAreNotServedByDefault(t *testing.T) {
	s, err := frontend.New(testConfig(buildWithSourceMap(), io.Discard))
	if err != nil {
		t.Fatalf("New refused a build with source maps: %v", err)
	}
	for _, target := range []string{"/" + hashedJS + ".map", "/assets/fonts/inter-Ab12cd34.woff2.map"} {
		res := do(t, s.Handler(), http.MethodGet, target, nil)
		got := body(t, res)
		if res.StatusCode != http.StatusNotFound || strings.Contains(got, "original source") {
			t.Errorf("%s: status %d, body %q, want 404 without the sources", target, res.StatusCode, got)
		}
	}
}

func TestSourceMapsAreServedWithTheOptIn(t *testing.T) {
	cfg := testConfig(buildWithSourceMap(), io.Discard)
	cfg.ServeSourceMaps = true
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, s.Handler(), http.MethodGet, "/"+hashedJS+".map", nil)
	if got := body(t, res); res.StatusCode != http.StatusOK || got != sourceMap {
		t.Fatalf("status %d, body %q, want the map", res.StatusCode, got)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		// index-BXa3k2_9.js.map has no hash right before its extension.
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestSourceMapIsNoPublicFile(t *testing.T) {
	cfg := testConfig(buildWithSourceMap(), io.Discard)
	cfg.PublicFiles = []string{"app.js.map"}
	cfg.ServeSourceMaps = true
	_, err := frontend.New(cfg)
	if err == nil || !strings.Contains(err.Error(), "Config.PublicFiles entry 1") {
		t.Errorf("error = %v, want the refused PublicFiles entry", err)
	}
}

func TestHashedName(t *testing.T) {
	for name, want := range map[string]bool{
		"index-BXa3k2_9.js":       true,
		"index-Ab-3k2_9.css":      true,
		"chunk.Ab12cd34.js":       true,
		"inter-Ab12cd34.woff2":    true,
		"logo.svg":                false,
		"index.js":                false,
		"polyfills-legacy.js":     false,
		"vendor-abcdefgh.js":      false,
		"index-BXa3k2_.js":        false,
		"indexBXa3k2_9.js":        false,
		"index-BXa3k2_9X.js":      false,
		"-BXa3k2_9.js":            false,
		"index-BXa3k2!9.js":       false,
		"pow.worker-Dk3k2LmN.mjs": true,
	} {
		if got := frontend.HashedName(name); got != want {
			t.Errorf("HashedName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRateLimitAppliesToEveryRoute(t *testing.T) {
	cfg := testConfig(buildOf(routes), io.Discard)
	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{
		Limiter:  ratelimit.New(1, time.Hour, 100),
		ClientIP: clientip.NewResolver(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.RateLimit = limit
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/de/", "/" + hashedJS, "/", "/impressum", "/missing"} {
		_ = body(t, do(t, s.Handler(), http.MethodGet, target, nil))
	}
	_ = body(t, do(t, s.Handler(), http.MethodGet, "/de/", nil))
	for _, target := range []string{"/de/", "/" + hashedJS, "/", "/impressum", "/missing"} {
		if res := do(t, s.Handler(), http.MethodGet, target, nil); res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%s after the burst: status = %d, want 429", target, res.StatusCode)
		}
	}
}
