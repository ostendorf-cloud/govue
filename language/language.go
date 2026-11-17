// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package language sends a visitor of the site root to the language variant
// of the site that suits them.
//
// A site serves its public pages under a path prefix per language, such as
// /de/ and /en/. Redirect answers GET and HEAD for / only and redirects to
// one of those prefixes. It picks the language in this order: the language
// cookie when its value is exactly one of the configured codes, then the
// Accept-Language header, then the default language.
//
// The target is built from the configured codes alone. The request path, the
// Host header, the cookie value and the header text never reach the Location
// header, so the redirect cannot be turned into an open redirect or a header
// injection. Only the query string of the request is carried over, with every
// byte that is not safe in a header escaped.
//
// Mount the handler for the exact pattern GET /{$}:
//
//	h, err := language.Redirect(language.Config{})
//	if err != nil { ... }
//	mux.Handle("GET /{$}", h)
//
// A request for a prefixed path such as /en/ never reaches the handler, and
// the handler answers 404 for any path but /, so a crawler keeps the language
// it asked for. The answer depends on the visitor, so it is a 302 with
// Cache-Control: no-store and Vary: Accept-Language, Cookie.
//
// The package imports no other govue package.
package language

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// CookieName is the default name of the cookie that holds the visitor's
// choice. It is the name the Vue package writes when its language storage
// kind is cookie, and a test in web/ keeps the two equal.
const CookieName = "govue-language"

// ErrConfig is wrapped by every error Redirect returns for an invalid Config.
var ErrConfig = errors.New("language: invalid config")

// maxLanguages bounds the configured list, so the negotiation loop stays
// small whatever the configuration holds.
const maxLanguages = 32

// codePattern is the shape of a language code: two or three lowercase
// letters. Anything else could not be a safe path segment.
var codePattern = regexp.MustCompile(`^[a-z]{2,3}$`)

// Config configures Redirect. The zero value serves German and English with
// German as the default and CookieName as the cookie.
type Config struct {
	// Languages lists the language codes the site has a prefix for. Each
	// code is two or three lowercase letters and appears once. Empty means
	// de and en.
	Languages []string
	// Default is the language used when the cookie and the header give no
	// answer. It must be in Languages. Empty means de.
	Default string
	// Cookie is the name of the cookie that holds the visitor's choice. It
	// must be an HTTP token. Empty means CookieName.
	Cookie string
}

// redirector is the handler Redirect returns.
type redirector struct {
	// languages lists the codes with the default first, so a wildcard in
	// Accept-Language resolves to the default.
	languages []string
	fallback  string
	cookie    string
}

// Redirect returns a handler that redirects / to the language variant for the
// request. It returns an error wrapping ErrConfig when cfg is invalid.
func Redirect(cfg Config) (http.Handler, error) {
	langs := cfg.Languages
	if len(langs) == 0 {
		langs = []string{"de", "en"}
	}
	def := cfg.Default
	if def == "" {
		def = "de"
	}
	cookie := cfg.Cookie
	if cookie == "" {
		cookie = CookieName
	}

	var errs []error
	if len(langs) > maxLanguages {
		errs = append(errs, fmt.Errorf("%w: at most %d languages", ErrConfig, maxLanguages))
		langs = langs[:maxLanguages]
	}
	seen := make(map[string]bool, len(langs))
	for _, code := range langs {
		switch {
		case !codePattern.MatchString(code):
			errs = append(errs, fmt.Errorf("%w: language code %q must be two or three lowercase letters", ErrConfig, code))
		case seen[code]:
			errs = append(errs, fmt.Errorf("%w: language code %q is listed twice", ErrConfig, code))
		}
		seen[code] = true
	}
	if !seen[def] {
		errs = append(errs, fmt.Errorf("%w: default language %q is not in the language list", ErrConfig, def))
	}
	if !isToken(cookie) {
		errs = append(errs, fmt.Errorf("%w: cookie name %q is not an HTTP token", ErrConfig, cookie))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	ordered := make([]string, 0, len(langs))
	ordered = append(ordered, def)
	for _, code := range langs {
		if code != def {
			ordered = append(ordered, code)
		}
	}
	return &redirector{languages: ordered, fallback: def, cookie: cookie}, nil
}

// ServeHTTP answers GET and HEAD for / with a 302 to the language prefix.
func (rd *redirector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	target := "/" + rd.pick(r) + "/"
	if q := r.URL.RawQuery; q != "" {
		target += "?" + escapeQuery(q)
	}

	h := w.Header()
	h.Add("Vary", "Accept-Language, Cookie")
	h.Set("Cache-Control", "no-store")
	h.Set("Location", target)
	w.WriteHeader(http.StatusFound)
}

// pick returns the configured code for the request. The result is always one
// of rd.languages, never text taken from the request.
func (rd *redirector) pick(r *http.Request) string {
	for _, c := range r.CookiesNamed(rd.cookie) {
		for _, code := range rd.languages {
			if c.Value == code {
				return code
			}
		}
	}
	if code, ok := Negotiate(strings.Join(r.Header.Values("Accept-Language"), ","), rd.languages); ok {
		return code
	}
	return rd.fallback
}

// escapeQuery percent encodes every byte of a raw query that must not appear
// in a header value or a URL reference: controls, space, '#', DEL and bytes
// above ASCII. The server already refuses CR and LF in a request target, so
// this is a second line.
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

// isToken reports whether s is a non empty RFC 9110 token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}
