// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
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
)

// writeTestSite writes the prerender output of the fixture routes into a
// fresh directory, plus a page the sitemap does not list and a file outside
// the directory.
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
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte(secretText), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return testSite{dir: dir, secret: secretText, locs: locs}
}

// newSiteApp builds the app with SITE_DIR set, the way main would.
func newSiteApp(t *testing.T, dir string) *app {
	t.Helper()
	a, _ := newTestAppEnv(t, 1000, map[string]string{"SITE_DIR": dir})
	return a
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

func TestPostToAPageIsNotServed(t *testing.T) {
	s := writeTestSite(t)
	a := newSiteApp(t, s.dir)
	h := a.handler()

	for _, target := range []string{"/", "/de/", "/de/impressum", "/sitemap.xml"} {
		res := do(t, h, http.MethodPost, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusFound {
			t.Fatalf("POST %s = %d, want a refusal", target, res.StatusCode)
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
				// Only a listed page may answer, and never one that
				// is not the page of its own path.
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

	for _, target := range []string{"/", "/de/", "/en/", "/sitemap.xml"} {
		res := do(t, h, http.MethodGet, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 without SITE_DIR", target, res.StatusCode)
		}
	}
}

func TestNewAppFailsOnABrokenSiteDirectory(t *testing.T) {
	env := testEnv(t, 1000)
	env["SITE_DIR"] = filepath.Join(t.TempDir(), "missing")
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, err := newApp(cfg, testLogger()); err == nil {
		t.Fatal("newApp accepted a site directory without a sitemap")
	}
}

func TestLoadSite(t *testing.T) {
	langs := []string{"de", "en"}
	page := func(lang string) []byte { return []byte(pageHTML("/" + lang + "/")) }
	sitemap := func(locs ...string) *fstest.MapFile {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
		for _, l := range locs {
			b.WriteString("<url><loc>" + l + "</loc></url>")
		}
		b.WriteString("</urlset>")
		return &fstest.MapFile{Data: []byte(b.String())}
	}
	pages := func(extra fstest.MapFS) fstest.MapFS {
		fsys := fstest.MapFS{
			"de/index.html":           {Data: page("de")},
			"en/index.html":           {Data: page("en")},
			"de/impressum/index.html": {Data: []byte(pageHTML("/de/impressum"))},
			// Files of a language and a path the site does not serve. A loc
			// naming them must still be refused.
			"fr/index.html":       {Data: []byte(pageHTML("/fr/"))},
			"app/x/index.html":    {Data: []byte(pageHTML("/app/x"))},
			"de/a%25b/index.html": {Data: []byte(pageHTML("/de/a%25b"))},
			"de/{x}/index.html":   {Data: []byte(pageHTML("/de/{x}"))},
		}
		for k, v := range extra {
			fsys[k] = v
		}
		return fsys
	}

	for _, tc := range []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{
			name: "valid",
			fsys: pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/", "https://e.test/en/", "https://e.test/de/impressum")}),
		},
		{
			name:    "no sitemap",
			fsys:    pages(nil),
			wantErr: "sitemap.xml",
		},
		{
			name:    "empty sitemap",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap()}),
			wantErr: "no url",
		},
		{
			name:    "not xml",
			fsys:    pages(fstest.MapFS{"sitemap.xml": {Data: []byte("<urlset><url>")}}),
			wantErr: "sitemap.xml",
		},
		{
			name:    "listed page missing",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/", "https://e.test/de/gone")}),
			wantErr: "de/gone/index.html",
		},
		{
			name:    "dot dot segment",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/../x")}),
			wantErr: "https://e.test/de/../x",
		},
		{
			name:    "encoded dot dot segment",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/%2e%2e/x")}),
			wantErr: "%2e%2e",
		},
		{
			name:    "encoded slash",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/a%2fb")}),
			wantErr: "a%2fb",
		},
		{
			name:    "backslash",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/a%5cb")}),
			wantErr: "a%5cb",
		},
		{
			name:    "pattern syntax",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/%7Bx%7D")}),
			wantErr: "%7Bx%7D",
		},
		{
			name:    "percent in a decoded segment",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/a%2525b")}),
			wantErr: "a%2525b",
		},
		{
			name:    "language not configured",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/fr/")}),
			wantErr: "https://e.test/fr/",
		},
		{
			name:    "app path",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/app")}),
			wantErr: "https://e.test/app",
		},
		{
			name:    "app path with a file on disk",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/app/x")}),
			wantErr: "language prefix",
		},
		{
			name:    "language not configured with a file on disk",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/fr/")}),
			wantErr: "language prefix",
		},
		{
			name:    "root",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/")}),
			wantErr: "https://e.test/",
		},
		{
			name:    "trailing slash on a deep path",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/impressum/")}),
			wantErr: "https://e.test/de/impressum/",
		},
		{
			name:    "query",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/?a=1")}),
			wantErr: "https://e.test/de/?a=1",
		},
		{
			name:    "fragment",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/#a")}),
			wantErr: "https://e.test/de/#a",
		},
		{
			name:    "not absolute",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("/de/")}),
			wantErr: "/de/",
		},
		{
			name:    "duplicate",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/", "https://e.test/de/")}),
			wantErr: "twice",
		},
		{
			name:    "sitemap names itself",
			fsys:    pages(fstest.MapFS{"sitemap.xml": sitemap("https://e.test/de/", "https://e.test/sitemap.xml")}),
			wantErr: "https://e.test/sitemap.xml",
		},
		{
			name: "page without a head element",
			fsys: pages(fstest.MapFS{
				"sitemap.xml":   sitemap("https://e.test/de/"),
				"de/index.html": {Data: []byte("<p>no head</p>")},
			}),
			wantErr: "head",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := loadSite(fs.FS(tc.fsys), langs, favicon.Icons{})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("loadSite: %v", err)
				}
				if s == nil || len(s.pages) != 3 {
					t.Fatalf("site = %+v, want three pages", s)
				}
				return
			}
			if err == nil {
				t.Fatalf("loadSite accepted the directory, want an error naming %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to name %q", err, tc.wantErr)
			}
		})
	}
}

// testLogger returns a logger that writes nowhere.
func testLogger() *slog.Logger { return logging.New(io.Discard, slog.LevelInfo) }
