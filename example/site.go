// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// siteLanguages are the languages of the example site. A site lists the same
// codes as web/src/i18n/catalogues.ts.
var siteLanguages = []string{"de", "en"}

// sitemapFile is the name the prerender writes the sitemap under.
const sitemapFile = "sitemap.xml"

// maxSitemapBytes bounds the sitemap read at startup: the 50 MiB limit of the
// sitemap protocol.
const maxSitemapBytes = 50 << 20

// sitePage is one page the sitemap lists, ready to serve.
type sitePage struct {
	// pattern is the ServeMux pattern for the exact path of the page.
	pattern string
	handler http.Handler
}

// site is the output of the prerender step, loaded into memory. The pages
// are known from the sitemap, so the server serves exactly those paths and
// reads no file per request: nothing a request carries is ever joined to a
// file name.
type site struct {
	pages   []sitePage
	sitemap []byte
}

// loadSite reads the prerender output in fsys. The sitemap is the list of
// pages: every loc becomes an exact path. A loc that is not a clean path
// under one of languages, or whose file is missing, fails the load. This is
// how a site learns its routes in Go without a second copy of the route
// table: the build writes them once, into the sitemap.
func loadSite(fsys fs.FS, languages []string, icons favicon.Icons) (*site, error) {
	f, err := fsys.Open(sitemapFile)
	if err != nil {
		return nil, fmt.Errorf("example: load site: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxSitemapBytes+1))
	if err != nil {
		return nil, fmt.Errorf("example: load site: read %s: %w", sitemapFile, err)
	}
	if len(raw) > maxSitemapBytes {
		return nil, fmt.Errorf("example: load site: %s is larger than %d bytes", sitemapFile, maxSitemapBytes)
	}

	var doc struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("example: load site: parse %s: %w", sitemapFile, err)
	}
	if len(doc.URLs) == 0 {
		return nil, fmt.Errorf("example: load site: %s lists no url", sitemapFile)
	}

	s := &site{sitemap: raw}
	seen := make(map[string]bool, len(doc.URLs))
	var problems []error
	for _, u := range doc.URLs {
		page, err := loadPage(fsys, strings.TrimSpace(u.Loc), languages, icons)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if seen[page.pattern] {
			problems = append(problems, fmt.Errorf("example: load site: %q is listed twice", u.Loc))
			continue
		}
		seen[page.pattern] = true
		s.pages = append(s.pages, page)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return s, nil
}

// loadPage turns one loc into its pattern and handler.
func loadPage(fsys fs.FS, loc string, languages []string, icons favicon.Icons) (page sitePage, err error) {
	bad := func(reason string) (sitePage, error) {
		return sitePage{}, fmt.Errorf("example: load site: loc %q %s", loc, reason)
	}
	u, err := url.Parse(loc)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return bad("is not an absolute http or https URL")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil {
		return bad("has a query, a fragment or userinfo")
	}
	// Path is the decoded form. The decoded text is what the route table held,
	// so it must hold nothing a file name or a ServeMux pattern would read as
	// syntax.
	p := u.Path
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if !strings.HasPrefix(p, "/") || !slices.Contains(languages, segments[0]) {
		return bad("is not under a configured language prefix")
	}
	isRoot := len(segments) == 2 && segments[1] == ""
	if isRoot {
		segments = segments[:1]
	}
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "\\{}%") ||
			strings.IndexFunc(seg, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
			return bad("has a segment that is not a plain path segment")
		}
	}
	if p == "/"+segments[0] && len(segments) == 1 {
		return bad("is a language prefix without its trailing slash")
	}

	file := strings.Join(segments, "/") + "/index.html"
	if isRoot {
		file = segments[0] + "/index.html"
	}
	if !fs.ValidPath(file) {
		return bad("does not name a file")
	}
	content, err := fs.ReadFile(fsys, file)
	if err != nil {
		return sitePage{}, fmt.Errorf("example: load site: loc %q: page %s: %w", loc, file, err)
	}

	pattern := "GET " + p
	if isRoot {
		pattern = "GET /" + segments[0] + "/{$}"
	}
	defer func() {
		// Shell panics at build time for a page without a head element or one
		// that already has icon links. A bad build output must fail startup
		// with an error naming the page, as every other problem does.
		if r := recover(); r != nil {
			page, err = sitePage{}, fmt.Errorf("example: load site: page %s: %v", file, r)
		}
	}()
	return sitePage{pattern: pattern, handler: themeboot.Shell(content, themeboot.WithFavicon(icons))}, nil
}

// sitemapHandler serves the sitemap as it was built.
func (s *site) sitemapHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		// A failed write means the client went away, there is nobody to tell.
		_, _ = w.Write(s.sitemap)
	})
}

// mount registers every page and the sitemap on mux, each wrapped by route.
// Every pattern starts with a configured language code or is the sitemap, and
// loadSite refused duplicates, so none collides with an application route.
func (s *site) mount(mux *http.ServeMux, route func(http.Handler) http.Handler) {
	for _, p := range s.pages {
		mux.Handle(p.pattern, route(p.handler))
	}
	mux.Handle("GET /"+sitemapFile, route(s.sitemapHandler()))
}
