// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// writeFrontendBuild writes what a site's frontend image holds: the Vite
// build with the prerender written into it, the favicon files Vite copies
// from web/public, a 404 page per language and the files a server must not
// serve, the template and the Vite manifest.
func writeFrontendBuild(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dist")
	write := func(name string, content []byte) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range siteLanguages {
		for _, route := range fixtureRoutes {
			vp := "/" + code + route
			if route == "/" {
				vp = "/" + code + "/"
			}
			write(pageFile(vp), []byte(pageHTML(vp)))
		}
		write(code+"/404.html", []byte(pageHTML("/"+code+"/404")))
	}
	write(sitemapFile, []byte(fixtureSitemap(fixtureRoutes)))
	write("assets/index-BXa3k2_9.js", []byte(scriptText))
	write("index.html", []byte("<!--app-html-->"))
	write(".vite/manifest.json", []byte(`{"manifest":true}`))
	for _, name := range []string{favicon.SVGFile, favicon.PNGFile, favicon.TouchFile} {
		data, err := fs.ReadFile(publicFiles, "web/public/"+name)
		if err != nil {
			t.Fatal(err)
		}
		write(name, data)
	}
	return dir
}

func frontendEnv(siteDir string) func(string) string {
	env := map[string]string{"SITE_DIR": siteDir}
	return func(key string) string { return env[key] }
}

// TestFrontendServesTheBuildThroughTheRealChain runs the frontend image of
// the example through its real handler chain, with no socket.
func TestFrontendServesTheBuildThroughTheRealChain(t *testing.T) {
	cfg, err := loadFrontendConfig(frontendEnv(writeFrontendBuild(t)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := newFrontend(cfg, testLogger())
	if err != nil {
		t.Fatalf("newFrontend: %v", err)
	}
	h := s.Handler()

	for _, code := range siteLanguages {
		for _, route := range fixtureRoutes {
			target := "/" + code + escapedSegments(route)
			if route == "/" {
				target = "/" + code + "/"
			}
			res := do(t, h, http.MethodGet, target, nil)
			got := bodyOf(t, res)
			if res.StatusCode != http.StatusOK || !strings.Contains(got, "page:") {
				t.Errorf("%s: status %d, body %q", target, res.StatusCode, got)
			}
			if res.Header.Get("Cache-Control") != "no-cache" || res.Header.Get("ETag") == "" {
				t.Errorf("%s: Cache-Control %q, ETag %q", target, res.Header.Get("Cache-Control"), res.Header.Get("ETag"))
			}
			csp := res.Header.Get("Content-Security-Policy")
			if !strings.Contains(csp, "script-src 'self' "+themeboot.Hash()) {
				t.Errorf("%s: CSP lacks the boot script hash: %s", target, csp)
			}
			if !strings.Contains(got, "<script>"+themeboot.Script()+"</script>") {
				t.Errorf("%s: no boot script", target)
			}
		}
	}

	checks := []struct {
		target   string
		status   int
		location string
	}{
		{"/", http.StatusFound, "/de/"},
		{"/impressum", http.StatusFound, "/de/imprint"},
		{"/de/imprint/", http.StatusMovedPermanently, "/de/imprint"},
		{"/assets/index-BXa3k2_9.js", http.StatusOK, ""},
		{"/favicon.svg", http.StatusOK, ""},
		{"/robots.txt", http.StatusOK, ""},
		{"/sitemap.xml", http.StatusOK, ""},
		{"/index.html", http.StatusNotFound, ""},
		{"/.vite/manifest.json", http.StatusNotFound, ""},
		{"/de/../../site_test.go", http.StatusNotFound, ""},
		{"/notes", http.StatusNotFound, ""},
		{"/healthz", http.StatusNotFound, ""},
	}
	for _, c := range checks {
		res := do(t, h, http.MethodGet, c.target, nil)
		got := bodyOf(t, res)
		if res.StatusCode != c.status || res.Header.Get("Location") != c.location {
			t.Errorf("%s: %d to %q, want %d to %q", c.target, res.StatusCode, res.Header.Get("Location"), c.status, c.location)
		}
		if strings.Contains(got, "manifest") || strings.Contains(got, "app-html") {
			t.Errorf("%s leaks a build file: %s", c.target, got)
		}
		if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Set-Cookie") != "" {
			t.Errorf("%s: headers %v", c.target, res.Header)
		}
	}
	if res := do(t, h, http.MethodPost, "/notes", nil); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /notes: status %d, want 405, the frontend has no API", res.StatusCode)
	}

	admin := s.AdminHandler()
	for _, target := range []string{"/healthz", "/readyz", "/metrics"} {
		if res := do(t, admin, http.MethodGet, target, nil); res.StatusCode != http.StatusOK {
			t.Errorf("admin %s: status %d", target, res.StatusCode)
		}
	}
}

func TestFrontendRefusesABuildWithoutA404Page(t *testing.T) {
	dir := writeFrontendBuild(t)
	if err := os.Remove(filepath.Join(dir, "en", "404.html")); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFrontendConfig(frontendEnv(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newFrontend(cfg, testLogger()); err == nil || !strings.Contains(err.Error(), "en/404.html") {
		t.Errorf("newFrontend error = %v, want the missing 404 page", err)
	}
}

func TestFrontendRefusesAPageThatLinksOutOfTheBuild(t *testing.T) {
	dir := writeFrontendBuild(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.html")
	if err := os.WriteFile(outside, []byte(pageHTML("/de/x")), 0o600); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, "de", "imprint", "index.html")
	if err := os.Remove(page); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, page); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFrontendConfig(frontendEnv(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newFrontend(cfg, testLogger()); err == nil || !strings.Contains(err.Error(), "de/imprint/index.html") {
		t.Errorf("newFrontend error = %v, want the page that links out", err)
	}
}

func TestFrontendConfigNeedsTheSiteDirectory(t *testing.T) {
	_, err := loadFrontendConfig(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "SITE_DIR") {
		t.Errorf("error = %v, want SITE_DIR", err)
	}
}

func TestFrontendNeverQuotesTheSiteDirectory(t *testing.T) {
	const secret = "/no/such/dir/hunter2"
	cfg, err := loadFrontendConfig(frontendEnv(secret))
	if err != nil {
		t.Fatal(err)
	}
	_, err = newFrontend(cfg, testLogger())
	if !errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error = %v, want ErrNotExist without the path", err)
	}
}
