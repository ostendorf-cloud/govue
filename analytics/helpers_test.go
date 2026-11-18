// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testHost = "site.example.test"
	testUA   = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
	// testPeer is a documentation address that is public for net/netip.
	testPeer = "203.0.113.7:4000"
)

// recorded is one post the fake Umami received.
type recorded struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// fakeUmami is the RoundTripper standing in for Umami. No socket is opened.
type fakeUmami struct {
	mu   sync.Mutex
	reqs []recorded

	// status is the answer, 0 means 200.
	status int
	// err, when set, is returned instead of an answer.
	err error
	// block, when set, holds every post until it is closed or the request
	// context ends.
	block chan struct{}
	// entered receives one value per post that reached the fake.
	entered chan struct{}
	calls   atomic.Int64
}

func (f *fakeUmami) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	body, _ := io.ReadAll(req.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{Method: req.Method, URL: req.URL.String(), Header: req.Header.Clone(), Body: body})
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func (f *fakeUmami) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

// syncBuffer is a log sink safe for the workers and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// lines parses the JSON log lines.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// rig is a Client wired to a fake Umami.
type rig struct {
	c     *Client
	fake  *fakeUmami
	logs  *syncBuffer
	clock *fakeClock
	drops atomic.Int64
}

// newRig builds an enabled client. tune changes the config and the options
// before New runs.
func newRig(t *testing.T, tune func(*Config, *Options, *fakeUmami)) *rig {
	t.Helper()
	r := &rig{fake: &fakeUmami{}, logs: &syncBuffer{}, clock: &fakeClock{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}}
	cfg := Config{URL: testURL, WebsiteID: testID, QueueSize: 16}
	opts := Options{
		Hostname:   testHost,
		Logger:     slog.New(slog.NewJSONHandler(r.logs, nil)),
		HTTPClient: &http.Client{Transport: r.fake},
		Now:        r.clock.Now,
		OnDrop:     func() { r.drops.Add(1) },
	}
	if tune != nil {
		tune(&cfg, &opts, r.fake)
	}
	c, err := New(cfg, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.c = c
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Close(ctx)
	})
	return r
}

// drain closes the client, which posts what is queued, and returns the posts.
func (r *rig) drain(t *testing.T) []recorded {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return r.fake.requests()
}

// view decodes the payload of one post.
func view(t *testing.T, rec recorded) map[string]any {
	t.Helper()
	var ev struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body, &ev); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	if ev.Type != "event" {
		t.Fatalf("type = %q, want event", ev.Type)
	}
	return ev.Payload
}

// waitFor polls cond until it holds or two seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

var testAddr = netip.MustParseAddr("203.0.113.7")

func htmlPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<!doctype html><title>x</title>")
}

// siteMux is a small site: pages, an id route, errors, assets, an API route
// and the health and metrics routes answering HTML on purpose, so only the
// exclusion keeps them out.
func siteMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", htmlPage)
	mux.HandleFunc("GET /about", htmlPage)
	mux.HandleFunc("GET /notes/{id}", htmlPage)
	mux.HandleFunc("POST /notes", htmlPage)
	mux.HandleFunc("GET /healthz", htmlPage)
	mux.HandleFunc("GET /readyz", htmlPage)
	mux.HandleFunc("GET /livez", htmlPage)
	mux.HandleFunc("GET /metrics", htmlPage)
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "<html>not found</html>")
	})
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/about", http.StatusFound)
	})
	mux.HandleFunc("GET /cached", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotModified)
	})
	mux.HandleFunc("GET /api/notes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"notes":[]}`)
	})
	mux.HandleFunc("GET /assets/app.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = io.WriteString(w, "body{}")
	})
	mux.HandleFunc("GET /sniffed", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body>hi</body></html>")
	})
	mux.HandleFunc("GET /empty", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /early", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<html></html>")
	})
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})
	return mux
}

// request builds a browser like request from a public address. mod changes it.
func request(method, target string, mod func(*http.Request)) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	r.RemoteAddr = testPeer
	r.Header.Set("User-Agent", testUA)
	r.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	if mod != nil {
		mod(r)
	}
	return r
}

// serve runs r through h and returns the recorder.
func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
