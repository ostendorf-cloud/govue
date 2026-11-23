// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"
)

// TestServeHandsOptionsStarToTheHandler pins DisableGeneralOptionsHandler:
// without it net/http answers OPTIONS * itself with an empty 200, and the
// handler, with its security headers, never sees the request. The request
// is written as a raw request line through a real http.Server on a pipe.
func TestServeHandsOptionsStarToTheHandler(t *testing.T) {
	t.Parallel()

	listener := newMemListener()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, Config{
			Logger: discardLogger(),
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Reached", r.Method+" "+r.RequestURI)
				http.NotFound(w, r)
			}),
			DrainDelay: 10 * time.Millisecond,
		}, listener)
	}()

	conn, err := listener.dial(ctx)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(safetyNet))
	if _, err := conn.Write([]byte("OPTIONS * HTTP/1.1\r\nHost: memory\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = res.Body.Close()

	if got := res.Header.Get("X-Reached"); got != "OPTIONS *" {
		t.Errorf("the handler did not see OPTIONS *: status %d, X-Reached %q", res.StatusCode, got)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want the handler's 404", res.StatusCode)
	}

	stop()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("Serve = %v, want nil for a clean shutdown", err)
		}
	case <-time.After(safetyNet):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}
