// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package language_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/language"
)

func mustRedirect(t *testing.T, cfg language.Config) http.Handler {
	t.Helper()
	h, err := language.Redirect(cfg)
	if err != nil {
		t.Fatalf("Redirect: %v", err)
	}
	return h
}

func newReq(method, target string) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), method, target, nil)
}

func serve(h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	req := newReq(method, target)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCookieName(t *testing.T) {
	t.Parallel()
	// The Vue package writes this name, see web/src/i18n/languageStorage.ts.
	if language.CookieName != "govue-language" {
		t.Fatalf("CookieName = %q", language.CookieName)
	}
}

func TestRedirect_order(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})

	tests := []struct {
		name   string
		cookie string // raw Cookie header, empty for none
		accept string
		want   string
	}{
		{"cookie beats header", "govue-language=en", "de", "/en/"},
		{"header without cookie", "", "en-US,de;q=0.5", "/en/"},
		{"unknown cookie falls to header", "govue-language=fr", "en", "/en/"},
		{"path like cookie falls to default", "govue-language=../evil", "", "/de/"},
		{"nothing at all", "", "", "/de/"},
		{"cookie is case sensitive", "govue-language=EN", "", "/de/"},
		{"empty cookie value", "govue-language=", "en", "/en/"},
		{"other cookie name ignored", "language=en", "", "/de/"},
		{"second cookie of the same name counts", "govue-language=fr; govue-language=en", "", "/en/"},
		{"cookie de beats header en", "govue-language=de", "en", "/de/"},
		{"unmatched header uses default", "", "fr,es", "/de/"},
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
			rec := serve(h, http.MethodGet, "/", hdr)
			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedirect_malformedHeader(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})

	tests := []struct {
		name   string
		accept string
		want   string
	}{
		{"bad q and out of range wildcard", "de;q=abc, *;q=2", "/de/"},
		{"bad q entry dropped rest counts", "de;q=abc, en;q=0.5", "/en/"},
		{"q zero excludes", "de;q=0, en", "/en/"},
		{"q above one dropped", "en;q=1.0001", "/de/"},
		{"separators only", ";;,,", "/de/"},
		{"oversized header is absent", strings.Repeat("en,", 2000), "/de/"},
		{"entry past the cap ignored", strings.Repeat("fr,", 39) + "en", "/de/"},
		{"wildcard alone", "*", "/de/"},
		{"wildcard with default excluded", "de;q=0, *", "/en/"},
		{"wildcard lower than named", "*;q=0.1, en;q=0.2", "/en/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := serve(h, http.MethodGet, "/", map[string]string{"Accept-Language": tt.accept})
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedirect_multipleHeaderLines(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	req := newReq(http.MethodGet, "/")
	req.Header.Add("Accept-Language", "fr")
	req.Header.Add("Accept-Language", "en")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); got != "/en/" {
		t.Fatalf("Location = %q", got)
	}
}

func TestRedirect_headers(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	rec := serve(h, http.MethodGet, "/", nil)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	vary := strings.Join(rec.Header().Values("Vary"), ",")
	for _, want := range []string{"Accept-Language", "Cookie"} {
		if !strings.Contains(vary, want) {
			t.Fatalf("Vary = %q, missing %s", vary, want)
		}
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want none", rec.Body.String())
	}
}

func TestRedirect_varyKeepsEarlierValues(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	rec := httptest.NewRecorder()
	rec.Header().Add("Vary", "Origin")
	h.ServeHTTP(rec, newReq(http.MethodGet, "/"))
	if got := strings.Join(rec.Header().Values("Vary"), ","); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary = %q, lost Origin", got)
	}
}

func TestRedirect_head(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	get := serve(h, http.MethodGet, "/?a=1", map[string]string{"Accept-Language": "en"})
	head := serve(h, http.MethodHead, "/?a=1", map[string]string{"Accept-Language": "en"})

	if head.Code != http.StatusFound || head.Header().Get("Location") != "/en/?a=1" {
		t.Fatalf("HEAD = %d %q", head.Code, head.Header().Get("Location"))
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body = %q", head.Body.String())
	}
	if head.Header().Get("Cache-Control") != get.Header().Get("Cache-Control") ||
		strings.Join(head.Header().Values("Vary"), ",") != strings.Join(get.Header().Values("Vary"), ",") {
		t.Fatalf("HEAD headers differ from GET: %v vs %v", head.Header(), get.Header())
	}
}

