// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"net/http"
	"strings"
	"testing"
)

// notModifiedMux registers the page handlers of siteMux through
// NotModified, the way a server with revalidated pages does.
func notModifiedMux(c *Client) *http.ServeMux {
	mux := http.NewServeMux()
	inner := siteMux()
	for _, pattern := range []string{"GET /about", "GET /cached", "GET /missing", "GET /old", "GET /notes/{id}"} {
		mux.Handle(pattern, c.NotModified(inner))
	}
	return mux
}

func TestNotModifiedCountsA304OnlyByItsPattern(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		target  string
		counted bool
	}{
		{"304", http.MethodGet, "/cached", true},
		{"200 with an id", http.MethodGet, "/notes/secret-id-123?token=abc", false},
		{"200 is left to Middleware", http.MethodGet, "/about", false},
		{"404", http.MethodGet, "/missing", false},
		{"redirect", http.MethodGet, "/old", false},
		{"HEAD", http.MethodHead, "/cached", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)
			serve(notModifiedMux(r.c), request(tt.method, tt.target, nil))
			posts := r.drain(t)
			if got := len(posts) == 1; got != tt.counted {
				t.Errorf("counted = %v, want %v", got, tt.counted)
			}
			for _, p := range posts {
				if v := view(t, p); v["url"] != "/cached" {
					t.Errorf("url = %v, want the pattern", v["url"])
				}
			}
		})
	}
}

// TestNotModifiedKeepsThePrivacyRules runs the visitor rules of Middleware
// against a 304: a signal, a bot or a private client counts nothing, and a
// counted view carries the pattern and a masked address only.
func TestNotModifiedKeepsThePrivacyRules(t *testing.T) {
	for name, mod := range map[string]func(*http.Request){
		"do not track":           func(r *http.Request) { r.Header.Set("DNT", "1") },
		"global privacy control": func(r *http.Request) { r.Header.Set("Sec-GPC", "1") },
		"bot":                    func(r *http.Request) { r.Header.Set("User-Agent", "Googlebot/2.1") },
		"private client":         func(r *http.Request) { r.RemoteAddr = "10.0.0.1:4000" },
		"prefetch":               func(r *http.Request) { r.Header.Set("Sec-Purpose", "prefetch") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil)
			serve(notModifiedMux(r.c), request(http.MethodGet, "/cached", mod))
			if posts := r.drain(t); len(posts) != 0 {
				t.Errorf("counted a 304 the rules leave out")
			}
		})
	}

	r := newRig(t, nil)
	serve(notModifiedMux(r.c), request(http.MethodGet, "/cached?utm=x", nil))
	posts := r.drain(t)
	if len(posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(posts))
	}
	body := string(posts[0].Body)
	if strings.Contains(body, "utm") || strings.Contains(body, "203.0.113.7") {
		t.Errorf("the view carries the query or the full address: %s", body)
	}
}

func TestNotModifiedOfADisabledClientIsTheHandler(t *testing.T) {
	var c Client
	h := http.HandlerFunc(htmlPage)
	if got := c.NotModified(h); got == nil {
		t.Fatal("nil handler")
	}
	rec := serve(c.NotModified(h), request(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}
