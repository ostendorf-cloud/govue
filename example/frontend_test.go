// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
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
			if !strings.Contains(csp, "; worker-src 'self'; ") {
				t.Errorf("%s: CSP lacks worker-src 'self' for the contact form's solver: %s", target, csp)
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

// TestFrontendCountsPagesThroughTheRealChain runs the frontend image with
// Umami set: a page is counted by its pattern, a redirect, an alias, a 404
// and a file are not.
func TestFrontendCountsPagesThroughTheRealChain(t *testing.T) {
	env := map[string]string{
		"SITE_DIR":         writeFrontendBuild(t),
		"SITE_HOST":        "site.example.test",
		"UMAMI_URL":        umamiTestURL,
		"UMAMI_WEBSITE_ID": umamiTestID,
	}
	cfg, err := loadFrontendConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeUmami{}
	cfg.umamiHTTP = &http.Client{Transport: fake}
	s, err := newFrontend(cfg, testLogger())
	if err != nil {
		t.Fatalf("newFrontend: %v", err)
	}
	for _, target := range []string{
		"/de/imprint?token=abc", "/", "/de", "/impressum", "/de/missing", "/assets/index-BXa3k2_9.js", "/robots.txt",
	} {
		visit(t, s.Handler(), target, nil)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	posts := fake.bodies()
	if len(posts) != 1 || !strings.Contains(posts[0], `"url":"/de/imprint"`) || strings.Contains(posts[0], "token") {
		t.Errorf("posts = %v, want the one page by its pattern", posts)
	}
}

// handlerTransport answers a proxied request with a handler in memory, the
// example's own API here, so no socket is opened.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	// The API sees the request as a server would: a RequestURI and a body.
	in := req.Clone(req.Context())
	in.RequestURI = req.URL.RequestURI()
	if in.Body == nil {
		in.Body = http.NoBody
	}
	t.h.ServeHTTP(rec, in)
	res := rec.Result()
	res.Request = req
	return res, nil
}

// TestFrontendProxiesTheAPIInDevelopment runs the frontend image and the
// example's API on one origin, the compose shape: a page from the frontend,
// /api/ from the API handler, both through the frontend's chain.
func TestFrontendProxiesTheAPIInDevelopment(t *testing.T) {
	api, _ := newTestApp(t, 1000)
	env := map[string]string{
		"SITE_DIR":      writeFrontendBuild(t),
		"DEVELOPMENT":   "true",
		"DEV_API_PROXY": "http://api:8080",
	}
	cfg, err := loadFrontendConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	cfg.devAPITransport = handlerTransport{h: api.handler()}
	s, err := newFrontend(cfg, testLogger())
	if err != nil {
		t.Fatalf("newFrontend: %v", err)
	}

	res := do(t, s.Handler(), http.MethodGet, "/api/v1/contact/challenge", nil)
	got := bodyOf(t, res)
	// The example runs without a contact key, so its API answers 503
	// not_configured: the answer of the API, not of the frontend.
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(got, "not_configured") {
		t.Errorf("status %d, body %q, want the API's own answer", res.StatusCode, got)
	}
	if res.Header.Values("X-Frame-Options")[0] != "DENY" || len(res.Header.Values("Content-Security-Policy")) != 1 {
		t.Errorf("the proxied answer lacks the frontend's headers: %v", res.Header)
	}
	if page := do(t, s.Handler(), http.MethodGet, "/de/", nil); page.StatusCode != http.StatusOK {
		t.Errorf("page: status %d", page.StatusCode)
	}
}

func TestFrontendWithoutDevelopmentKeepsAPIA404(t *testing.T) {
	env := map[string]string{"SITE_DIR": writeFrontendBuild(t), "DEV_API_PROXY": "http://api:8080"}
	cfg, err := loadFrontendConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newFrontend(cfg, testLogger()); err == nil || !strings.Contains(err.Error(), "Config.Development") {
		t.Errorf("newFrontend error = %v, want the refused proxy", err)
	}
	delete(env, "DEV_API_PROXY")
	cfg, err = loadFrontendConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	s, err := newFrontend(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if res := do(t, s.Handler(), http.MethodGet, "/api/v1/contact/challenge", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", res.StatusCode)
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