func TestRedirect_query(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"kept", "/?utm=a&b=1", "/de/?utm=a&b=1"},
		{"empty query dropped", "/", "/de/"},
		{"encoded newline stays encoded", "/?x=%0d%0aSet-Cookie:a=b", "/de/?x=%0d%0aSet-Cookie:a=b"},
		{"slashes stay in the query", "/?next=//example.com", "/de/?next=//example.com"},
		{"url in the query", "/?next=https://example.com/", "/de/?next=https://example.com/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := serve(h, http.MethodGet, tt.target, nil)
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedirect_rawQueryBytesEscaped(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	req := newReq(http.MethodGet, "/")
	// A handler can see these through a proxy or a test, so the handler must
	// not trust the server to have refused them.
	req.URL.RawQuery = "a=1\r\nX-Evil: 1 #f\x7f\xc3\xa4"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	loc := rec.Header().Get("Location")
	if loc != "/de/?a=1%0D%0AX-Evil:%201%20%23f%7F%C3%A4" {
		t.Fatalf("Location = %q", loc)
	}
	if len(rec.Header().Values("X-Evil")) != 0 {
		t.Fatal("header injected")
	}
}

func TestRedirect_noRequestDataInLocation(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})

	tests := []struct {
		name   string
		target string
		host   string
		hdr    map[string]string
	}{
		{"host header", "/", "evil.example.com", nil},
		{"cookie value", "/", "", map[string]string{"Cookie": "govue-language=evil.example.com"}},
		{"cookie with slashes", "/", "", map[string]string{"Cookie": "govue-language=//evil.example.com"}},
		{"accept language text", "/", "", map[string]string{"Accept-Language": "//evil.example.com"}},
		{"forwarded host", "/", "", map[string]string{"X-Forwarded-Host": "evil.example.com"}},
		{"query with an url", "/?u=//evil.example.com", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := newReq(http.MethodGet, tt.target)
			if tt.host != "" {
				req.Host = tt.host
			}
			for k, v := range tt.hdr {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			loc := rec.Header().Get("Location")
			if loc != "/de/" && !strings.HasPrefix(loc, "/de/?") {
				t.Fatalf("Location = %q", loc)
			}
			if strings.Contains(strings.SplitN(loc, "?", 2)[0], "evil") {
				t.Fatalf("request data in the path of Location %q", loc)
			}
		})
	}
}

func TestRedirect_wrongMethod(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			rec := serve(h, m, "/", nil)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("Allow = %q", got)
			}
			if rec.Header().Get("Location") != "" {
				t.Fatal("Location set on 405")
			}
		})
	}
}

func TestRedirect_otherPaths(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{})
	for _, p := range []string{"/de/", "/en/", "/de", "/impressum", "/fr/", "//", "/index.html"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			req := newReq(http.MethodGet, "/")
			req.URL.Path = p
			req.Header.Set("Accept-Language", "en")
			req.Header.Set("Cookie", "govue-language=en")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if rec.Header().Get("Location") != "" {
				t.Fatal("Location set on 404")
			}
		})
	}
}

// TestRedirect_mounted pins the ServeMux behaviour the package relies on:
// GET /{$} matches only the root and HEAD, a prefixed page is never
// redirected, and /de answers with a 307 to /de/ that keeps the query.
func TestRedirect_mounted(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", mustRedirect(t, language.Config{}))
	mux.HandleFunc("GET /en/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /de/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name         string
		method       string
		target       string
		hdr          map[string]string
		wantStatus   int
		wantLocation string
	}{
		{"root without header", "GET", "/", nil, 302, "/de/"},
		{"root head", "HEAD", "/", nil, 302, "/de/"},
		{"crawler on en with de cookie and header", "GET", "/en/", map[string]string{"Cookie": "govue-language=de", "Accept-Language": "de"}, 200, ""},
		{"crawler on de with en cookie and header", "GET", "/de/", map[string]string{"Cookie": "govue-language=en", "Accept-Language": "en"}, 200, ""},
		{"prefix without slash", "GET", "/de?x=1", nil, 307, "/de/?x=1"},
		{"unknown prefix", "GET", "/fr/", nil, 404, ""},
		{"upper case prefix", "GET", "/DE/", nil, 404, ""},
		{"unprefixed public path", "GET", "/impressum", nil, 404, ""},
		{"post to root", "POST", "/", nil, 405, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := serve(mux, tt.method, tt.target, tt.hdr)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Location"); got != tt.wantLocation {
				t.Fatalf("Location = %q, want %q", got, tt.wantLocation)
			}
		})
	}
}

