// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/logging"
)

// fixtureOrigin is the origin the fixture sitemap carries. Go never reads it:
// only the path of every loc counts.
const fixtureOrigin = "https://example.test"

// fixtureRoutes are the decoded public routes of the fixture site. They cover
// the root, a plain path, a non ASCII path and a path with an ampersand, the
// two shapes a percent encoded loc differs from the file name in.
var fixtureRoutes = []string{"/", "/impressum", "/über", "/a&b"}

// escapedSegments mirrors what sitemapXml does to a path: encodeURIComponent
// on every segment. The fixtures need only these two cases.
func escapedSegments(p string) string {
	p = strings.ReplaceAll(p, "ü", "%C3%BC")
	return strings.ReplaceAll(p, "&", "%26")
}

// fixtureSitemap writes what web/src/i18n/routes.ts sitemapXml writes for the
// routes: one url per language variant with its alternates and x-default.
func fixtureSitemap(routes []string) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\" xmlns:xhtml=\"http://www.w3.org/1999/xhtml\">\n")
	variant := func(code, route string) string {
		if route == "/" {
			return "/" + code + "/"
		}
		return "/" + code + route
	}
	for _, code := range []string{"de", "en"} {
		for _, route := range routes {
			b.WriteString("  <url>\n    <loc>" + fixtureOrigin + escapedSegments(variant(code, route)) + "</loc>\n")
			for _, alt := range []string{"de", "en", "x-default"} {
				target := alt
				if alt == "x-default" {
					target = "de"
				}
				b.WriteString("    <xhtml:link rel=\"alternate\" hreflang=\"" + alt + "\" href=\"" +
					fixtureOrigin + escapedSegments(variant(target, route)) + "\"/>\n")
			}
			b.WriteString("  </url>\n")
		}
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// pageFile is the file the prerender writes for a decoded variant path:
// /de/ is de/index.html and /de/impressum is de/impressum/index.html.
func pageFile(variantPath string) string {
	rel := strings.TrimPrefix(variantPath, "/")
	if strings.HasSuffix(rel, "/") {
		return rel + "index.html"
	}
	return rel + "/index.html"
}

// pageHTML is a prerendered page in miniature. The marker tells the pages
// apart, and the head has no icon links, which themeboot.Shell requires of
// a page it adds them to.
func pageHTML(variantPath string) string {
	lang := strings.Split(variantPath, "/")[1]
	return "<!doctype html><html lang=\"" + lang + "\"><head><title>t</title></head><body>page:" +
		variantPath + "</body></html>"
}

// testSite is a site directory filled like the prerender output, under a
// parent directory that holds a file outside the site.
type testSite struct {
	// dir is the site directory SITE_DIR points at.
	dir string
	// secret is the text of a file next to dir, outside the site.
	secret string
	// locs are the loc values of the sitemap, in order.
	locs []string
}

const (
	secretText  = "outside-the-site-directory"
	privateText = "inside-but-not-in-the-sitemap"
	scriptText  = "console.log('asset');"
)

// writeTestSite writes the prerender output of the fixture routes into a
// fresh directory, plus a page the sitemap does not list, build assets, a
// symbolic link out of the directory and a file outside it.
func writeTestSite(t *testing.T) testSite {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "site")
	write := func(name, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	var locs []string
	for _, code := range []string{"de", "en"} {
		for _, route := range fixtureRoutes {
			vp := "/" + code + route
			if route == "/" {
				vp = "/" + code + "/"
			}
			write(pageFile(vp), pageHTML(vp))
			locs = append(locs, fixtureOrigin+escapedSegments(vp))
		}
	}
	write("sitemap.xml", fixtureSitemap(fixtureRoutes))
	write("de/draft/index.html", privateText)
	write("private.html", privateText)
	write("assets/app-abc123.js", scriptText)
	write("assets/app-abc123.css", "body{margin:0}")
	write("assets/sub/deep.js", scriptText)
	write("assets/index.html", "<p>an asset that is named index.html</p>")
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte(secretText), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink("../../secret.txt", filepath.Join(dir, "assets", "link.js")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return testSite{dir: dir, secret: secretText, locs: locs}
}

// newSiteApp builds the app with SITE_DIR set, the way main would.
func newSiteApp(t *testing.T, dir string) *app {
	t.Helper()
	a, _ := newSiteAppLogs(t, dir, 1000)
	return a
}

// newSiteAppLogs is newSiteApp with the rate limit burst and the access log.
func newSiteAppLogs(t *testing.T, dir string, burst int) (*app, *bytes.Buffer) {
	t.Helper()
	a, logs := newTestAppEnv(t, burst, map[string]string{"SITE_DIR": dir})
	t.Cleanup(func() {
		if err := a.close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return a, logs
}

// do sends one request through the real handler. It never follows a redirect.
func do(t *testing.T, h http.Handler, method, target string, header http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func bodyOf(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// requestTarget is the target a browser sends for a loc.
func requestTarget(t *testing.T, loc string) string {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse loc %q: %v", loc, err)
	}
	return u.EscapedPath()
}

func TestEverySitemapLocAnswers200WithoutARedirect(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	if len(s.locs) != 8 {
		t.Fatalf("fixture has %d locs, want 8", len(s.locs))
	}
	for _, loc := range s.locs {
		target := requestTarget(t, loc)
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+target, func(t *testing.T) {
				res := do(t, h, method, target, nil)
				body := bodyOf(t, res)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", res.StatusCode)
				}
				if got := res.Header.Get("Location"); got != "" {
					t.Fatalf("Location = %q, want none", got)
				}
				if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
					t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", got)
				}
				if method == http.MethodHead {
					// The recorder does not drop a HEAD body the way a server does.
					return
				}
				u, _ := url.Parse(loc)
				if want := "page:" + u.Path; !strings.Contains(body, want) {
					t.Fatalf("body does not hold %q: %s", want, body)
				}
			})
		}
	}
}

func TestSitemapIsServedAsXML(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	res := do(t, h, http.MethodGet, "/sitemap.xml", nil)
	body := bodyOf(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "" {
		t.Fatalf("Location = %q, want none", got)
	}
	if got := res.Header.Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/xml; charset=utf-8", got)
	}
	if body != fixtureSitemap(fixtureRoutes) {
		t.Fatalf("body is not the file on disk: %s", body)
	}
}

func TestPagesCarryTheBootScriptFaviconAndSecurityHeaders(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	res := do(t, h, http.MethodGet, "/en/impressum", nil)
	body := bodyOf(t, res)
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "nonce-") {
		t.Fatalf("Content-Security-Policy = %q, want a nonce", csp)
	}
	nonce := csp[strings.Index(csp, "nonce-")+len("nonce-"):]
	nonce = nonce[:strings.IndexAny(nonce, "' ;")]
	if !strings.Contains(body, "<script nonce=\""+nonce+"\"") {
		t.Fatalf("boot script is not under the nonce %q: %s", nonce, body)
	}
	if n := strings.Count(body, "rel=\"icon\""); n == 0 {
		t.Fatalf("page has no favicon link: %s", body)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("no request id: the page skipped the outer chain")
	}
}

