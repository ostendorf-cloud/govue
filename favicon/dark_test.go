// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package favicon_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

func darkIcons(t *testing.T) (favicon.Icons, fstest.MapFS) {
	t.Helper()
	public := fstest.MapFS{
		favicon.SVGFile:   {Data: []byte("<svg/>")},
		favicon.PNGFile:   {Data: []byte("\x89PNG\r\n\x1a\n")},
		favicon.TouchFile: {Data: []byte("\x89PNG\r\n\x1a\n")},
	}
	icons, err := favicon.Load(public, "#ffffff")
	if err != nil {
		t.Fatal(err)
	}
	return icons, public
}

func TestWithDarkThemeColorWritesOneTagPerMode(t *testing.T) {
	icons, public := darkIcons(t)
	dark, err := icons.WithDarkThemeColor("#000")
	if err != nil {
		t.Fatal(err)
	}
	links := dark.Links()
	for _, want := range []string{
		`<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">`,
		`<meta name="theme-color" content="#000" media="(prefers-color-scheme: dark)">`,
	} {
		if !strings.Contains(links, want) {
			t.Errorf("Links lacks %s:\n%s", want, links)
		}
	}
	if strings.Count(links, "theme-color") != 2 {
		t.Errorf("want two theme-color tags:\n%s", links)
	}
	page := []byte("<html><head>" + links + "</head></html>")
	if err := favicon.CheckPage(page, public); err != nil {
		t.Errorf("CheckPage refuses the tags per mode: %v", err)
	}
	if !favicon.Present(page) {
		t.Error("Present misses the tags per mode")
	}
	if strings.Contains(icons.Links(), "media=") {
		t.Error("WithDarkThemeColor changed the icons it was called on")
	}
}

func TestWithDarkThemeColorRefusesAnotherFormWithoutQuotingIt(t *testing.T) {
	icons, _ := darkIcons(t)
	for _, bad := range []string{"hunter2", "#12345", `#000" onload="x`, "#gggggg"} {
		_, err := icons.WithDarkThemeColor(bad)
		if !errors.Is(err, favicon.ErrThemeColor) {
			t.Errorf("%q: error = %v, want ErrThemeColor", bad, err)
		}
		configtexttest.AssertHidden(t, err, bad, "dark theme colour")
	}
}
