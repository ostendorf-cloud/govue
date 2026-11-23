// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package themeboot_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/themeboot"
)

const page = `<!doctype html><html lang="en"><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><div id="app"></div></body></html>`

func TestShellCarriesTheNonceOfTheCSP(t *testing.T) {
	h := middleware.DefaultSecurity().Middleware(themeboot.Shell([]byte(page)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	scriptSrc := regexp.MustCompile(`script-src [^;]*`).FindString(csp)
	m := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(scriptSrc)
	if m == nil {
		t.Fatalf("script-src carries no nonce: %q", csp)
	}
	nonce := m[1]

	body := rec.Body.String()
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) {
		t.Errorf("the page has no inline script with the header's nonce %q: %s", nonce, body)
	}
	if got := strings.Count(csp, "'nonce-"+nonce+"'"); got != 2 {
		t.Errorf("nonce %q appears %d times in the policy, want 2 (script-src and style-src): %q", nonce, got, csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("the policy allows unsafe-inline: %q", csp)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestShellUsesAFreshNoncePerResponse(t *testing.T) {
	h := middleware.DefaultSecurity().Middleware(themeboot.Shell([]byte(page)))
	nonces := map[string]bool{}
	for range 3 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		m := regexp.MustCompile(`<script nonce="([^"]+)">`).FindStringSubmatch(rec.Body.String())
		if m == nil {
			t.Fatal("no nonce on the script tag")
		}
		nonces[m[1]] = true
	}
	if len(nonces) != 3 {
		t.Errorf("got %d distinct nonces for 3 responses", len(nonces))
	}
}

func TestInjectPlacesTheScriptFirstInTheHead(t *testing.T) {
	got := string(themeboot.Inject([]byte(page), "abc"))
	scriptAt := strings.Index(got, "<script")
	linkAt := strings.Index(got, "<link")
	headAt := strings.Index(got, "<head>")
	if headAt < 0 || scriptAt != headAt+len("<head>") {
		t.Errorf("the script does not follow the head tag directly: %s", got)
	}
	if linkAt < scriptAt {
		t.Errorf("the stylesheet comes before the script: %s", got)
	}
}

func TestInjectLeavesAPageWithoutHeadAlone(t *testing.T) {
	const fragment = "<p>no head</p>"
	if got := string(themeboot.Inject([]byte(fragment), "abc")); got != fragment {
		t.Errorf("got %q, want the page unchanged", got)
	}
}

func TestInjectSkipsHeaderElementsAndComments(t *testing.T) {
	const doc = `<!-- <head> in a comment --><header>x</header><HEAD lang="en"><title>t</title></HEAD>`
	got := string(themeboot.Inject([]byte(doc), "abc"))
	want := `<!-- <head> in a comment --><header>x</header><HEAD lang="en"><script nonce="abc">`
	if !strings.HasPrefix(got, want) {
		t.Errorf("the script is not right after the real head tag: %s", got)
	}
	const noHead = `<header>x</header><!-- <head> -->`
	if got := string(themeboot.Inject([]byte(noHead), "abc")); got != noHead {
		t.Errorf("got %q, want the page unchanged", got)
	}
}

func TestInjectIgnoresNonASCIIBeforeTheHead(t *testing.T) {
	// U+0130 lower cases to a different byte length, which shifted the
	// insertion point when the page was lower cased before scanning.
	const doc = "<!-- İİ äö€ -->İ<head><title>t</title></head>"
	got := string(themeboot.Inject([]byte(doc), "abc"))
	want := "<!-- İİ äö€ -->İ<head><script nonce=\"abc\">"
	if !strings.HasPrefix(got, want) {
		t.Errorf("the script is not right after the head tag: %s", got)
	}
	if !strings.HasSuffix(got, "</script><title>t</title></head>") {
		t.Errorf("the page after the head tag was altered: %s", got)
	}
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	fn()
}

func TestEmptyNonceFailsLoudly(t *testing.T) {
	mustPanic(t, "Tag", func() { themeboot.Tag("") })
	mustPanic(t, "Inject", func() { themeboot.Inject([]byte(page), "") })

	// Without the security middleware the request carries no nonce. The
	// panic is what the recover middleware turns into a 500.
	h := themeboot.Shell([]byte(page))
	mustPanic(t, "Shell", func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	})
}

func TestWithStorageKey(t *testing.T) {
	src := themeboot.Script(themeboot.WithStorageKey("my-app:theme.v2"))
	if !strings.Contains(src, "getItem('my-app:theme.v2')") || strings.Contains(src, "'govue-theme'") {
		t.Errorf("the key was not substituted: %s", src)
	}
	if !strings.Contains(themeboot.Tag("n", themeboot.WithStorageKey("k")), "getItem('k')") {
		t.Error("Tag ignores the storage key")
	}
	if got := string(themeboot.Inject([]byte(page), "n", themeboot.WithStorageKey("k"))); !strings.Contains(got, "getItem('k')") {
		t.Error("Inject ignores the storage key")
	}

	for _, bad := range []string{"", "a'b", `a"b`, `a\b`, "a b", "</script>", strings.Repeat("a", 65)} {
		mustPanic(t, "WithStorageKey("+bad+")", func() { themeboot.WithStorageKey(bad) })
	}
}

func TestTagEscapesTheNonce(t *testing.T) {
	got := themeboot.Tag(`a"><b`)
	if strings.Contains(got, `a"><b`) || !strings.Contains(got, "a&#34;&gt;&lt;b") {
		t.Errorf("the nonce is not escaped: %s", got)
	}
}

func TestWithFavicon(t *testing.T) {
	public := fstest.MapFS{
		favicon.SVGFile:   {Data: []byte("<svg/>")},
		favicon.PNGFile:   {Data: []byte("\x89PNG\r\n\x1a\n")},
		favicon.TouchFile: {Data: []byte("\x89PNG\r\n\x1a\n")},
	}
	icons, err := favicon.Load(public, "#0f766e")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		lang string
	}{
		{name: "english", lang: "en"},
		{name: "german", lang: "de"},
		{name: "french", lang: "fr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := strings.Replace(page, `lang="en"`, `lang="`+tt.lang+`"`, 1)
			h := middleware.DefaultSecurity().Middleware(themeboot.Shell([]byte(src), themeboot.WithFavicon(icons)))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			body := rec.Body.String()

			if err := favicon.CheckPage(rec.Body.Bytes(), public); err != nil {
				t.Errorf("the served page fails the favicon check: %v", err)
			}
			headEnd := strings.Index(body, "<head>") + len("<head>")
			scriptAt := strings.Index(body, "<script")
			styleAt := strings.Index(body, `<link rel="stylesheet"`)
			if !strings.HasPrefix(body[headEnd:], icons.Links()+"<script") || styleAt < scriptAt {
				t.Errorf("the icon links do not lead the boot script ahead of the stylesheet: %s", body)
			}
		})
	}
}

