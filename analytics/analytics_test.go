// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewValidatesOptions(t *testing.T) {
	good := Options{Hostname: testHost, Logger: newRig(t, nil).c.logger}
	tests := []struct {
		name string
		cfg  Config
		opts func(Options) Options
		errs []string
	}{
		{"valid", Config{URL: testURL, WebsiteID: testID}, func(o Options) Options { return o }, nil},
		{"no hostname", Config{URL: testURL, WebsiteID: testID}, func(o Options) Options { o.Hostname = ""; return o }, []string{"Hostname"}},
		{
			"hostname with a scheme",
			Config{URL: testURL, WebsiteID: testID},
			func(o Options) Options { o.Hostname = "https://site.example.test"; return o },
			[]string{"Hostname"},
		},
		{
			"hostname with a path",
			Config{URL: testURL, WebsiteID: testID},
			func(o Options) Options { o.Hostname = "site.example.test/x"; return o },
			[]string{"Hostname"},
		},
		{"no logger", Config{URL: testURL, WebsiteID: testID}, func(o Options) Options { o.Logger = nil; return o }, []string{"Logger"}},
		{"half configured", Config{URL: testURL}, func(o Options) Options { return o }, []string{"set together"}},
		{
			"everything wrong",
			Config{URL: testURL, WebsiteID: "x"},
			func(o Options) Options { o.Logger, o.Hostname = nil, ""; return o },
			[]string{"WEBSITE_ID", "Hostname", "Logger"},
		},
		{"disabled needs nothing", Config{}, func(Options) Options { return Options{} }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.cfg, tt.opts(good))
			if len(tt.errs) == 0 {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				_ = c.Close(t.Context())
				return
			}
			if err == nil {
				t.Fatal("New returned no error")
			}
			for _, want := range tt.errs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestSlowUmamiDoesNotDelayTheResponse(t *testing.T) {
	r := newRig(t, func(_ *Config, _ *Options, f *fakeUmami) {
		f.block = make(chan struct{})
		f.entered = make(chan struct{}, 8)
	})
	h := r.c.Middleware(siteMux())

	done := make(chan struct{})
	go func() {
		serve(h, request(http.MethodGet, "/about", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the response waited for Umami")
	}
	select {
	case <-r.fake.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the view never reached Umami")
	}
	close(r.fake.block)
	if got := len(r.drain(t)); got != 1 {
		t.Errorf("got %d posts", got)
	}
}

func TestFullQueueDropsAndCounts(t *testing.T) {
	r := newRig(t, func(cfg *Config, _ *Options, f *fakeUmami) {
		cfg.QueueSize = 1
		f.block = make(chan struct{})
		f.entered = make(chan struct{}, 8)
	})
	h := r.c.Middleware(siteMux())
	hit := func() { serve(h, request(http.MethodGet, "/about", nil)) }

	// Both workers hold a view, so the queue is empty again. Each view is
	// awaited, or the second one would meet the still full queue of one.
	for range workers {
		hit()
		select {
		case <-r.fake.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("a worker never took a view")
		}
	}
	hit() // fills the queue of one
	hit() // dropped
	hit() // dropped

	if got := r.c.Stats().Dropped; got != 2 {
		t.Errorf("Dropped = %d, want 2", got)
	}
	if got := r.drops.Load(); got != 2 {
		t.Errorf("OnDrop called %d times, want 2", got)
	}
	lines := r.logs.lines(t)
	if len(lines) != 1 || lines[0]["class"] != "queue_full" {
		t.Fatalf("log lines = %v, want one queue_full line", lines)
	}

	close(r.fake.block)
	if got := len(r.drain(t)); got != workers+1 {
		t.Errorf("got %d posts, want %d", got, workers+1)
	}
	if got := r.c.Stats(); got.Sent != workers+1 || got.Dropped != 2 {
		t.Errorf("stats = %+v", got)
	}
}

func TestQueueIsBounded(t *testing.T) {
	r := newRig(t, func(cfg *Config, _ *Options, f *fakeUmami) {
		cfg.QueueSize = 3
		f.block = make(chan struct{})
		f.entered = make(chan struct{}, 64)
	})
	for range 50 {
		r.c.Track(View{URL: "/"})
	}
	// Two in flight at most plus three queued: everything else is dropped.
	waitFor(t, "workers", func() bool { return r.fake.calls.Load() == workers })
	if got := r.c.Stats().Dropped; got < 50-workers-3 {
		t.Errorf("Dropped = %d, the queue holds more than its bound", got)
	}
	close(r.fake.block)
	r.drain(t)
	if got := r.fake.calls.Load(); got > workers+3 {
		t.Errorf("%d posts, more than workers plus queue", got)
	}
}

func TestFailuresAreLoggedOncePerClassAndInterval(t *testing.T) {
	secretErr := &url.Error{
		Op:  "Post",
		URL: "https://umami.example.test/secret-path-token/api/send",
		Err: errors.New("connection refused"),
	}
	r := newRig(t, func(cfg *Config, _ *Options, f *fakeUmami) {
		cfg.URL = testURL + "/secret-path-token"
		f.err = secretErr
	})
	for range 5 {
		r.c.Track(View{URL: "/", UserAgent: testUA, IP: testAddr})
	}
	waitFor(t, "five failures", func() bool { return r.c.Stats().Failed == 5 })

	lines := r.logs.lines(t)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines for five failures, want 1: %s", len(lines), r.logs.String())
	}
	if lines[0]["class"] != "transport" || lines[0]["count"] != float64(1) || lines[0]["level"] != "WARN" {
		t.Errorf("line = %v", lines[0])
	}

	r.clock.Advance(2 * time.Minute)
	r.c.Track(View{URL: "/", UserAgent: testUA, IP: testAddr})
	waitFor(t, "second line", func() bool { return len(r.logs.lines(t)) == 2 })
	if got := r.logs.lines(t)[1]["count"]; got != float64(5) {
		t.Errorf("second line count = %v, want the 5 gathered since", got)
	}

	r.drain(t)
	out := r.logs.String()
	for _, secret := range []string{"secret-path-token", "connection refused", "203.0.113", "Firefox", "/api/send"} {
		if strings.Contains(out, secret) {
			t.Errorf("log holds %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "umami.example.test") {
		t.Error("log names no host to look at")
	}
}

func TestFailureClasses(t *testing.T) {
	tests := []struct {
		name  string
		fake  func(*fakeUmami)
		class string
	}{
		{"server error", func(f *fakeUmami) { f.status = 503 }, "status_5xx"},
		{"client error", func(f *fakeUmami) { f.status = 401 }, "status_4xx"},
		{"redirect", func(f *fakeUmami) { f.status = 302 }, "status_3xx"},
		{"timeout", func(f *fakeUmami) { f.err = &url.Error{Op: "Post", URL: "x", Err: context.DeadlineExceeded} }, "timeout"},
		{"transport", func(f *fakeUmami) { f.err = errors.New("no route to host") }, "transport"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, func(_ *Config, _ *Options, f *fakeUmami) { tt.fake(f) })
			r.c.Track(View{URL: "/", UserAgent: testUA, IP: testAddr})
			waitFor(t, "a failure", func() bool { return r.c.Stats().Failed == 1 })
			lines := r.logs.lines(t)
			if len(lines) != 1 || lines[0]["class"] != tt.class {
				t.Errorf("lines = %v, want class %s", lines, tt.class)
			}
			if got := r.c.Stats(); got.Sent != 0 {
				t.Errorf("a failed post counted as sent: %+v", got)
			}
		})
	}
}

func TestRequestTimeoutBoundsASlowUmami(t *testing.T) {
	r := newRig(t, func(cfg *Config, _ *Options, f *fakeUmami) {
		cfg.Timeout = time.Second
		f.block = make(chan struct{}) // never closed: only the timeout ends the post
	})
	r.c.Track(View{URL: "/", UserAgent: testUA, IP: testAddr})
	waitFor(t, "the timeout", func() bool { return r.c.Stats().Failed == 1 })
	lines := r.logs.lines(t)
	if len(lines) != 1 || lines[0]["class"] != "timeout" {
		t.Errorf("lines = %v", lines)
	}
}

func TestCloseDrainsTheQueue(t *testing.T) {
	r := newRig(t, func(cfg *Config, _ *Options, f *fakeUmami) {
		cfg.QueueSize = 8
		f.block = make(chan struct{})
		f.entered = make(chan struct{}, 8)
	})
	for range 6 {
		r.c.Track(View{URL: "/", UserAgent: testUA, IP: testAddr})
	}
	errc := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errc <- r.c.Close(ctx)
	}()
	select {
	case err := <-errc:
		t.Fatalf("Close returned %v before the queue drained", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(r.fake.block)
	if err := <-errc; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := r.c.Stats(); got.Sent != 6 || got.Dropped != 0 {
		t.Errorf("stats = %+v, want 6 sent", got)
	}
}

func TestCloseDeadlineAbortsAndCountsTheRest(t *testing.T) {
	before := runtime.NumGoroutine()
	r := newRig(t, func(cfg *Config, _ *Options, f *fakeUmami) {
		cfg.QueueSize = 8
		f.block = make(chan struct{}) // never released
		f.entered = make(chan struct{}, 8)
	})
	for range 5 {
		r.c.Track(View{URL: "/", UserAgent: testUA, IP: testAddr})
	}
	for range workers {
		<-r.fake.entered
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := r.c.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want the deadline error", err)
	}
	waitFor(t, "the queued views to be counted lost", func() bool { return r.c.Stats().Dropped == 5-workers })
	if got := r.drops.Load(); got != 5-workers {
		t.Errorf("OnDrop called %d times, want %d", got, 5-workers)
	}
	waitForGoroutines(t, before)
}

func TestTrackAfterCloseDropsWithoutPanic(t *testing.T) {
	r := newRig(t, nil)
	r.drain(t)
	r.c.Track(View{URL: "/"})
	if got := r.c.Stats().Dropped; got != 1 {
		t.Errorf("Dropped = %d, want 1", got)
	}
	if err := r.c.Close(t.Context()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 5 {
		r := newRig(t, nil)
		h := r.c.Middleware(siteMux())
		for range 10 {
			serve(h, request(http.MethodGet, "/about", nil))
		}
		if got := len(r.drain(t)); got != 10 {
			t.Fatalf("got %d posts", got)
		}
	}
	waitForGoroutines(t, before)
}

func waitForGoroutines(t *testing.T, limit int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > limit {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("%d goroutines, started with %d:\n%s", runtime.NumGoroutine(), limit, buf)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
