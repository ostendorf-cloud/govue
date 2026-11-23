// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/frontend"
)

// TestNonCanonicalPathsAnswer404BeforeRouting sends each path as a request
// line, so it arrives parsed the way a server parses it. Every one answers
// the default 404 page, with no redirect, and none reaches a file: the build
// holds a dotfile, the Vite manifest, the template and a page the sitemap
// does not list, and their contents never appear.
func TestNonCanonicalPathsAnswer404BeforeRouting(t *testing.T) {
	h := newServer(t).Handler()
	for _, target := range nonCanonical {
		t.Run(target, func(t *testing.T) {
			res := do(t, h, http.MethodGet, target, nil)
			got := body(t, res)
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (Location %q)", res.StatusCode, res.Header.Get("Location"))
			}
			if !strings.Contains(got, "nicht gefunden") {
				t.Errorf("not the default 404 page: %q", got)
			}
			for _, leak := range []string{secretText, "SECRET=1", "manifest", "<!--app-html-->", "console.log", "hidden"} {
				if strings.Contains(got, leak) {
					t.Errorf("the answer leaks %q: %s", leak, got)
				}
			}
			if res.Header.Get("Location") != "" || res.Header.Get("Set-Cookie") != "" {
				t.Errorf("unexpected Location or Set-Cookie: %v", res.Header)
			}
		})
	}
}

// TestCanonicalPathRefusesEachRuleOnItsOwn runs the check without the table
// of served paths behind it, which refuses most of these a second time.
func TestCanonicalPathRefusesEachRuleOnItsOwn(t *testing.T) {
	for _, target := range nonCanonical {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if frontend.CanonicalPath(req) {
			t.Errorf("%s passes the check", target)
		}
	}
	for _, target := range canonical {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if !frontend.CanonicalPath(req) {
			t.Errorf("%s fails the check", target)
		}
	}
}

// canonical are paths the site serves, in the encodings a browser sends.
var canonical = []string{
	"/", "/de/", "/en/imprint", "/en/%C3%BCber", "/en/über", "/de/a%26b", "/de/a&b",
	"/" + hashedJS, "/" + nestedFont, "/" + ogImage, "/robots.txt", "/de/?q=%2e%2e",
	"/de/imprint/", "/de/a.b", "/de/a..b",
}

// nonCanonical are paths the check refuses, one or more per rule.
var nonCanonical = []string{
	// dot segments, plain and encoded
	"/de/../en/",
	"/de/./imprint",
	"/de/imprint/.",
	"/de/imprint/..",
	"/assets/../index.html",
	"/%2e%2e/index.html",
	"/de/%2e%2e/en/",
	"/de/%2E%2E/en/",
	"/de/.%2e/en/",
	"/assets/%2e%2e/.vite/manifest.json",
	"/de/imprint%2e",
	// double slashes
	"//de/",
	"/de//imprint",
	"/assets//" + strings.TrimPrefix(hashedJS, "assets/"),
	"//example.org/",
	"///de/",
	// backslashes, plain and encoded
	"/de\\imprint",
	"/assets\\..\\index.html",
	"/de/%5cimprint",
	"/de/%5Cimprint",
	"/assets/..%5c..%5cindex.html",
	// encoded slash
	"/de%2fimprint",
	"/de%2Fimprint",
	"/assets%2f" + strings.TrimPrefix(hashedJS, "assets/"),
	// NUL and other control characters
	"/de/imprint%00",
	"/de/%00/",
	"/assets/index-BXa3k2_9.js%00.png",
	"/de/imprint%0a",
	"/de/imprint%0d%0aSet-Cookie:x=1",
	"/de/%7f",
	"/de/%c2%85",
	// double encoding and percent
	"/de/%252e%252e/en/",
	"/de/%25",
	"/%252f",
	// invalid UTF-8
	"/de/%ff",
	"/de/%c3",
	// dotfiles and the Vite manifest
	"/.env",
	"/.vite/manifest.json",
	"/.vite/",
	"/assets/.hidden.js",
	"/assets/.git/config",
	"/.well-known/security.txt",
	"/de/.imprint",
	// not absolute
	"*",
}

// TestRawRequestLines feeds request lines a client could send but
// httptest.NewRequest would refuse or normalise, through http.ReadRequest,
// the parser of the server.
func TestRawRequestLines(t *testing.T) {
	h := newServer(t).Handler()
	for _, line := range []string{
		"GET /de/..%2fen/ HTTP/1.1",
		"GET /de/%2e%2e%2fen/ HTTP/1.1",
		"GET /assets/%2e%2e%2f%2e%2e%2fsitemap.xml HTTP/1.1",
		"GET /de/imprint/%2e HTTP/1.1",
	} {
		t.Run(line, func(t *testing.T) {
			req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(line + "\r\nHost: example.test\r\n\r\n")))
			if err != nil {
				// The parser refusing it is as good as a 404.
				return
			}
			req = req.WithContext(t.Context())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
}

// TestCanonicalPathsStillWork is the other half: the check refuses no path
// the site serves, in the encodings a browser sends.
func TestCanonicalPathsStillWork(t *testing.T) {
	h := newServer(t).Handler()
	for _, target := range []string{
		"/de/", "/en/imprint", "/en/%C3%BCber", "/en/über", "/de/a%26b", "/de/a&b",
		"/" + hashedJS, "/" + nestedFont, "/" + ogImage, "/robots.txt", "/de/?q=%2e%2e",
	} {
		res := do(t, h, http.MethodGet, target, nil)
		_ = body(t, res)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", target, res.StatusCode)
		}
	}
}