func TestBuildAssetsAreServedAsImmutableFiles(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, tc := range []struct {
		target   string
		wantType string
		wantBody string
	}{
		{"/assets/app-abc123.js", "text/javascript", scriptText},
		{"/assets/app-abc123.css", "text/css", "body{margin:0}"},
		{"/assets/sub/deep.js", "text/javascript", scriptText},
		// A file named index.html is a file, not a directory index: no redirect.
		{"/assets/index.html", "text/html", "an asset that is named index.html"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+tc.target, func(t *testing.T) {
				res := do(t, h, method, tc.target, nil)
				body := bodyOf(t, res)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", res.StatusCode)
				}
				if got := res.Header.Get("Location"); got != "" {
					t.Fatalf("Location = %q, want none", got)
				}
				if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.wantType) {
					t.Fatalf("Content-Type = %q, want prefix %q", got, tc.wantType)
				}
				if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
					t.Fatalf("Cache-Control = %q, want public, max-age=31536000, immutable", got)
				}
				if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
					t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
				}
				if method == http.MethodGet && !strings.Contains(body, tc.wantBody) {
					t.Fatalf("body = %q, want it to hold %q", body, tc.wantBody)
				}
			})
		}
	}
}

// The security middleware already sends nosniff on every response of the
// example. The asset handler sends it itself, so it holds in an application
// that mounts the handler without that middleware.
func TestAssetHandlerSendsNosniffWithoutTheChain(t *testing.T) {
	fsys := fstest.MapFS{
		"sitemap.xml":      {Data: []byte(fixtureSitemapOf("https://e.test/de/"))},
		"de/index.html":    {Data: []byte(pageHTML("/de/"))},
		"assets/app.js":    {Data: []byte(scriptText)},
		"assets/dir/x.css": {Data: []byte("a{}")},
	}
	s, err := loadSite(fsys, []string{"de", "en"}, favicon.Icons{})
	if err != nil {
		t.Fatalf("loadSite: %v", err)
	}
	h := s.assetHandler()
	for target, want := range map[string]int{
		"/assets/app.js":    http.StatusOK,
		"/assets/dir":       http.StatusNotFound,
		"/assets/dir/":      http.StatusNotFound,
		"/assets/nope.js":   http.StatusNotFound,
		"/de/index.html":    http.StatusNotFound,
		"/assets/../de/":    http.StatusNotFound,
		"/assets/dir/x.css": http.StatusOK,
	} {
		res := do(t, h, http.MethodGet, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != want {
			t.Fatalf("GET %s = %d, want %d", target, res.StatusCode, want)
		}
		if want == http.StatusOK && res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("GET %s: X-Content-Type-Options = %q, want nosniff", target, res.Header.Get("X-Content-Type-Options"))
		}
	}
}

