// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/logging"
)

// fakeUmami records the posts of the analytics client. No socket is opened.
type fakeUmami struct {
	mu    sync.Mutex
	posts [][]byte
	// block, when set, holds every post until the request context ends.
	block bool
}

func (f *fakeUmami) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	f.mu.Lock()
	f.posts = append(f.posts, body)
	f.mu.Unlock()
	if f.block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader("{}")), Request: req,
	}, nil
}

// urls returns the url field of every post.
func (f *fakeUmami) urls(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.posts {
		var ev struct {
			Payload struct {
				URL string `json:"url"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(p, &ev); err != nil {
			t.Fatalf("post %s: %v", p, err)
		}
		out = append(out, ev.Payload.URL)
	}
	return out
}

func newViews(t *testing.T, fake *fakeUmami, queue int) *analytics.Client {
	t.Helper()
	c, err := analytics.New(analytics.Config{
		URL:       "https://umami.example.test",
		WebsiteID: "0a1b2c3d-0000-4000-8000-000000000001",
		QueueSize: queue,
	}, analytics.Options{
		Hostname:   "example.test",
		Logger:     logging.New(io.Discard, slog.LevelInfo),
		HTTPClient: &http.Client{Transport: fake},
	})
	if err != nil {
		t.Fatalf("analytics.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Close(ctx)
	})
	return c
}

// browser is a request header of a visitor the analytics rules count.
func browser() http.Header {
	return http.Header{
		"User-Agent":      {"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"},
		"Accept-Language": {"de-DE,de;q=0.9"},
	}
}

// visit sends one request from a public address through h.
func visit(t *testing.T, h http.Handler, target string, header http.Header) *http.Response {
	t.Helper()
	if header == nil {
		header = browser()
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.RemoteAddr = "203.0.113.7:4000"
	for name, values := range header {
		req.Header[name] = values
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func closeViews(t *testing.T, c *analytics.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPagesAreCountedByTheirPatternAndNothingElse(t *testing.T) {
	fake := &fakeUmami{}
	views := newViews(t, fake, 64)
	cfg := testConfig(buildOf(termsRoutes), io.Discard)
	cfg.Analytics = views
	cfg.Redirects = []frontend.Redirect{{From: "/de/agb", To: "/de/terms"}}
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	// Counted: a page with 200, by pattern, the query and the encoded path
	// never sent.
	for _, target := range []string{"/de/?utm_source=x&token=abc", "/en/imprint", "/en/%C3%BCber"} {
		if res := visit(t, h, target, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", target, res.StatusCode)
		}
	}
	// Counted: a page revalidated with 304.
	etag := visit(t, h, "/de/privacy", nil).Header.Get("ETag")
	revalidate := browser()
	revalidate.Set("If-None-Match", etag)
	if res := visit(t, h, "/de/privacy", revalidate); res.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation: status %d, want 304", res.StatusCode)
	}

	// Never counted: redirects, aliases, 404, files, assets.
	for _, target := range []string{
		"/", "/de", "/de/imprint/", "/de/agb", "/impressum", "/agb", "/de/missing", "/missing",
		"/de/../en/", "/" + hashedJS, "/" + plainSVG, "/robots.txt", "/sitemap.xml", "/favicon.svg", "/" + ogImage,
	} {
		_ = body(t, visit(t, h, target, nil))
	}
	// Never counted: the visitor rules of analytics.
	for _, mod := range []func(http.Header){
		func(h http.Header) { h.Set("DNT", "1") },
		func(h http.Header) { h.Set("Sec-GPC", "1") },
		func(h http.Header) { h.Set("User-Agent", "Googlebot/2.1") },
		func(h http.Header) { h.Set("Sec-Purpose", "prefetch") },
	} {
		header := browser()
		mod(header)
		_ = body(t, visit(t, h, "/de/", header))
	}

	closeViews(t, views)
	// Two workers post, so the order is not fixed.
	got := fake.urls(t)
	slices.Sort(got)
	want := []string{"/de/", "/de/privacy", "/de/privacy", "/en/imprint", "/en/über"}
	if !slices.Equal(got, want) {
		t.Errorf("counted %q, want %q", got, want)
	}
	for _, p := range fake.posts {
		for _, leak := range []string{"utm", "token", "%C3", "203.0.113.7"} {
			if strings.Contains(string(p), leak) {
				t.Errorf("a view carries %q: %s", leak, p)
			}
		}
	}
}

func TestDroppedViewsAreOnTheFrontendMetrics(t *testing.T) {
	fake := &fakeUmami{block: true}
	views := newViews(t, fake, 1)
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Analytics = views
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Two workers block on the fake, one view waits in the queue of one,
	// so a handful of visits drops some.
	for range 8 {
		_ = body(t, visit(t, s.Handler(), "/de/", nil))
	}
	dropped := views.Stats().Dropped
	if dropped == 0 {
		t.Fatal("no view was dropped, the test does not test the counter")
	}
	scrape := body(t, do(t, s.AdminHandler(), http.MethodGet, "/metrics", nil))
	line := "analytics_views_dropped_total " + strconv.FormatUint(dropped, 10)
	if !strings.Contains(scrape, line) {
		t.Errorf("the metrics lack %q:\n%s", line, scrape)
	}
}

func TestRunClosesTheClientSoQueuedViewsAreSent(t *testing.T) {
	fake := &fakeUmami{}
	views := newViews(t, fake, 64)
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Analytics = views
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, visit(t, s.Handler(), "/de/", nil))
	if err := s.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := fake.urls(t); len(got) != 1 {
		t.Errorf("posts after close = %v, want the queued view", got)
	}
	// A view after close is dropped, not sent: the client is closed.
	_ = body(t, visit(t, s.Handler(), "/de/", nil))
	if views.Stats().Dropped != 1 {
		t.Errorf("dropped = %d, want 1 after close", views.Stats().Dropped)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Errorf("a second Close: %v", err)
	}
	if err := newServer(t).Close(t.Context()); err != nil {
		t.Errorf("Close without analytics: %v", err)
	}
}

// TestRunClosesTheClientWhenItReturns runs Run itself without a socket: an
// address without a port fails in net.ListenConfig.Listen while it is
// parsed, in resolveAddrList, before any socket is created. Run must close
// the client on that way out too, so the queued view is sent and a later
// one is dropped.
func TestRunClosesTheClientWhenItReturns(t *testing.T) {
	fake := &fakeUmami{}
	views := newViews(t, fake, 64)
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Analytics = views
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, visit(t, s.Handler(), "/de/", nil))

	err = s.Run(t.Context(), "no-port-here", "no-port-either")
	if err == nil || !strings.Contains(err.Error(), "listen on Config.Addr") {
		t.Fatalf("Run = %v, want the refused address", err)
	}
	if got := fake.urls(t); len(got) != 1 {
		t.Errorf("posts after Run = %v, want the queued view sent", got)
	}
	_ = body(t, visit(t, s.Handler(), "/de/", nil))
	if views.Stats().Dropped != 1 {
		t.Errorf("dropped = %d, want 1: Run did not close the client", views.Stats().Dropped)
	}
}

func TestWithoutAnalyticsNoDropCounter(t *testing.T) {
	scrape := body(t, do(t, newServer(t).AdminHandler(), http.MethodGet, "/metrics", nil))
	if strings.Contains(scrape, "analytics_views_dropped_total") {
		t.Error("a frontend without analytics reports a drop counter")
	}
}
