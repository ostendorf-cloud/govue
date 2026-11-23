// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package themeboot_test

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// inlineScript returns the text of the first script element of page, the
// text a browser hashes for a 'sha256-' source.
func inlineScript(t *testing.T, page string) string {
	t.Helper()
	const open = "<script>"
	start := strings.Index(page, open)
	end := strings.Index(page, "</script>")
	if start < 0 || end < start {
		t.Fatalf("no script element without attributes in %s", page)
	}
	return page[start+len(open) : end]
}

// cspHash is the source a browser computes for an inline script text.
func cspHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func TestInjectStaticWritesTheScriptWithoutANonce(t *testing.T) {
	got := string(themeboot.InjectStatic([]byte(page)))
	if !strings.HasPrefix(got, `<!doctype html><html lang="en"><head><script>`) {
		t.Errorf("the script does not follow the opening head tag: %s", got)
	}
	if strings.Contains(got, "nonce") {
		t.Errorf("the static script carries a nonce: %s", got)
	}
	if text := inlineScript(t, got); text != themeboot.Script() {
		t.Errorf("the injected text is not the script:\n%s", text)
	}
}

func TestHashAllowsExactlyTheInjectedScript(t *testing.T) {
	for _, opts := range [][]themeboot.Option{nil, {themeboot.WithStorageKey("site:theme")}} {
		got := string(themeboot.InjectStatic([]byte(page), opts...))
		want := cspHash(inlineScript(t, got))
		if h := themeboot.Hash(opts...); h != want {
			t.Errorf("Hash = %s, the injected script hashes to %s", h, want)
		}
	}
	if themeboot.Hash() == themeboot.Hash(themeboot.WithStorageKey("site:theme")) {
		t.Error("another storage key gives the same hash, so the hash does not cover the key")
	}
}

func TestHashIsAQuotedSHA256Source(t *testing.T) {
	h := themeboot.Hash()
	if !strings.HasPrefix(h, "'sha256-") || !strings.HasSuffix(h, "'") {
		t.Fatalf("Hash = %s, want a quoted sha256 source", h)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(h, "'sha256-"), "'"))
	if err != nil || len(raw) != sha256.Size {
		t.Errorf("Hash = %s is not the standard base64 of a SHA-256 digest (%v)", h, err)
	}
}

func TestInjectStaticLeavesAPageWithoutHeadUnchanged(t *testing.T) {
	const noHead = `<header>x</header><!-- <head> -->`
	if got := string(themeboot.InjectStatic([]byte(noHead))); got != noHead {
		t.Errorf("a page without a head changed: %s", got)
	}
}

func TestInjectStaticWritesTheIconsBehindTheScript(t *testing.T) {
	public := fstest.MapFS{
		favicon.SVGFile:   {Data: []byte("<svg/>")},
		favicon.PNGFile:   {Data: []byte("\x89PNG\r\n\x1a\n")},
		favicon.TouchFile: {Data: []byte("\x89PNG\r\n\x1a\n")},
	}
	icons, err := favicon.Load(public, "#0f766e")
	if err != nil {
		t.Fatal(err)
	}
	got := string(themeboot.InjectStatic([]byte(page), themeboot.WithFavicon(icons)))
	if err := favicon.CheckPage([]byte(got), public); err != nil {
		t.Errorf("the page fails the favicon check: %v", err)
	}
	if !strings.Contains(got, "</script>"+icons.Links()) {
		t.Errorf("the icon links do not follow the script: %s", got)
	}
}
