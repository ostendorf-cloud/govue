// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/logging"
)

const (
	umamiTestID  = "0a1b2c3d-0000-4000-8000-000000000001"
	umamiTestURL = "https://umami.example.test"
	visitorUA    = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
)

// fakeUmami is the transport standing in for Umami, so no socket is opened.
type fakeUmami struct {
	mu    sync.Mutex
	posts []string
}

func (f *fakeUmami) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	f.mu.Lock()
	f.posts = append(f.posts, string(body))
	f.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func (f *fakeUmami) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

// newCountingApp builds the app with Umami set, posting to fake, and a site.
func newCountingApp(t *testing.T, fake *fakeUmami) *app {
	t.Helper()
	s := writeTestSite(t)
	env := testEnv(t, 1000)
	env["SITE_DIR"] = s.dir
	env["SITE_HOST"] = "site.example.test"
	env["UMAMI_URL"] = umamiTestURL
	env["UMAMI_WEBSITE_ID"] = umamiTestID
	cfg, err := loadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.umamiHTTP = &http.Client{Transport: fake}
	a, err := newApp(cfg, logging.New(io.Discard, cfg.logLevel))
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return a
}

func visit(t *testing.T, h http.Handler, target string, header http.Header) {
	t.Helper()
	hdr := http.Header{"User-Agent": {visitorUA}}
	for k, v := range header {
		hdr[k] = v
	}
	_ = do(t, h, http.MethodGet, target, hdr).Body.Close()
}

func TestAnalyticsIsOffByDefault(t *testing.T) {
	s := writeTestSite(t)
	a, _ := newSiteAppLogs(t, s.dir, 1000)
	if a.analytics == nil || a.analytics.Enabled() || a.cfg.umami.Enabled() {
		t.Fatal("counting is on without UMAMI_URL and UMAMI_WEBSITE_ID")
	}
	visit(t, a.handler(), "/de/impressum", nil)
	if got := a.analytics.Stats(); got.Sent+got.Failed+got.Dropped != 0 {
		t.Errorf("stats = %+v, nothing may be queued", got)
	}
}

func TestAnalyticsHalfSetPairFailsStartup(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"url only": {"UMAMI_URL": umamiTestURL},
		"id only":  {"UMAMI_WEBSITE_ID": umamiTestID},
	} {
		t.Run(name, func(t *testing.T) {
			base := testEnv(t, 10)
			for k, v := range env {
				base[k] = v
			}
			_, err := loadConfig(func(k string) string { return base[k] })
			if err == nil || !strings.Contains(err.Error(), "UMAMI_URL and UMAMI_WEBSITE_ID") {
				t.Fatalf("loadConfig error = %v", err)
			}
		})
	}
}

func TestAnalyticsCountsOnlyPagesThroughTheRealChain(t *testing.T) {
	fake := &fakeUmami{}
	a := newCountingApp(t, fake)
	h := a.handler()

	visit(t, h, "/de/impressum?utm=x", nil)                                    // counted
	visit(t, h, "/de/impressum", http.Header{"Dnt": {"1"}})                    // Do Not Track
	visit(t, h, "/de/impressum", http.Header{"Sec-Gpc": {"1"}})                // Global Privacy Control
	visit(t, h, "/assets/app-abc123.js", nil)                                  // asset
	visit(t, h, "/sitemap.xml", nil)                                           // not HTML
	visit(t, h, "/app", nil)                                                   // JSON route
	visit(t, h, "/nowhere", nil)                                               // 404
	visit(t, h, "/de/impressum", http.Header{"User-Agent": {"Googlebot/2.1"}}) // bot

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.analytics.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The two HTML pages count: the site page and the app shell at /app.
	posts := fake.bodies()
	if len(posts) != 2 {
		t.Fatalf("got %d views, want 2: %v", len(posts), posts)
	}
	urls := map[any]bool{}
	for _, post := range posts {
		var ev struct {
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal([]byte(post), &ev); err != nil {
			t.Fatal(err)
		}
		urls[ev.Payload["url"]] = true
		if ev.Payload["hostname"] != "site.example.test" {
			t.Errorf("hostname = %v", ev.Payload["hostname"])
		}
		if strings.Contains(post, "utm") {
			t.Error("the query string reached Umami")
		}
	}
	if !urls["/de/impressum"] || !urls["/app"] {
		t.Errorf("urls = %v", urls)
	}
}
