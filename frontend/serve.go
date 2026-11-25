// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend

import (
	"bytes"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// serve writes f. http.ServeContent answers a matching If-None-Match with
// 304 and a HEAD without a body. The zero modification time sends no
// Last-Modified, because a build file has no meaningful one.
func (f *file) serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("Cache-Control", f.cacheControl)
	h.Set("ETag", f.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.body))
}

func (f *file) handler() http.Handler {
	return http.HandlerFunc(f.serve)
}

// mount registers every page, its slash form, the sitemap, robots.txt, the
// public files and the assets on mux, each wrapped by route.
//
// page wraps the handler of each page only, inside route: it is where a 304
// of a page is counted as a view.
func (s *site) mount(mux *http.ServeMux, route, page func(http.Handler) http.Handler) {
	for _, p := range s.pages {
		mux.Handle(p.pattern, route(page(p.file.handler())))
		mux.Handle(p.slashPattern, route(redirectTo(p.location)))
	}
	mux.Handle("GET /"+sitemapFile, route(s.sitemap.handler()))
	mux.Handle("GET /"+robotsFile, route(s.robots.handler()))
	for name, f := range s.public {
		mux.Handle("GET "+name, route(f.handler()))
	}
	mountAssets(mux, s.assets, http.HandlerFunc(s.notFoundFor), route)
}

// mountAssets registers the files under /assets/ on mux. A path under
// /assets/ that names no file answers notFound, never a page.
func mountAssets(mux *http.ServeMux, assets map[string]*file, notFound http.Handler,
	route func(http.Handler) http.Handler,
) {
	// The exact pattern keeps the mux from redirecting /assets to /assets/.
	mux.Handle("GET /"+assetsDir, route(notFound))
	mux.Handle("GET /"+assetsDir+"/{file...}", route(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := assets[r.URL.Path]
		if !ok {
			notFound.ServeHTTP(w, r)
			return
		}
		f.serve(w, r)
	})))
}

// redirectTo answers 301 to the canonical location of a page, a path from
// the sitemap. The query is kept with every unsafe byte escaped. Nothing
// else of the request reaches the Location header.
func redirectTo(location string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := location
		if q := r.URL.RawQuery; q != "" {
			target += "?" + escapeQuery(q)
		}
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusMovedPermanently)
	})
}

// catchAll answers every path no pattern matched: 405 for a method other
// than GET and HEAD, the 404 page otherwise. It is registered without a
// method, which language.HandleLegal needs.
func (s *site) catchAll() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w)
			return
		}
		s.notFoundFor(w, r)
	})
}

// methodNotAllowed answers 405 with Allow: GET, HEAD.
func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

// notFoundFor answers 404 with the 404 page of the language the first
// segment of the path names, else of the default language.
func (s *site) notFoundFor(w http.ResponseWriter, r *http.Request) {
	code := s.fallback
	first, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if _, ok := s.notFound[first]; ok {
		code = first
	}
	s.writeNotFound(w, code)
}

func (s *site) writeNotFound(w http.ResponseWriter, code string) {
	f := s.notFound[code]
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("Cache-Control", cacheNoCache)
	w.WriteHeader(http.StatusNotFound)
	// A failed write means the client went away, there is nobody to tell.
	_, _ = w.Write(f.body)
}

// canonicalOnly answers notFound for a path that is not in canonical form,
// before the mux sees it, so the mux never cleans or redirects a path itself.
// It passes every other request on unchanged. notFound is the 404 page of
// the default language for a prerendered site and the plain 404 in app mode.
func canonicalOnly(next, notFound http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !canonicalPath(r) {
			notFound.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// canonicalPath reports whether the request path is in the one form the
// server answers: absolute, valid UTF-8, no encoded slash, backslash, dot,
// percent or NUL, no decoded backslash, percent or control character, no
// empty segment but a trailing one, and no segment starting with a dot.
func canonicalPath(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/") || !utf8.ValidString(p) {
		return false
	}
	raw := r.URL.RawPath
	if raw == "" {
		raw = p
	}
	if !encodingAllowed(raw) {
		return false
	}
	if strings.IndexFunc(p, func(c rune) bool { return c == '\\' || c == '%' || unicode.IsControl(c) }) >= 0 {
		return false
	}
	segments := strings.Split(p[1:], "/")
	for i, seg := range segments {
		if seg == "" {
			if i == len(segments)-1 {
				continue
			}
			return false
		}
		if seg[0] == '.' {
			return false
		}
	}
	return true
}

// escapeQuery percent encodes every byte of a raw query that must not appear
// in a header value or a URL reference: controls, space, '#', DEL and bytes
// above ASCII. It is the rule of language.Redirect.
func escapeQuery(q string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(q))
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c <= 0x20 || c == 0x7f || c >= 0x80 || c == '#' {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