// fixtureSitemapOf is a minimal urlset of the given locs.
func fixtureSitemapOf(locs ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><urlset xmlns="` + sitemapNamespace + `">`)
	for _, l := range locs {
		b.WriteString("<url><loc>" + l + "</loc></url>")
	}
	b.WriteString("</urlset>")
	return b.String()
}

func TestAssetsNeverListOrEscape(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, target := range []string{
		"/assets",
		"/assets/",
		"/assets/sub",
		"/assets/sub/",
		"/assets/app-abc123.js/",
		"/assets/missing.js",
		"/assets/link.js",
		"/assets/%2e%2e/secret.txt",
		"/assets/..%2fsecret.txt",
		"/assets/..%5csecret.txt",
		"/assets/%2e%2e/%2e%2e/secret.txt",
		"/assets/../secret.txt",
		"/assets/sub/../../secret.txt",
	} {
		t.Run(target, func(t *testing.T) {
			res := do(t, h, http.MethodGet, target, nil)
			body := bodyOf(t, res)
			for _, leak := range []string{s.secret, "app-abc123", "deep.js", "link.js\""} {
				if strings.Contains(body, leak) {
					t.Fatalf("status %d: body shows %q: %s", res.StatusCode, leak, body)
				}
			}
			switch res.StatusCode {
			case http.StatusNotFound:
			case http.StatusTemporaryRedirect:
				// The mux cleans a non canonical path. The target must refuse again.
				u, err := url.Parse(res.Header.Get("Location"))
				if err != nil || u.Host != "" {
					t.Fatalf("redirect to %q is not a local path", res.Header.Get("Location"))
				}
				next := do(t, h, http.MethodGet, u.RequestURI(), nil)
				if got := bodyOf(t, next); strings.Contains(got, s.secret) {
					t.Fatalf("redirect target leaks the secret")
				}
				if next.StatusCode != http.StatusNotFound {
					t.Fatalf("redirect target status = %d, want 404", next.StatusCode)
				}
			default:
				t.Fatalf("status = %d, want 404", res.StatusCode)
			}
		})
	}
}

func TestEveryRouteOfTheSiteSitsInTheRouteChain(t *testing.T) {
	s := writeTestSite(t)

	// A burst of one: the second request from the same client is refused by
	// the rate limiter, and only a handler inside the route chain sees it.
	for _, target := range []string{"/", "/de/impressum", "/sitemap.xml", "/assets/app-abc123.js"} {
		t.Run(target, func(t *testing.T) {
			a, _ := newSiteAppLogs(t, s.dir, 1)
			h := a.handler()
			first := do(t, h, http.MethodGet, target, nil)
			_ = bodyOf(t, first)
			if first.StatusCode >= 400 {
				t.Fatalf("first request status = %d, want a success or redirect", first.StatusCode)
			}
			second := do(t, h, http.MethodGet, target, nil)
			_ = bodyOf(t, second)
			if second.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("second request status = %d, want 429: the handler is outside the route chain", second.StatusCode)
			}
		})
	}
}

func TestRootRedirectIsMountedForTheRootAlone(t *testing.T) {
	s := writeTestSite(t)
	a, logs := newSiteAppLogs(t, s.dir, 1000)
	h := a.handler()

	for _, tc := range []struct {
		target    string
		wantRoute string
		wantCode  int
	}{
		{"/", "GET /{$}", http.StatusFound},
		// Another path is the catch-all's, not the redirect handler's own 404:
		// the pattern must be "GET /{$}", never "GET /".
		{"/x", "/", http.StatusNotFound},
		{"/impressum", "/", http.StatusNotFound},
	} {
		logs.Reset()
		res := do(t, h, http.MethodGet, tc.target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != tc.wantCode {
			t.Fatalf("GET %s = %d, want %d", tc.target, res.StatusCode, tc.wantCode)
		}
		lines := accessLogLines(t, logs)
		if len(lines) != 1 {
			t.Fatalf("GET %s: got %d access log lines, want 1", tc.target, len(lines))
		}
		if got := lines[0]["route"]; got != tc.wantRoute {
			t.Fatalf("GET %s route = %v, want %q", tc.target, got, tc.wantRoute)
		}
	}
}

func TestRootRedirectsToTheNegotiatedLanguage(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, tc := range []struct {
		name   string
		target string
		header http.Header
		want   string
	}{
		{"no header falls back to German", "/", nil, "/de/"},
		{"browser language", "/", http.Header{"Accept-Language": {"en-US,de;q=0.5"}}, "/en/"},
		{"cookie beats header", "/", http.Header{"Accept-Language": {"de"}, "Cookie": {"govue-language=en"}}, "/en/"},
		{"malformed header", "/", http.Header{"Accept-Language": {";;,,"}}, "/de/"},
		{"query is kept", "/?utm=a&b=1", http.Header{"Accept-Language": {"en"}}, "/en/?utm=a&b=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := do(t, h, http.MethodGet, tc.target, tc.header)
			_ = bodyOf(t, res)
			if res.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", res.StatusCode)
			}
			if got := res.Header.Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
			if got := res.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			if got := strings.Join(res.Header.Values("Vary"), ","); !strings.Contains(got, "Accept-Language") || !strings.Contains(got, "Cookie") {
				t.Fatalf("Vary = %q, want Accept-Language and Cookie", got)
			}
		})
	}
}

func TestRootRedirectGoesThroughTheRouteChain(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	// The CORS middleware adds Vary: Origin on the route chain. The language
	// redirect must add to it, not replace it.
	res := do(t, h, http.MethodGet, "/", http.Header{"Origin": {"https://app.example.test"}})
	_ = bodyOf(t, res)
	vary := strings.Join(res.Header.Values("Vary"), ",")
	if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Language") {
		t.Fatalf("Vary = %q, want Origin and Accept-Language", vary)
	}
}

func TestCrawlerKeepsTheLanguageOfTheURL(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	res := do(t, h, http.MethodGet, "/en/", http.Header{
		"Accept-Language": {"de"},
		"Cookie":          {"govue-language=de"},
	})
	body := bodyOf(t, res)
	if res.StatusCode != http.StatusOK || res.Header.Get("Location") != "" {
		t.Fatalf("status = %d, Location = %q, want 200 and none", res.StatusCode, res.Header.Get("Location"))
	}
	if !strings.Contains(body, "page:/en/") {
		t.Fatalf("body is not the English page: %s", body)
	}
}

func TestUnknownPathsAnswer404WithoutARedirect(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, target := range []string{
		"/fr/",
		"/DE/",
		"/impressum",
		"/de/nope",
		"/de/impressum/",
		"/en/impressum/",
		"/de/impressum/index.html",
		"/de/index.html",
		"/de/%C3%BCber/",
		"/de/draft",
		"/de/draft/",
		"/private.html",
		"/sitemap.xml/",
	} {
		t.Run(target, func(t *testing.T) {
			res := do(t, h, http.MethodGet, target, nil)
			body := bodyOf(t, res)
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", res.StatusCode)
			}
			if got := res.Header.Get("Location"); got != "" {
				t.Fatalf("Location = %q, want none", got)
			}
			if strings.Contains(body, privateText) {
				t.Fatalf("body leaks a file the sitemap does not list: %s", body)
			}
		})
	}
}

// A bare language prefix is no page of the sitemap. ServeMux itself sends it
// to the root of the language, with the query kept, before any handler runs.
func TestBareLanguagePrefixIsRedirectedByTheMux(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, tc := range []struct{ target, want string }{
		{"/de", "/de/"},
		{"/en?x=1", "/en/?x=1"},
	} {
		res := do(t, h, http.MethodGet, tc.target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("GET %s = %d, want 307", tc.target, res.StatusCode)
		}
		if got := res.Header.Get("Location"); got != tc.want {
			t.Fatalf("GET %s Location = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// A path the mux has to clean is answered with a 307 before any handler runs,
// so the redirect carries no Vary and no Cache-Control, and the cleaned path
// can name another language than the one the client typed. A raw client can
// reach the other language this way. The target is a listed page that answers
// 200 directly.
func TestCleanedPathIsRedirectedByTheMuxToTheCleanedPage(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	res := do(t, h, http.MethodGet, "/de/../en/", http.Header{"Accept-Language": {"de"}})
	_ = bodyOf(t, res)
	if res.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/en/" {
		t.Fatalf("Location = %q, want /en/", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control = %q, want none on the mux redirect", got)
	}
	if got := res.Header.Values("Vary"); len(got) != 0 {
		t.Fatalf("Vary = %q, want none on the mux redirect", got)
	}
	next := do(t, h, http.MethodGet, "/en/", nil)
	_ = bodyOf(t, next)
	if next.StatusCode != http.StatusOK {
		t.Fatalf("target status = %d, want 200", next.StatusCode)
	}
}

// Every path the site serves answers GET and HEAD only. A POST reaches the
// catch-all, which answers 404: no handler of the site is registered without
// its method, so language.Redirect's own 405 for other methods is never
// reached through this mux. ServeMux would answer 405 only if the catch-all
// were absent.
func TestPostToAPageIsNotServed(t *testing.T) {
	s := writeTestSite(t)
	a := newSiteApp(t, s.dir)
	h := a.handler()

	for _, target := range []string{"/", "/de/", "/de/impressum", "/sitemap.xml", "/assets/app-abc123.js"} {
		res := do(t, h, http.MethodPost, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("POST %s = %d, want 404 from the catch-all", target, res.StatusCode)
		}
	}
}

func TestPathTraversalNeverReadsOutsideTheSite(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, target := range []string{
		"/de/../secret.txt",
		"/de/../../secret.txt",
		"/../secret.txt",
		"/de/%2e%2e/secret.txt",
		"/de/%2E%2E/secret.txt",
		"/de/%2e%2e%2fsecret.txt",
		"/de/..%2fsecret.txt",
		"/de/..%5csecret.txt",
		"/de%5c..%5csecret.txt",
		"/de/..\\secret.txt",
		"/de/%2e%2e/%2e%2e/secret.txt",
		"/de/impressum/../../../secret.txt",
		"/de/impressum/%2e%2e/%2e%2e/%2e%2e/secret.txt",
		"/de/%00",
		"//secret.txt",
		"/de//impressum",
		"/de/./impressum",
		"/de/draft/../impressum/../../private.html",
	} {
		t.Run(target, func(t *testing.T) {
			res := do(t, h, http.MethodGet, target, nil)
			body := bodyOf(t, res)
			for _, leak := range []string{s.secret, privateText} {
				if strings.Contains(body, leak) {
					t.Fatalf("status %d leaks %q", res.StatusCode, leak)
				}
			}
			switch res.StatusCode {
			case http.StatusNotFound, http.StatusBadRequest:
				return
			case http.StatusMovedPermanently, http.StatusPermanentRedirect, http.StatusTemporaryRedirect, http.StatusFound:
				// The mux cleans a non canonical path with a redirect. What it
				// points at must again be a refusal or a listed page.
				loc := res.Header.Get("Location")
				u, err := url.Parse(loc)
				if err != nil || u.Host != "" {
					t.Fatalf("redirect to %q is not a local path", loc)
				}
				next := do(t, h, http.MethodGet, u.RequestURI(), nil)
				nextBody := bodyOf(t, next)
				for _, leak := range []string{s.secret, privateText} {
					if strings.Contains(nextBody, leak) {
						t.Fatalf("redirect target %q leaks %q", loc, leak)
					}
				}
			case http.StatusOK:
				if !strings.Contains(body, "page:") {
					t.Fatalf("200 for %s with a body that is no listed page: %s", target, body)
				}
			default:
				t.Fatalf("status = %d, want 404, 400 or a redirect to a refusal", res.StatusCode)
			}
		})
	}
}

func TestWithoutASiteDirectoryNoLanguageRouteExists(t *testing.T) {
	a, _ := newTestApp(t, 1000)
	h := a.handler()

	for _, target := range []string{"/", "/de/", "/en/", "/sitemap.xml", "/assets/app.js"} {
		res := do(t, h, http.MethodGet, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 without SITE_DIR", target, res.StatusCode)
		}
	}
}

// appFromEnv builds the app and returns the error instead of failing.
func appFromEnv(t *testing.T, siteDir string) (*app, error) {
	t.Helper()
	env := testEnv(t, 1000)
	env["SITE_DIR"] = siteDir
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return newApp(cfg, testLogger())
}

func TestNewAppFailsOnABrokenSiteDirectory(t *testing.T) {
	if _, err := appFromEnv(t, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("newApp accepted a site directory that does not exist")
	}
	if _, err := appFromEnv(t, t.TempDir()); err == nil {
		t.Fatal("newApp accepted a site directory without a sitemap")
	}
}

// A page that is a symbolic link out of the site directory fails startup and
// names the page. os.OpenRoot refuses the link, so the page is never read.
func TestNewAppRefusesAPageThatLinksOutOfTheSiteDirectory(t *testing.T) {
	s := writeTestSite(t)
	page := filepath.Join(s.dir, "de", "impressum", "index.html")
	if err := os.Remove(page); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// The target is a valid page, so only the link itself can be the reason
	// the load fails.
	outside := filepath.Join(filepath.Dir(s.dir), "outside.html")
	if err := os.WriteFile(outside, []byte(pageHTML("/de/impressum")), 0o600); err != nil {
		t.Fatalf("write outside page: %v", err)
	}
	if err := os.Symlink("../../../outside.html", page); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	a, err := appFromEnv(t, s.dir)
	if err == nil {
		_ = a.close()
		t.Fatal("newApp accepted a page that links out of the site directory")
	}
	if !strings.Contains(err.Error(), "de/impressum/index.html") || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("error = %v, want it to name the page and say the link escapes", err)
	}
}

// A link that stays inside the site directory is fine.
func TestNewAppAcceptsAPageThatLinksInsideTheSiteDirectory(t *testing.T) {
	s := writeTestSite(t)
	page := filepath.Join(s.dir, "de", "impressum", "index.html")
	if err := os.Remove(page); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Symlink("../../en/impressum/index.html", page); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	a, err := appFromEnv(t, s.dir)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	_ = a.close()
}

const (
	reasonURL       = "is not an absolute http or https URL"
	reasonQuery     = "has a query, a fragment or userinfo"
	reasonEncoded   = "has an encoded slash or backslash"
	reasonLanguage  = "is not under a configured language prefix"
	reasonBare      = "is a language prefix without its trailing slash"
	reasonSegment   = "has a segment that is not a plain path segment"
	reasonOrigin    = "uses another origin than the first loc"
	reasonRoot      = "must be a urlset in the sitemap namespace"
	reasonMultiLoc  = "has more than one loc"
	reasonDuplicate = "is listed twice"
)

func TestLoadSite(t *testing.T) {
	langs := []string{"de", "en"}
	const ns = "http://www.sitemaps.org/schemas/sitemap/0.9"
	doc := func(root, namespace string, urls ...string) *fstest.MapFile {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><` + root + ` xmlns="` + namespace + `">`)
		for _, u := range urls {
			b.WriteString("<url>" + u + "</url>")
		}
		b.WriteString("</" + root + ">")
		return &fstest.MapFile{Data: []byte(b.String())}
	}
	locs := func(list ...string) *fstest.MapFile {
		urls := make([]string, 0, len(list))
		for _, l := range list {
			urls = append(urls, "<loc>"+l+"</loc>")
		}
		return doc("urlset", ns, urls...)
	}
	// Every file a case names exists, so a missing file never stands in for
	// the refusal the case asserts.
	site := func(sitemap *fstest.MapFile) fstest.MapFS {
		page := func(p string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(pageHTML(p))} }
		return fstest.MapFS{
			"sitemap.xml":             sitemap,
			"de/index.html":           page("/de/"),
			"en/index.html":           page("/en/"),
			"de/impressum/index.html": page("/de/impressum"),
			"fr/index.html":           page("/fr/"),
			"app/x/index.html":        page("/app/x"),
			"de/a%25b/index.html":     page("/de/a%25b"),
			"de/{x}/index.html":       page("/de/{x}"),
			"de/a b/index.html":       page("/de/a b"),
			"de/a\x01b/index.html":    page("/de/a\x01b"),
			"de/a/b/index.html":       page("/de/a/b"),
			"de/a\\b/index.html":      page("/de/a\\b"),
		}
	}
	with := func(sitemap *fstest.MapFile, extra fstest.MapFS) fstest.MapFS {
		fsys := site(sitemap)
		for k, v := range extra {
			fsys[k] = v
		}
		return fsys
	}

	for _, tc := range []struct {
		name string
		fsys fstest.MapFS
		// wantErr is the refusal reason. Empty means the load succeeds.
		wantErr string
		// missing allows the error to be a missing file. Every other case
		// must not be one.
		missing bool
		// wantPages is the number of pages on success.
		wantPages int
	}{
		{name: "valid", fsys: site(locs("https://e.test/de/", "https://e.test/en/", "https://e.test/de/impressum")), wantPages: 3},
		{name: "white space around a loc", fsys: site(locs("\n  https://e.test/de/ \n", "\thttps://e.test/en/\t")), wantPages: 2},
		{name: "http origin", fsys: site(locs("http://localhost:8080/de/", "http://localhost:8080/en/")), wantPages: 2},
		{name: "no sitemap", fsys: fstest.MapFS{}, wantErr: "sitemap.xml", missing: true},
		{name: "not xml", fsys: site(&fstest.MapFile{Data: []byte("<urlset><url>")}), wantErr: "parse sitemap.xml"},
		{name: "empty urlset", fsys: site(locs()), wantErr: "lists no url"},
		{name: "sitemap index", fsys: site(doc("sitemapindex", ns, "<loc>https://e.test/de/</loc>")), wantErr: reasonRoot},
		{name: "no namespace", fsys: site(doc("urlset", "", "<loc>https://e.test/de/</loc>")), wantErr: reasonRoot},
		{name: "another namespace", fsys: site(doc("urlset", "http://example.test/ns", "<loc>https://e.test/de/</loc>")), wantErr: reasonRoot},
		{name: "two locs in one url", fsys: site(doc("urlset", ns, "<loc>https://e.test/de/</loc><loc>https://e.test/en/</loc>")), wantErr: reasonMultiLoc},
		{name: "url without a loc", fsys: site(doc("urlset", ns, "<lastmod>2026-01-01</lastmod>")), wantErr: "has no loc"},
		{name: "mixed host", fsys: site(locs("https://e.test/de/", "https://other.test/en/")), wantErr: reasonOrigin},
		{name: "mixed scheme", fsys: site(locs("https://e.test/de/", "http://e.test/en/")), wantErr: reasonOrigin},
		{name: "mixed port", fsys: site(locs("https://e.test/de/", "https://e.test:8443/en/")), wantErr: reasonOrigin},
		{name: "ftp scheme", fsys: site(locs("ftp://e.test/de/")), wantErr: reasonURL},
		{name: "not absolute", fsys: site(locs("/de/")), wantErr: reasonURL},
		{name: "no host", fsys: site(locs("https:///de/")), wantErr: reasonURL},
		{name: "query", fsys: site(locs("https://e.test/de/?a=1")), wantErr: reasonQuery},
		{name: "empty query", fsys: site(locs("https://e.test/de/?")), wantErr: reasonQuery},
		{name: "fragment", fsys: site(locs("https://e.test/de/#a")), wantErr: reasonQuery},
		{name: "userinfo", fsys: site(locs("https://u:p@e.test/de/")), wantErr: reasonQuery},
		{name: "lower case encoded slash", fsys: site(locs("https://e.test/de/a%2fb")), wantErr: reasonEncoded},
		{name: "upper case encoded slash", fsys: site(locs("https://e.test/de/a%2Fb")), wantErr: reasonEncoded},
		{name: "encoded backslash", fsys: site(locs("https://e.test/de/a%5cb")), wantErr: reasonEncoded},
		{name: "upper case encoded backslash", fsys: site(locs("https://e.test/de/a%5Cb")), wantErr: reasonEncoded},
		{name: "dot dot segment", fsys: site(locs("https://e.test/de/../x")), wantErr: reasonSegment},
		{name: "encoded dot dot segment", fsys: site(locs("https://e.test/de/%2e%2e/x")), wantErr: reasonSegment},
		{name: "dot segment", fsys: site(locs("https://e.test/de/./x")), wantErr: reasonSegment},
		{name: "empty segment", fsys: site(locs("https://e.test/de//x")), wantErr: reasonSegment},
		{name: "pattern syntax", fsys: site(locs("https://e.test/de/%7Bx%7D")), wantErr: reasonSegment},
		{name: "percent in a decoded segment", fsys: site(locs("https://e.test/de/a%2525b")), wantErr: reasonSegment},
		{name: "space in a segment", fsys: site(locs("https://e.test/de/a%20b")), wantErr: reasonSegment},
		{name: "control character in a segment", fsys: site(locs("https://e.test/de/a%01b")), wantErr: reasonSegment},
		{name: "trailing slash on a deep path", fsys: site(locs("https://e.test/de/impressum/")), wantErr: reasonSegment},
		{name: "bare language prefix", fsys: site(locs("https://e.test/de")), wantErr: reasonBare},
		{name: "language not configured", fsys: site(locs("https://e.test/fr/")), wantErr: reasonLanguage},
		{name: "app path with a file on disk", fsys: site(locs("https://e.test/app/x")), wantErr: reasonLanguage},
		{name: "root", fsys: site(locs("https://e.test/")), wantErr: reasonLanguage},
		{name: "sitemap names itself", fsys: site(locs("https://e.test/sitemap.xml")), wantErr: reasonLanguage},
		{name: "duplicate", fsys: site(locs("https://e.test/de/", "https://e.test/de/")), wantErr: reasonDuplicate},
		{name: "listed page missing", fsys: site(locs("https://e.test/de/gone")), wantErr: "de/gone/index.html", missing: true},
		{
			name:    "page without a head element",
			fsys:    with(locs("https://e.test/de/"), fstest.MapFS{"de/index.html": {Data: []byte("<p>no head</p>")}}),
			wantErr: "head",
		},
		{
			name:    "sitemap over the size limit",
			fsys:    site(&fstest.MapFile{Data: bytes.Repeat([]byte(" "), maxSitemapBytes+1)}),
			wantErr: "larger than",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := loadSite(fs.FS(tc.fsys), langs, favicon.Icons{})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("loadSite: %v", err)
				}
				if s == nil || len(s.pages) != tc.wantPages {
					t.Fatalf("site = %+v, want %d pages", s, tc.wantPages)
				}
				return
			}
			if err == nil {
				t.Fatalf("loadSite accepted the directory, want an error saying %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to say %q", err, tc.wantErr)
			}
			if !tc.missing && errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("error = %v: a missing file masks the refusal", err)
			}
		})
	}
}

// testLogger returns a logger that writes nowhere.
func testLogger() *slog.Logger { return logging.New(io.Discard, slog.LevelInfo) }
