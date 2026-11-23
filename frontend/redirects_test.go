// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

// termsRoutes is the fixture site with a terms page, the target of the old
// agb paths.
var termsRoutes = append([]string{"/terms"}, routes...)

func redirectServer(t *testing.T, redirects []frontend.Redirect) *frontend.Server {
	t.Helper()
	cfg := testConfig(buildOf(termsRoutes), io.Discard)
	cfg.Redirects = redirects
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestRedirectsAnswer301ToThePage(t *testing.T) {
	s := redirectServer(t, []frontend.Redirect{
		{From: "/de/agb", To: "/de/terms"},
		{From: "/de/agb/", To: "/de/terms"},
		{From: "/en/agb", To: "/en/terms"},
		{From: "/de/alt/über", To: "/de/über"},
	})
	tests := map[string]string{
		"/de/agb":                 "/de/terms",
		"/de/agb/":                "/de/terms",
		"/en/agb":                 "/en/terms",
		"/de/agb?utm=a&b=1":       "/de/terms?utm=a&b=1",
		"/de/agb/?q=ü%20x":        "/de/terms?q=%C3%BC%20x",
		"/de/alt/%C3%BCber":       "/de/%C3%BCber",
		"/de/agb?x=%0d%0aSet-X:1": "/de/terms?x=%0d%0aSet-X:1",
	}
	for req, want := range tests {
		res := do(t, s.Handler(), http.MethodGet, req, nil)
		_ = body(t, res)
		if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q, want 301 to %q", req, res.StatusCode, res.Header.Get("Location"), want)
		}
		if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: the redirect lacks the security headers", req)
		}
		if res.Header.Get("Set-X") != "" {
			t.Errorf("%s: a header was injected", req)
		}
	}
	// The target answers, and a path that is not listed stays a 404.
	if res := do(t, s.Handler(), http.MethodGet, "/de/terms", nil); res.StatusCode != http.StatusOK {
		t.Errorf("target: status %d", res.StatusCode)
	}
	if res := do(t, s.Handler(), http.MethodGet, "/fr/agb", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("/fr/agb: status %d, want 404", res.StatusCode)
	}
	if res := do(t, s.Handler(), http.MethodPost, "/de/agb", nil); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /de/agb: status %d, want 405", res.StatusCode)
	}
}

func TestWithoutRedirectsOldPathsAre404(t *testing.T) {
	s := redirectServer(t, nil)
	for _, target := range []string{"/de/agb", "/de/agb/"} {
		if res := do(t, s.Handler(), http.MethodGet, target, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, res.StatusCode)
		}
	}
}

// TestNewRefusesABadRedirectWithoutQuotingIt covers every refusal. The value
// holds a marker that must appear in no printed form of the error and in no
// error under it.
func TestNewRefusesABadRedirectWithoutQuotingIt(t *testing.T) {
	const marker = "hunter2"
	tests := []struct {
		name string
		r    frontend.Redirect
		want string
	}{
		{
			"target not a page",
			frontend.Redirect{From: "/de/" + marker, To: "/de/" + marker + "-missing"},
			"entry 2: the target is not a page",
		},
		{
			"target an alias",
			frontend.Redirect{From: "/de/" + marker, To: "/impressum"},
			"entry 2: the target is not a page",
		},
		{
			"target an asset",
			frontend.Redirect{From: "/de/" + marker, To: "/" + hashedJS},
			"entry 2: the target is not a page",
		},
		{
			"source is a page",
			frontend.Redirect{From: "/de/imprint", To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is a slash form",
			frontend.Redirect{From: "/de/imprint/", To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is a language root",
			frontend.Redirect{From: "/de", To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is an alias",
			frontend.Redirect{From: "/impressum", To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is the terms alias",
			frontend.Redirect{From: "/agb/", To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is the sitemap",
			frontend.Redirect{From: "/sitemap.xml", To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is a public file",
			frontend.Redirect{From: "/" + ogImage, To: "/de/terms"},
			"entry 2: the source is already a route",
		},
		{
			"source is an asset",
			frontend.Redirect{From: "/" + hashedJS, To: "/de/terms"},
			"entry 2: the source is under /assets/",
		},
		{
			"source is the asset directory",
			frontend.Redirect{From: "/assets", To: "/de/terms"},
			"entry 2: the source is under /assets/",
		},
		{"source is the root", frontend.Redirect{From: "/", To: "/de/terms"}, "entry 2: the source must be"},
		{"source empty", frontend.Redirect{From: "", To: "/de/terms"}, "entry 2: the source must be"},
		{"source relative", frontend.Redirect{From: marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source double slash", frontend.Redirect{From: "//" + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source dot segment", frontend.Redirect{From: "/de/../" + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source dotfile", frontend.Redirect{From: "/." + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source backslash", frontend.Redirect{From: "/de\\" + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source percent", frontend.Redirect{From: "/de/%2e" + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source brace", frontend.Redirect{From: "/de/{" + marker + "}", To: "/de/terms"}, "entry 2: the source must be"},
		{"source space", frontend.Redirect{From: "/de/" + marker + " x", To: "/de/terms"}, "entry 2: the source must be"},
		{"source control", frontend.Redirect{From: "/de/" + marker + "\n", To: "/de/terms"}, "entry 2: the source must be"},
		{
			"source invalid UTF-8",
			frontend.Redirect{From: "/de/" + marker + "\xff", To: "/de/terms"},
			"entry 2: the source must be",
		},
		{"source query", frontend.Redirect{From: "/de/agb?" + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{
			"source bare question mark",
			frontend.Redirect{From: "/de/" + marker + "?", To: "/de/terms"},
			"entry 2: the source must be",
		},
		{"source fragment", frontend.Redirect{From: "/de/agb#" + marker, To: "/de/terms"}, "entry 2: the source must be"},
		{"source repeated", frontend.Redirect{From: "/de/agb-" + marker, To: "/de/terms"}, "entry 2 repeats the source of entry 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := frontend.Redirect{From: "/de/agb", To: "/de/terms"}
			if tt.name == "source repeated" {
				first.From = tt.r.From
			}
			cfg := testConfig(buildOf(termsRoutes), io.Discard)
			cfg.Redirects = []frontend.Redirect{first, tt.r}
			_, err := frontend.New(cfg)
			configtexttest.AssertHidden(t, err, marker, "Config.Redirects "+tt.want)
		})
	}
}
