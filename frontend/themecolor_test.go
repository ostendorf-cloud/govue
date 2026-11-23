// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
	"github.com/ostendorf-cloud/govue/themeboot"
)

const darkColor = "#0b1f1c"

func TestPagesCarryOneThemeColorPerMode(t *testing.T) {
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.ThemeColorDark = darkColor
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/de/", "/en/imprint", "/de/missing"} {
		res := do(t, s.Handler(), http.MethodGet, target, nil)
		page := body(t, res)
		light := `<meta name="theme-color" content="` + themeColor + `" media="` + favicon.MediaLight + `">`
		dark := `<meta name="theme-color" content="` + darkColor + `" media="` + favicon.MediaDark + `">`
		if !strings.Contains(page, light) || !strings.Contains(page, dark) {
			t.Errorf("%s: no theme-color tag per mode: %s", target, page)
		}
		if strings.Count(page, `<meta name="theme-color"`) != 2 {
			t.Errorf("%s: want exactly two theme-color tags: %s", target, page)
		}
		// The tags come ahead of the script, which reads them while the
		// head is parsed.
		if strings.Index(page, dark) > strings.Index(page, "<script>") {
			t.Errorf("%s: the theme-color tags follow the boot script", target)
		}

		// The served script is the one the policy allows: the colours are
		// in the tags, not in the script, so the hash does not change.
		const open = "<script>"
		start := strings.Index(page, open)
		end := strings.Index(page, "</script>")
		sum := sha256.Sum256([]byte(page[start+len(open) : end]))
		source := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if source != themeboot.Hash() || !strings.Contains(res.Header.Get("Content-Security-Policy"), source) {
			t.Errorf("%s: the served script hashes to %s, the policy is %s", target, source,
				res.Header.Get("Content-Security-Policy"))
		}
	}
}

func TestOneThemeColorWithoutADarkColor(t *testing.T) {
	page := body(t, do(t, newServer(t).Handler(), http.MethodGet, "/de/", nil))
	if !strings.Contains(page, `<meta name="theme-color" content="`+themeColor+`">`) ||
		strings.Count(page, `<meta name="theme-color"`) != 1 || strings.Contains(page, `media="`+favicon.MediaDark) {
		t.Errorf("want one theme-color tag without a media query: %s", page)
	}
}

func TestNewRefusesABadDarkThemeColorWithoutQuotingIt(t *testing.T) {
	for _, bad := range []string{"hunter2", "#hunter2", "#0f766e\"><script>hunter2", "red"} {
		cfg := testConfig(buildOf(routes), io.Discard)
		cfg.ThemeColorDark = bad
		_, err := frontend.New(cfg)
		configtexttest.AssertHidden(t, err, bad, "Config.ThemeColorDark")
	}
}
