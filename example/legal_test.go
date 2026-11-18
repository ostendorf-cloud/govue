// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// legalHeader is what a hostile or confused client sends with a legal path. A
// bad bearer token would get 401 from the session middleware in the route
// chain. The legal redirects sit outside it.
func legalHeader(extra http.Header) http.Header {
	header := http.Header{"Authorization": {"Bearer not-a-token"}, "Origin": {"https://foreign.example"}}
	for k, v := range extra {
		header[k] = v
	}
	return header
}

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
		{"/privacy?next=//evil.example", http.Header{"Cookie": {"govue-language=en"}}, "/en/privacy"},
		{"/impressum", http.Header{"Accept-Language": {"en"}}, "/de/imprint"},
		{"/datenschutz", nil, "/de/privacy"},
		{"/datenschutzerklaerung/", nil, "/de/privacy"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			res := do(t, h, http.MethodGet, tc.target, legalHeader(tc.header))
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

// follow sends target through h and follows one redirect, the way a browser
// does. It returns the status and body of the page it lands on. The header is
// a browser's: the page sits in the route chain, where a bad bearer token
// would get 401.
func follow(t *testing.T, h http.Handler, target string, header http.Header) (int, string) {
	t.Helper()
	res := do(t, h, http.MethodGet, target, header)
	_ = bodyOf(t, res)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("GET %s = %d, want 302", target, res.StatusCode)
	}
	loc := res.Header.Get("Location")
	page := do(t, h, http.MethodGet, loc, header)
	return page.StatusCode, bodyOf(t, page)
}

func TestLegalRedirectsLandOnAServedPage(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()

	for _, tc := range []struct {
		target string
		header http.Header
		want   string
	}{
		{"/imprint", nil, "page:/de/imprint"},
		{"/imprint/", http.Header{"Accept-Language": {"en"}}, "page:/en/imprint"},
		{"/privacy", http.Header{"Cookie": {"govue-language=en"}}, "page:/en/privacy"},
		{"/impressum", nil, "page:/de/imprint"},
		{"/datenschutz", nil, "page:/de/privacy"},
		{"/datenschutzerklaerung", nil, "page:/de/privacy"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			status, body := follow(t, h, tc.target, tc.header)
			if status != http.StatusOK {
				t.Fatalf("landed on status %d, want 200", status)
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("landed on %q, want %s", body, tc.want)
			}
		})
	}
}

// TestTermsAreOffByDefault pins the zero value: the example sells nothing, so
// /terms and /agb answer the catch-all 404 instead of a redirect to a page
// that does not exist.
func TestTermsAreOffByDefault(t *testing.T) {
	s := writeTestSite(t)
	h := newSiteApp(t, s.dir).handler()
	for _, target := range []string{"/terms", "/terms/", "/agb", "/agb/"} {
		res := do(t, h, http.MethodGet, target, nil)
		_ = bodyOf(t, res)
		if res.StatusCode != http.StatusNotFound || res.Header.Get("Location") != "" {
			t.Fatalf("GET %s = %d, Location %q, want 404", target, res.StatusCode, res.Header.Get("Location"))
		}
	}
}

// TestTermsVariantLandsOnTheTermsPage is a site that sells something: it has
// a terms page and sets Terms.
func TestTermsVariantLandsOnTheTermsPage(t *testing.T) {
	s := writeTestSiteWith(t, append(slices.Clone(fixtureRoutes), "/terms"))
	a := newSiteApp(t, s.dir)
	a.legal.Terms = true
	h := a.handler()

	for _, tc := range []struct {
		target string
		header http.Header
		want   string
	}{
		{"/terms", nil, "page:/de/terms"},
		{"/terms", http.Header{"Accept-Language": {"en"}}, "page:/en/terms"},
		{"/agb/", http.Header{"Accept-Language": {"en"}}, "page:/de/terms"},
	} {
		status, body := follow(t, h, tc.target, tc.header)
		if status != http.StatusOK || !strings.Contains(body, tc.want) {
			t.Fatalf("%s landed on %d %q, want 200 %s", tc.target, status, body, tc.want)
		}
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
