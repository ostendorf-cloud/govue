// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package language

import (
	"fmt"
	"net/http"
	"slices"
)

// legalDefault is the language the German aliases redirect to.
const legalDefault = "de"

// legalRoute is one path of the legal redirect set. lang is the fixed target
// language, or empty when the target follows the visitor.
type legalRoute struct {
	path  string
	page  string
	lang  string
	terms bool
}

// legalRoutes is the whole redirect set. The targets are built from this
// table and a configured code only.
var legalRoutes = []legalRoute{
	{path: "/imprint", page: "imprint"},
	{path: "/privacy", page: "privacy"},
	{path: "/terms", page: "terms", terms: true},
	{path: "/impressum", page: "imprint", lang: legalDefault},
	{path: "/datenschutz", page: "privacy", lang: legalDefault},
	{path: "/datenschutzerklaerung", page: "privacy", lang: legalDefault},
	{path: "/agb", page: "terms", lang: legalDefault, terms: true},
}

// LegalConfig configures HandleLegal. The zero value serves German and
// English with German as the default, CookieName as the cookie and the terms
// paths included.
type LegalConfig struct {
	// Config holds the languages, the default and the cookie, with the
	// rules of Redirect. Languages must contain de, the target of the German
	// aliases.
	Config
	// WithoutTerms leaves /terms and /agb unregistered, for a site that has
	// no terms page.
	WithoutTerms bool
}

// HandleLegal registers the redirects of the legal pages on mux: /imprint,
// /privacy and /terms to the page in the visitor's language, and the German
// aliases /impressum, /datenschutz, /datenschutzerklaerung and /agb to the
// German page. A canonical page is /<code>/<page>, such as /en/imprint, the
// path the route table of the Vue package gives.
//
// Each path is registered as an exact pattern and with one trailing slash,
// each once as "GET path", which also matches HEAD, and once without a
// method, which answers 405 with an Allow header. The paths are case
// sensitive. Every answer is a 302 with Cache-Control: no-store and no body.
// A redirect that follows the visitor also carries Vary: Accept-Language,
// Cookie. The Location is built from a fixed table and a configured code
// only: the request path, the query, the Host header, the cookie value and
// the header text never reach it.
//
// wrap is applied to each handler, nil for none. The redirects need no
// session and no CSRF check, so a site mounts them outside that chain.
//
// HandleLegal returns an error wrapping ErrConfig and registers nothing when
// cfg is invalid. It panics like ServeMux.Handle when a path is registered
// already.
func HandleLegal(mux *http.ServeMux, cfg LegalConfig, wrap func(http.Handler) http.Handler) error {
	rd, err := newRedirector(cfg.Config)
	if err != nil {
		return err
	}
	if !slices.Contains(rd.languages, legalDefault) {
		return fmt.Errorf("%w: the legal pages need the language %q for the German aliases", ErrConfig, legalDefault)
	}
	if wrap == nil {
		wrap = func(next http.Handler) http.Handler { return next }
	}
	for _, route := range legalRoutes {
		if route.terms && cfg.WithoutTerms {
			continue
		}
		h := wrap(&legalRedirect{rd: rd, page: route.page, lang: route.lang})
		for _, p := range []string{route.path, route.path + "/{$}"} {
			mux.Handle("GET "+p, h)
			mux.Handle(p, h)
		}
	}
	return nil
}

// legalRedirect answers one path of the set.
type legalRedirect struct {
	rd   *redirector
	page string
	// lang is the fixed target language, empty to follow the visitor.
	lang string
}

func (l *legalRedirect) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	h := w.Header()
	lang := l.lang
	if lang == "" {
		lang = l.rd.pick(r)
		h.Add("Vary", "Accept-Language, Cookie")
	}
	h.Set("Cache-Control", "no-store")
	h.Set("Location", "/"+lang+"/"+l.page)
	w.WriteHeader(http.StatusFound)
}
