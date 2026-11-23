// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/logging"
)

const (
	origin     = "https://example.test"
	themeColor = "#0f766e"
	hashedJS   = "assets/index-BXa3k2_9.js"
	hashedCSS  = "assets/index-Cq1x8ZzA.css"
	plainSVG   = "assets/logo.svg"
	nestedFont = "assets/fonts/inter-Ab12cd34.woff2"
	ogImage    = "og-image.png"
	secretText = "not-in-the-sitemap"
)

// routes are the decoded routes of the fixture site, without the language
// prefix: the root, the two legal pages, a non ASCII path and an ampersand,
// the two shapes whose escaped loc differs from the file name.
var routes = []string{"/", "/imprint", "/privacy", "/über", "/a&b"}

// escape mirrors encodeURIComponent of sitemapXml for the fixture routes.
func escape(p string) string {
	return strings.NewReplacer("ü", "%C3%BC", "&", "%26").Replace(p)
}

// variant is the language variant of a route.
func variant(code, route string) string {
	if route == "/" {
		return "/" + code + "/"
	}
	return "/" + code + route
}

// pageFile is the file the prerender writes for a variant.
func pageFile(v string) string {
	rel := strings.TrimPrefix(v, "/")
	if strings.HasSuffix(rel, "/") {
		return rel + "index.html"
	}
	return rel + "/index.html"
}

func html(lang, marker string) []byte {
	return []byte(`<!doctype html><html lang="` + lang + `"><head><title>t</title>` +
		`<script type="module" src="/` + hashedJS + `"></script></head><body>` + marker + `</body></html>`)
}

func sitemapOf(locs ...string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	for _, loc := range locs {
		b.WriteString("  <url><loc>" + loc + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	return []byte(b.String())
}

// buildOf returns the output of a Vite build with the prerender written into
// it, for the given routes, plus everything a server must not serve: the
// template, the Vite manifest, a dotfile, a page the sitemap does not list.
func buildOf(routes []string) fstest.MapFS {
	build := fstest.MapFS{
		"index.html":           {Data: []byte(`<!doctype html><html><head></head><body><!--app-html--></body></html>`)},
		".vite/manifest.json":  {Data: []byte(`{"secret":"manifest"}`)},
		".env":                 {Data: []byte("SECRET=1")},
		hashedJS:               {Data: []byte("console.log('app')")},
		hashedCSS:              {Data: []byte("body{}")},
		plainSVG:               {Data: []byte("<svg/>")},
		nestedFont:             {Data: []byte("wOF2")},
		"assets/.hidden.js":    {Data: []byte("hidden")},
		"assets/.git/config":   {Data: []byte("hidden")},
		favicon.SVGFile:        {Data: []byte("<svg/>")},
		favicon.PNGFile:        {Data: []byte("\x89PNG\r\n\x1a\n")},
		favicon.TouchFile:      {Data: []byte("\x89PNG\r\n\x1a\n")},
		ogImage:                {Data: []byte("\x89PNG\r\n\x1a\nog")},
		"de/secret/index.html": {Data: html("de", secretText)},
		"de/404.html":          {Data: html("de", "nicht gefunden")},
		"en/404.html":          {Data: html("en", "not found")},
	}
	var locs []string
	for _, code := range []string{"de", "en"} {
		for _, route := range routes {
			v := variant(code, route)
			build[pageFile(v)] = &fstest.MapFile{Data: html(code, "page:"+v)}
			locs = append(locs, origin+escape(v))
		}
	}
	build["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(locs...)}
	return build
}

func testConfig(build fstest.MapFS, logs io.Writer) frontend.Config {
	return frontend.Config{
		Build:       build,
		ThemeColor:  themeColor,
		PublicFiles: []string{ogImage},
		Logger:      logging.New(logs, slog.LevelInfo),
	}
}

func newServer(t *testing.T) *frontend.Server {
	t.Helper()
	s, err := frontend.New(testConfig(buildOf(routes), io.Discard))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// do sends one request through h. The target is parsed like a request line
// on the wire, so an encoded or unusual path arrives as a server sees it.
func do(t *testing.T, h http.Handler, method, target string, header http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for name, values := range header {
		req.Header[name] = values
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = res.Body.Close()
	return buf.String()
}