func TestRedirect_customConfig(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{
		Languages: []string{"fr", "en", "de"},
		Default:   "fr",
		Cookie:    "lang",
	})

	tests := []struct {
		name   string
		cookie string
		accept string
		want   string
	}{
		{"custom cookie", "lang=de", "", "/de/"},
		{"default cookie name ignored", "govue-language=de", "", "/fr/"},
		{"header", "", "en", "/en/"},
		{"wildcard goes to the default", "", "*", "/fr/"},
		{"wildcard with the default named zero", "", "fr;q=0, *", "/en/"},
		{"nothing", "", "", "/fr/"},
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
			rec := serve(h, http.MethodGet, "/", hdr)
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedirect_defaultNotListedFirst(t *testing.T) {
	t.Parallel()
	h := mustRedirect(t, language.Config{Languages: []string{"en", "de"}, Default: "de"})
	for _, accept := range []string{"*", "", "fr"} {
		rec := serve(h, http.MethodGet, "/", map[string]string{"Accept-Language": accept})
		if got := rec.Header().Get("Location"); got != "/de/" {
			t.Fatalf("Accept-Language %q: Location = %q, want /de/", accept, got)
		}
	}
}

func TestRedirect_languageCount(t *testing.T) {
	t.Parallel()
	codes := manyCodes(32)
	h := mustRedirect(t, language.Config{Languages: codes, Default: codes[0]})
	rec := serve(h, http.MethodGet, "/", map[string]string{"Accept-Language": codes[31]})
	if got := rec.Header().Get("Location"); got != "/"+codes[31]+"/" {
		t.Fatalf("Location = %q", got)
	}
}

func TestRedirect_invalidConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  language.Config
	}{
		{"upper case code", language.Config{Languages: []string{"de", "EN"}}},
		{"one letter code", language.Config{Languages: []string{"d", "de"}}},
		{"four letter code", language.Config{Languages: []string{"de", "engl"}}},
		{"slash in code", language.Config{Languages: []string{"de", "e/"}}},
		{"empty code", language.Config{Languages: []string{"de", ""}}},
		{"duplicate code", language.Config{Languages: []string{"de", "en", "de"}}},
		{"default not listed", language.Config{Languages: []string{"de", "en"}, Default: "fr"}},
		{"implicit default not listed", language.Config{Languages: []string{"en", "fr"}}},
		{"cookie with semicolon", language.Config{Cookie: "a;b"}},
		{"cookie with space", language.Config{Cookie: "a b"}},
		{"cookie with newline", language.Config{Cookie: "a\r\nb"}},
		{"too many languages", language.Config{Languages: manyCodes(33)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, err := language.Redirect(tt.cfg)
			if !errors.Is(err, language.ErrConfig) {
				t.Fatalf("err = %v, want ErrConfig", err)
			}
			if h != nil {
				t.Fatal("handler returned with an error")
			}
		})
	}
}

func TestRedirect_invalidConfigAggregates(t *testing.T) {
	t.Parallel()
	_, err := language.Redirect(language.Config{Languages: []string{"EN"}, Cookie: "a b"})
	if err == nil {
		t.Fatal("no error")
	}
	if n := strings.Count(err.Error(), "\n") + 1; n < 3 {
		t.Fatalf("got %d error lines, want every problem reported: %v", n, err)
	}
}

func manyCodes(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, string([]byte{'a' + byte(i/26), 'a' + byte(i%26)}))
	}
	return out
}
