// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package language_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/language"
)

// legalMux builds a mux like a site's: the legal redirects plus a catch-all
// that answers 404, so a path the set does not own is visible as such.
func legalMux(t *testing.T, cfg language.LegalConfig) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := language.HandleLegal(mux, cfg, nil); err != nil {
		t.Fatalf("HandleLegal: %v", err)
	}
	mux.Handle("/", http.NotFoundHandler())
	return mux
}

func TestHandleLegal_table(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})

	// The whole table, without cookie and header, so the visitor dependent
	// paths go to the default.
	tests := []struct {
		path   string
		want   string
		varies bool
	}{
		{"/imprint", "/de/imprint", true},
		{"/privacy", "/de/privacy", true},
		{"/terms", "/de/terms", true},
		{"/impressum", "/de/imprint", false},
		{"/datenschutz", "/de/privacy", false},
		{"/datenschutzerklaerung", "/de/privacy", false},
		{"/agb", "/de/terms", false},
	}
	for _, tt := range tests {
		for _, path := range []string{tt.path, tt.path + "/"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(method+" "+path, func(t *testing.T) {
					t.Parallel()
					rec := serve(mux, method, path, nil)
					if rec.Code != http.StatusFound {
						t.Fatalf("status = %d, want 302", rec.Code)
					}
					if got := rec.Header().Get("Location"); got != tt.want {
						t.Fatalf("Location = %q, want %q", got, tt.want)
					}
					if got := rec.Header().Get("Cache-Control"); got != "no-store" {
						t.Fatalf("Cache-Control = %q", got)
					}
					vary := strings.Join(rec.Header().Values("Vary"), ",")
					if tt.varies && vary != "Accept-Language, Cookie" {
						t.Fatalf("Vary = %q, want Accept-Language, Cookie", vary)
					}
					if !tt.varies && vary != "" {
						t.Fatalf("Vary = %q on a fixed redirect", vary)
					}
					if rec.Body.Len() != 0 {
						t.Fatalf("body = %q, want none", rec.Body.String())
					}
				})
			}
		}
	}
}

func TestHandleLegal_germanAliasesIgnoreTheVisitor(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})
	hdr := map[string]string{"Cookie": "govue-language=en", "Accept-Language": "en"}
	for path, want := range map[string]string{
		"/impressum":             "/de/imprint",
		"/datenschutz":           "/de/privacy",
		"/datenschutzerklaerung": "/de/privacy",
		"/agb":                   "/de/terms",
	} {
		if got := serve(mux, http.MethodGet, path, hdr).Header().Get("Location"); got != want {
			t.Errorf("%s: Location = %q, want %q", path, got, want)
		}
	}
}

func TestHandleLegal_localeOrder(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})

	tests := []struct {
		name   string
		cookie string
		accept string
		want   string
	}{
		{"cookie beats header", "govue-language=en", "de", "/en/privacy"},
		{"cookie de beats header en", "govue-language=de", "en", "/de/privacy"},
		{"header without cookie", "", "en-GB,de;q=0.5", "/en/privacy"},
		{"unknown cookie falls to header", "govue-language=fr", "en", "/en/privacy"},
		{"cookie is case sensitive", "govue-language=EN", "", "/de/privacy"},
		{"path like cookie is ignored", "govue-language=//evil.example", "", "/de/privacy"},
		{"nothing gives the default", "", "", "/de/privacy"},
		{"unknown language gives the default", "", "fr,es;q=0.9", "/de/privacy"},
		{"q values decide", "", "de;q=0.3,en;q=0.8", "/en/privacy"},
		{"q zero excludes", "", "de;q=0,en;q=0.1", "/en/privacy"},
		{"wildcard means the default", "", "*", "/de/privacy"},
		{"wildcard with the default excluded", "", "de;q=0,*", "/en/privacy"},
		{"bad q dropped, rest counts", "", "de;q=abc,en;q=0.5", "/en/privacy"},
		{"junk header", "", ";;,,=;q", "/de/privacy"},
		{"header path text", "", "//evil.example,en", "/en/privacy"},
		{"oversized header is absent", "", strings.Repeat("en,", 2000), "/de/privacy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hdr := map[string]string{}
			if tt.cookie != "" {
				hdr["Cookie"] = tt.cookie
			}
			if tt.accept != "" {
				hdr["Accept-Language"] = tt.accept
			}
			rec := serve(mux, http.MethodGet, "/privacy", hdr)
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleLegal_defaultAndLanguages(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{Config: language.Config{Languages: []string{"de", "en"}, Default: "en"}})
	if got := serve(mux, http.MethodGet, "/imprint", nil).Header().Get("Location"); got != "/en/imprint" {
		t.Fatalf("default en: Location = %q", got)
	}
	if got := serve(mux, http.MethodGet, "/impressum", nil).Header().Get("Location"); got != "/de/imprint" {
		t.Fatalf("German alias with default en: Location = %q", got)
	}

	custom := legalMux(t, language.LegalConfig{Config: language.Config{Cookie: "lang"}})
	rec := serve(custom, http.MethodGet, "/terms", map[string]string{"Cookie": "lang=en; govue-language=de"})
	if got := rec.Header().Get("Location"); got != "/en/terms" {
		t.Fatalf("custom cookie: Location = %q", got)
	}
}

func TestHandleLegal_methods(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions, "BREW"} {
		for _, path := range []string{"/imprint", "/impressum/", "/agb"} {
			rec := serve(mux, method, path, nil)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, path, rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("%s %s: Allow = %q", method, path, got)
			}
			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("%s %s: Location = %q", method, path, got)
			}
		}
	}
}