func TestShellWithFaviconRefusesAPageThatCannotCarryTheIcons(t *testing.T) {
	icons, err := favicon.Load(fstest.MapFS{
		favicon.SVGFile:   {Data: []byte("<svg/>")},
		favicon.PNGFile:   {Data: []byte("\x89PNG\r\n\x1a\n")},
		favicon.TouchFile: {Data: []byte("\x89PNG\r\n\x1a\n")},
	}, "#0f766e")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		page      string
		wantPanic bool
	}{
		{name: "plain page", page: page},
		{name: "no head element", page: "<p>fragment</p>", wantPanic: true},
		{name: "icon link already there", page: strings.Replace(page, "<head>", `<head><link rel="icon" href="/favicon.svg">`, 1), wantPanic: true},
		{name: "theme colour already there", page: strings.Replace(page, "<head>", `<head><meta name="theme-color" content="#fff">`, 1), wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if got := recover() != nil; got != tt.wantPanic {
					t.Errorf("panic = %v, want %v", got, tt.wantPanic)
				}
			}()
			themeboot.Shell([]byte(tt.page), themeboot.WithFavicon(icons))
		})
	}
}

func TestInjectLeavesAPageWithoutHeadWithoutIcons(t *testing.T) {
	icons, err := favicon.Load(fstest.MapFS{
		favicon.SVGFile:   {Data: []byte("<svg/>")},
		favicon.PNGFile:   {Data: []byte("\x89PNG\r\n\x1a\n")},
		favicon.TouchFile: {Data: []byte("\x89PNG\r\n\x1a\n")},
	}, "#0f766e")
	if err != nil {
		t.Fatal(err)
	}
	const fragment = "<p>no head</p>"
	if got := string(themeboot.Inject([]byte(fragment), "abc", themeboot.WithFavicon(icons))); got != fragment {
		t.Errorf("got %q, want the page unchanged", got)
	}
}

func TestInjectWithoutFaviconAddsNoIconLinks(t *testing.T) {
	got := string(themeboot.Inject([]byte(page), "abc"))
	// The script itself names theme-color in its selector, so the check
	// looks for the tag.
	if strings.Contains(got, `rel="icon"`) || strings.Contains(got, `<meta name="theme-color"`) {
		t.Errorf("icon markup without WithFavicon: %s", got)
	}
}

func TestScriptUsesTheSharedNames(t *testing.T) {
	src := themeboot.Script()
	for _, want := range []string{themeboot.StorageKey, themeboot.ThemeAttribute, themeboot.ModeAttribute} {
		if !strings.Contains(src, "'"+want+"'") {
			t.Errorf("the script does not contain %q", want)
		}
	}
	if strings.Contains(strings.ToLower(src), "</script") {
		t.Error("the script would end its own tag")
	}
}
