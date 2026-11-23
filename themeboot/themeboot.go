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
// from middleware.Nonce. A page served as static bytes, with an ETag, cannot
// carry a nonce: InjectStatic writes the tag without one, and Hash is the
// script-src source that lets it run.
package themeboot

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"

	"github.com/ostendorf-cloud/govue/favicon"
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
	icons      *favicon.Icons
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

// WithFavicon makes Inject and Shell write the favicon links and the theme
// colour meta tag into the head, right behind the boot script, so every page
// they serve carries them, in every language. icons comes from favicon.Load,
// which has already failed for a missing file. Tag and Script are not
// affected, they only ever carry the script. The page must not carry the
// links itself.
func WithFavicon(icons favicon.Icons) Option {
	return func(c *config) { c.icons = &icons }
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
// first paint. With WithFavicon the icon links follow the script. Only a
// real head tag counts: not a header element and not a tag inside a comment.
// A page without a head element is returned unchanged.
// An empty nonce panics, see Tag.
func Inject(page []byte, nonce string, opts ...Option) []byte {
	at := afterHeadTag(string(page))
	if at < 0 {
		return page
	}
	return insert(page, at, Tag(nonce, opts...), opts)
}

// InjectStatic is Inject for a page that is served as the same bytes on
// every response, such as a prerendered page with an ETag: the script tag
// carries no nonce, because a nonce in a cached or revalidated body would be
// a reused nonce. Under a policy that carries a nonce or a hash, the script
// then runs only when script-src also carries Hash with the same options.
// The frontend package does both. A page without a head element is returned
// unchanged.
func InjectStatic(page []byte, opts ...Option) []byte {
	at := afterHeadTag(string(page))
	if at < 0 {
		return page
	}
	return insert(page, at, "<script>"+Script(opts...)+"</script>", opts)
}

// insert places tag, and with WithFavicon the icon links behind it, at at.
func insert(page []byte, at int, tag string, opts []Option) []byte {
	if c := build(opts); c.icons != nil {
		tag += c.icons.Links()
	}
	return []byte(string(page[:at]) + tag + string(page[at:]))
}

// Hash returns the Content-Security-Policy source that allows the script
// InjectStatic writes, 'sha256-' and the base64 SHA-256 of Script with the
// same options, quotes included, ready for Policy.Add("script-src", ...). It
// allows exactly that script text and nothing else, wherever it appears.
func Hash(opts ...Option) string {
	sum := sha256.Sum256([]byte(Script(opts...)))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
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
//
// With WithFavicon, Shell panics when it is built, at startup, for a page
// without a head element, which would serve no icons, and for a page that
// already carries icon links or a theme-color tag, which would serve them
// twice. Inject itself stays total and leaves such a page unchanged, so the
// guarantee for a prerendered site is favicon.CheckPages in its tests.
func Shell(page []byte, opts ...Option) http.Handler {
	if build(opts).icons != nil {
		if afterHeadTag(string(page)) < 0 {
			panic("themeboot: WithFavicon on a page without a head element, it would serve no icons")
		}
		if favicon.Present(page) {
			panic("themeboot: WithFavicon on a page that already has icon links or a theme-color tag")
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := Inject(page, middleware.Nonce(r.Context()), opts...)
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		// A failed write means the client went away, there is nobody to tell.
		_, _ = w.Write(body)
	})
}
