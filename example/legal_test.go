// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestLegalPathsRedirectThroughTheWholeChain(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, tc := range []struct {
		target string
		header http.Header
		want   string
	}{
		{"/imprint", nil, "/de/imprint"},
		{"/privacy/", http.Header{"Accept-Language": {"en-US"}}, "/en/privacy"},
		{"/terms?next=//evil.example", http.Header{"Cookie": {"govue-language=en"}}, "/en/terms"},
		{"/impressum", http.Header{"Accept-Language": {"en"}}, "/de/imprint"},
		{"/datenschutz", nil, "/de/privacy"},
		{"/datenschutzerklaerung/", nil, "/de/privacy"},
		{"/agb", nil, "/de/terms"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			// A bad bearer token would get 401 from the session middleware in
			// the route chain. The legal redirects sit outside it.
			header := http.Header{"Authorization": {"Bearer not-a-token"}, "Origin": {"https://foreign.example"}}
			for k, v := range tc.header {
				header[k] = v
			}
			res := do(t, h, http.MethodGet, tc.target, header)
			_ = bodyOf(t, res)
			if res.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", res.StatusCode)
			}
			if got := res.Header.Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
			if got := res.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
			if len(res.Cookies()) != 0 {
				t.Fatalf("a redirect set a cookie: %v", res.Cookies())
			}
			// The outer chain applies: the security headers are there.
			if res.Header.Get("X-Content-Type-Options") == "" {
				t.Fatal("no security headers, the outer chain did not run")
			}
		})
	}
}

func TestLegalPathsRefuseOtherMethods(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()
	res := do(t, h, http.MethodPost, "/impressum", nil)
	_ = bodyOf(t, res)
	if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST /impressum = %d, Allow %q", res.StatusCode, res.Header.Get("Allow"))
	}
}

func TestWithoutASiteDirectoryNoLegalRedirectExists(t *testing.T) {
	a, _ := newTestApp(t, 1000)
	h := a.handler()
	for _, target := range []string{"/imprint", "/impressum", "/agb"} {
		res := do(t, h, http.MethodGet, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != http.StatusNotFound || strings.TrimSpace(res.Header.Get("Location")) != "" {
			t.Fatalf("GET %s = %d without SITE_DIR, want 404", target, res.StatusCode)
		}
	}
}