func TestHandleLegal_withoutTerms(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{WithoutTerms: true})
	for _, path := range []string{"/terms", "/terms/", "/agb", "/agb/"} {
		rec := serve(mux, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("%s: status = %d, Location = %q, want the catch-all 404", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, path := range []string{"/imprint", "/datenschutz"} {
		if rec := serve(mux, http.MethodGet, path, nil); rec.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want 302", path, rec.Code)
		}
	}
}

// TestHandleLegal_notOwned pins the paths the set does not answer: other
// case, a second trailing slash, a sub path, a prefixed page.
func TestHandleLegal_notOwned(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})
	for _, path := range []string{
		"/Imprint", "/IMPRESSUM", "/Datenschutz", "/imprint/x", "/impressum/de",
		"/imprints", "/de/imprint", "/en/privacy", "/datenschutzerklärung",
	} {
		rec := serve(mux, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("%s: status = %d, Location = %q, want 404 without Location", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// TestHandleLegal_noInputInLocation sends hostile requests through the mux
// and checks that no answer points off the site and that our redirects carry
// one of the fixed targets only.
func TestHandleLegal_noInputInLocation(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})
	fixed := map[string]bool{}
	for _, lang := range []string{"de", "en"} {
		for _, page := range []string{"imprint", "privacy", "terms"} {
			fixed["/"+lang+"/"+page] = true
		}
	}
	targets := []string{
		"/imprint?next=//evil.example",
		"/imprint?%0d%0aSet-Cookie:x=y",
		"/impressum/?redirect=https://evil.example/",
		"/privacy?#frag",
		"//evil.example/imprint",
		"//evil.example",
		"/%2fevil.example/imprint",
		"/%5cevil.example",
		"/imprint/%2e%2e/%2e%2e/evil",
		"/./imprint",
		"/x/../imprint",
		"/imprint%2f",
		"/imprint%00",
	}
	for _, target := range targets {
		for _, host := range []string{"", "evil.example"} {
			req := newReq(http.MethodGet, target)
			if host != "" {
				req.Host = host
			}
			req.Header.Set("Accept-Language", "en")
			req.Header.Set("Cookie", "govue-language=//evil.example")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			loc := rec.Header().Get("Location")
			if loc == "" {
				continue
			}
			if strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") || strings.Contains(loc, "://") ||
				strings.ContainsAny(loc, "\r\n?#") {
				t.Errorf("%s (host %q): Location %q leaves the site or carries input", target, host, loc)
			}
			if rec.Code == http.StatusFound && !fixed[loc] {
				t.Errorf("%s (host %q): 302 to %q, not a fixed target", target, host, loc)
			}
		}
	}
}

// TestHandleLegal_handlerIgnoresThePath calls the registered handlers with
// paths they would never get from the mux. The target stays the table's.
func TestHandleLegal_handlerIgnoresThePath(t *testing.T) {
	t.Parallel()
	mux := legalMux(t, language.LegalConfig{})
	for _, alias := range []struct{ path, want string }{
		{"/impressum", "/de/imprint"},
		{"/imprint", "/en/imprint"},
	} {
		h, pattern := mux.Handler(newReq(http.MethodGet, alias.path))
		if pattern == "/" {
			t.Fatalf("%s: no legal handler", alias.path)
		}
		for _, raw := range []string{"//evil.example/x", "/evil?q=1", "/de/../../etc/passwd"} {
			req := newReq(http.MethodGet, "/")
			req.URL.Path = raw
			req.URL.RawQuery = "next=https://evil.example"
			req.Host = "evil.example"
			req.Header.Set("Accept-Language", "en")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Location"); got != alias.want {
				t.Errorf("%s with path %q: Location = %q, want %q", alias.path, raw, got, alias.want)
			}
		}
	}
}

func TestHandleLegal_wrap(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	wrapped := 0
	err := language.HandleLegal(mux, language.LegalConfig{WithoutTerms: true}, func(next http.Handler) http.Handler {
		wrapped++
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "yes")
			next.ServeHTTP(w, r)
		})
	})
	if err != nil {
		t.Fatalf("HandleLegal: %v", err)
	}
	// imprint, privacy, impressum, datenschutz, datenschutzerklaerung.
	if wrapped != 5 {
		t.Fatalf("wrap called %d times, want 5", wrapped)
	}
	if rec := serve(mux, http.MethodGet, "/datenschutz/", nil); rec.Header().Get("X-Wrapped") != "yes" {
		t.Fatal("handler is not wrapped")
	}
}

func TestHandleLegal_config(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  language.LegalConfig
	}{
		{"no German", language.LegalConfig{Config: language.Config{Languages: []string{"en", "fr"}, Default: "en"}}},
		{"bad code", language.LegalConfig{Config: language.Config{Languages: []string{"de", "../x"}}}},
		{"default not listed", language.LegalConfig{Config: language.Config{Default: "fr"}}},
		{"bad cookie", language.LegalConfig{Config: language.Config{Cookie: "a b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			err := language.HandleLegal(mux, tt.cfg, nil)
			if !errors.Is(err, language.ErrConfig) {
				t.Fatalf("err = %v, want ErrConfig", err)
			}
			if _, pattern := mux.Handler(newReq(http.MethodGet, "/imprint")); pattern != "" {
				t.Fatalf("registered %q despite the error", pattern)
			}
		})
	}
}

func TestHandleLegal_duplicatePanics(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.Handle("GET /imprint", http.NotFoundHandler())
	defer func() {
		if recover() == nil {
			t.Fatal("no panic for a path registered twice")
		}
	}()
	_ = language.HandleLegal(mux, language.LegalConfig{}, nil)
}
