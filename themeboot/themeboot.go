// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package themeboot ships the small inline script that sets the theme and
// the colour mode on the root element before the first paint, so a visitor
// who chose dark never sees a flash of the light page.
//
// The script reads the same storage key and writes the same two attributes
// as the useTheme composable of the web package. A test in web/ runs this
// very script and checks the key and the attribute names against the
// composable's constants, so the two halves cannot drift apart. An
// application that gives useTheme its own storageKey passes the same key
// here with WithStorageKey.
//
// A page whose Content-Security-Policy carries a nonce runs the script only
// when the tag carries that nonce. Tag and Shell take it from the caller and
// from middleware.Nonce.
package themeboot

import (
	_ "embed"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"

	"github.com/ostendorf-cloud/govue/middleware"
)

// StorageKey is the default localStorage key the choice is kept under. It is
// the default key of useTheme.
const StorageKey = "govue-theme"

// ThemeAttribute and ModeAttribute are the attributes the stylesheets switch
// on, set on the root element.
const (
	ThemeAttribute = "data-theme"
	ModeAttribute  = "data-mode"
)

//go:embed boot.js
var script string

// keyPattern is what a storage key may look like. It holds nothing that
// could end the JavaScript string it is placed in or the script element.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type config struct {
	storageKey string
}

// Option changes how the script is built.
type Option func(*config)

// WithStorageKey makes the script read the choice from key instead of
// StorageKey. It must equal the storageKey given to useTheme. A key outside
// letters, digits and . _ : - up to 64 characters panics, because the key is
// written into the script and a wrong one is a programming error found at
// startup, not at request time.
func WithStorageKey(key string) Option {
	if !keyPattern.MatchString(key) {
		panic(fmt.Sprintf("themeboot: invalid storage key %q", key))
	}
	return func(c *config) { c.storageKey = key }
}

func build(opts []Option) config {
	c := config{storageKey: StorageKey}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Script returns the JavaScript source of the boot script, without a script
// tag around it.
func Script(opts ...Option) string {
	c := build(opts)
	if c.storageKey == StorageKey {
		return script
	}
	return strings.Replace(script, "'"+StorageKey+"'", "'"+c.storageKey+"'", 1)
}

// Tag returns the inline script element carrying nonce. An empty nonce
// panics: under a nonce policy a script without one is blocked silently,
// and the flash it was meant to prevent comes back with no error anywhere.
func Tag(nonce string, opts ...Option) string {
	if nonce == "" {
		panic("themeboot: empty nonce, is the security middleware outside this handler?")
	}
	return `<script nonce="` + html.EscapeString(nonce) + `">` + Script(opts...) + "</script>"
}

// Inject returns page with the boot script placed right after the opening
// head tag, ahead of every stylesheet, so the attributes are set before the
// first paint. Only a real head tag counts: not a header element and not a
// tag inside a comment. A page without a head element is returned unchanged.
// An empty nonce panics, see Tag.
func Inject(page []byte, nonce string, opts ...Option) []byte {
	at := afterHeadTag(string(page))
	if at < 0 {
		return page
	}
	return []byte(string(page[:at]) + Tag(nonce, opts...) + string(page[at:]))
}

// afterHeadTag returns the index just after the opening head tag, or -1.
//
// The scan runs on the original bytes with an ASCII only case fold. Lower
// casing the page would change its length for some non-ASCII runes, such as
// U+0130, and shift the returned index.
func afterHeadTag(s string) int {
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				return -1
			}
			i += 4 + end + 3
		case hasPrefixASCIIFold(s[i:], "<head") && i+5 < len(s) &&
			(s[i+5] == '>' || s[i+5] == ' ' || s[i+5] == '\t' ||
				s[i+5] == '\n' || s[i+5] == '\r' || s[i+5] == '\f'):
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return -1
			}
			return i + end + 1
		default:
			i++
		}
	}
	return -1
}

// hasPrefixASCIIFold reports whether s starts with the lower case ASCII
// prefix, ignoring ASCII case only.
func hasPrefixASCIIFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// Shell serves page, ordinarily the built index.html of the frontend, with
// the boot script injected under the response's own Content-Security-Policy
// nonce. It belongs inside the security middleware, which generates the
// nonce. Without one a request panics, see Tag, so the middleware that
// recovers panics answers it instead of a page that flashes. The response is
// marked no-store: a response carrying a nonce must never be served again
// from a cache.
func Shell(page []byte, opts ...Option) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := Inject(page, middleware.Nonce(r.Context()), opts...)
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		// A failed write means the client went away, there is nobody to tell.
		_, _ = w.Write(body)
	})
}
