// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ostendorf-cloud/govue/clientip"
)

func TestPayloadIsExact(t *testing.T) {
	r := newRig(t, func(cfg *Config, opts *Options, _ *fakeUmami) {
		cfg.URL = testURL + "/stats"
		opts.Title = func(*http.Request) string { return "A note" }
	})
	h := r.c.Middleware(siteMux())
	req := request(http.MethodGet, "/notes/8841?token=abc123&utm=x#frag", func(req *http.Request) {
		req.Header.Set("Referer", "https://search.example.org/results/private-path?q=secret#frag")
		req.Header.Set("Cookie", "session=cookie-secret")
		req.Header.Set("Authorization", "Bearer bearer-secret")
	})
	serve(h, req)

	posts := r.drain(t)
	if len(posts) != 1 {
		t.Fatalf("got %d posts, want 1", len(posts))
	}
	p := posts[0]
	if p.Method != http.MethodPost || p.URL != testURL+"/stats/api/send" {
		t.Errorf("request = %s %s", p.Method, p.URL)
	}
	if got := p.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := p.Header.Get("User-Agent"); got != testUA {
		t.Errorf("User-Agent = %q", got)
	}
	for _, name := range []string{"Cookie", "Authorization", "Referer", "X-Forwarded-For"} {
		if got := p.Header.Get(name); got != "" {
			t.Errorf("header %s = %q was forwarded to Umami", name, got)
		}
	}
	want := `{"type":"event","payload":{"website":"` + testID + `","hostname":"` + testHost +
		`","url":"/notes/{id}","title":"A note","referrer":"https://search.example.org/",` +
		`"language":"de-DE","ip":"203.0.113.0","userAgent":"` + testUA + `"}}`
	if string(p.Body) != want {
		t.Errorf("body =\n%s\nwant\n%s", p.Body, want)
	}
	for _, leak := range []string{"8841", "token", "abc123", "utm", "frag", "private-path", "secret", "cookie"} {
		if strings.Contains(string(p.Body), leak) {
			t.Errorf("body holds %q", leak)
		}
	}
}

func TestOptionalFieldsAreOmitted(t *testing.T) {
	r := newRig(t, nil)
	h := r.c.Middleware(siteMux())
	serve(h, request(http.MethodGet, "/about", func(req *http.Request) {
		req.Header.Del("Accept-Language")
	}))
	posts := r.drain(t)
	if len(posts) != 1 {
		t.Fatalf("got %d posts", len(posts))
	}
	got := view(t, posts[0])
	for _, key := range []string{"title", "referrer", "language"} {
		if _, ok := got[key]; ok {
			t.Errorf("payload carries %q without a value", key)
		}
	}
	for _, key := range []string{"screen", "cookie", "id", "session", "name"} {
		if _, ok := got[key]; ok {
			t.Errorf("payload carries unexpected %q", key)
		}
	}
}

func TestReferrerRules(t *testing.T) {
	tests := []struct {
		referer string
		want    string
	}{
		{"https://search.example.org/a/b?q=1", "https://search.example.org/"},
		{"http://User:Pw@Search.Example.org:8080/x", "http://search.example.org:8080/"},
		{"https://" + testHost + "/about", ""},
		{"https://www." + testHost + "/about", ""},
		{"https://SITE.example.test/", ""},
		{"ftp://files.example.org/x", ""},
		{"android-app://com.example.app", ""},
		{"not a url", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.referer, func(t *testing.T) {
			if got := referrer(tt.referer, testHost); got != tt.want {
				t.Errorf("referrer(%q) = %q, want %q", tt.referer, got, tt.want)
			}
		})
	}
}

func TestLanguageRules(t *testing.T) {
	tests := map[string]string{
		"de-DE,de;q=0.9":            "de-DE",
		"en":                        "en",
		"*":                         "",
		"":                          "",
		"<script>alert(1)</script>": "",
		"fr;q=0.5":                  "fr",
		"en-US-x-private-extra-xx":  "",
	}
	for in, want := range tests {
		if got := language(in); got != want {
			t.Errorf("language(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDoNotTrackAndGlobalPrivacyControl(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		counted bool
	}{
		{"no signal", nil, true},
		{"DNT 1", map[string]string{"DNT": "1"}, false},
		{"DNT 1 with space", map[string]string{"DNT": " 1 "}, false},
		{"DNT 0", map[string]string{"DNT": "0"}, true},
		{"Sec-GPC 1", map[string]string{"Sec-GPC": "1"}, false},
		{"Sec-GPC 0", map[string]string{"Sec-GPC": "0"}, true},
		{"both", map[string]string{"DNT": "1", "Sec-GPC": "1"}, false},
		{"DNT 1 comma 1", map[string]string{"DNT": "1, 1"}, false},
		{"DNT 0 comma 1", map[string]string{"DNT": "0, 1"}, false},
		{"GPC 0 comma 1", map[string]string{"Sec-GPC": "0,1"}, false},
		{"DNT yes", map[string]string{"DNT": "yes"}, true},
		{"DNT true", map[string]string{"DNT": "true"}, true},
		{"DNT empty", map[string]string{"DNT": ""}, true},
		{"DNT 10", map[string]string{"DNT": "10"}, true},
		{"DNT 0 and GPC 1", map[string]string{"DNT": "0", "Sec-GPC": "1"}, false},
		{"DNT 1 and GPC 0", map[string]string{"DNT": "1", "Sec-GPC": "0"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)
			h := r.c.Middleware(siteMux())
			serve(h, request(http.MethodGet, "/about", func(req *http.Request) {
				for k, v := range tt.headers {
					req.Header.Set(k, v)
				}
			}))
			if got := len(r.drain(t)) == 1; got != tt.counted {
				t.Errorf("counted = %v, want %v", got, tt.counted)
			}
		})
	}
}

func TestSignalOnAnyHeaderLineCountsNobody(t *testing.T) {
	for _, name := range []string{"DNT", "Sec-GPC"} {
		for _, lines := range [][]string{{"0", "1"}, {"1", "0"}, {"0", "0", "1"}, {"0", "1, 0"}} {
			t.Run(name+" "+strings.Join(lines, "|"), func(t *testing.T) {
				r := newRig(t, nil)
				serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/about", func(req *http.Request) {
					for _, l := range lines {
						req.Header.Add(name, l)
					}
				}))
				if got := len(r.drain(t)); got != 0 {
					t.Errorf("%d views counted", got)
				}
			})
		}
	}
	r := newRig(t, nil)
	serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/about", func(req *http.Request) {
		req.Header.Add("DNT", "0")
		req.Header.Add("DNT", "0")
	}))
	if got := len(r.drain(t)); got != 1 {
		t.Errorf("two DNT 0 lines: counted %d, want 1", got)
	}
}

func TestAddressIsMaskedByDefault(t *testing.T) {
	tests := []struct {
		name   string
		peer   string
		fullIP bool
		want   string
	}{
		{"ipv4 masked to /24", "203.0.113.77:4000", false, "203.0.113.0"},
		{"ipv4 mapped into ipv6 masked as ipv4", "[::ffff:203.0.113.77]:4000", false, "203.0.113.0"},
		{"ipv6 masked to /48", "[2001:db8:1234:5678:9abc:def0:1234:5678]:4000", false, "2001:db8:1234::"},
		{"ipv4 whole with FullIP", "203.0.113.77:4000", true, "203.0.113.77"},
		{"ipv4 mapped whole with FullIP", "[::ffff:203.0.113.77]:4000", true, "203.0.113.77"},
		{"ipv6 whole with FullIP", "[2001:db8:1234:5678:9abc:def0:1234:5678]:4000", true, "2001:db8:1234:5678:9abc:def0:1234:5678"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, func(_ *Config, o *Options, _ *fakeUmami) { o.FullIP = tt.fullIP })
			serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/about", func(req *http.Request) {
				req.RemoteAddr = tt.peer
			}))
			posts := r.drain(t)
			if len(posts) != 1 {
				t.Fatalf("got %d posts", len(posts))
			}
			if got := view(t, posts[0])["ip"]; got != tt.want {
				t.Errorf("ip = %v, want %s", got, tt.want)
			}
		})
	}
}

func TestMaskAlsoCoversViewsHandedToTrack(t *testing.T) {
	r := newRig(t, nil)
	r.c.Track(View{URL: "/", UserAgent: testUA, IP: netip.MustParseAddr("203.0.113.99")})
	r.c.Track(View{URL: "/", UserAgent: testUA})
	posts := r.drain(t)
	if len(posts) != 2 {
		t.Fatalf("got %d posts", len(posts))
	}
	for _, p := range posts {
		ip, has := view(t, p)["ip"]
		if has && ip != "203.0.113.0" {
			t.Errorf("ip = %v", ip)
		}
		if !has && strings.Contains(string(p.Body), "invalid") {
			t.Errorf("an invalid address was written: %s", p.Body)
		}
	}
}

func TestTrackedAddressesAreUnmappedAndLoseTheirZone(t *testing.T) {
	tests := []struct {
		name   string
		ip     string
		fullIP bool
		want   string
	}{
		{"mapped masked", "::ffff:203.0.113.99", false, "203.0.113.0"},
		{"mapped whole", "::ffff:203.0.113.99", true, "203.0.113.99"},
		{"zoned ipv6 whole", "fe80::1%eth0", true, "fe80::1"},
		{"zoned ipv6 masked", "2001:db8:1:2::9%eth0", false, "2001:db8:1::"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, func(_ *Config, o *Options, _ *fakeUmami) { o.FullIP = tt.fullIP })
			r.c.Track(View{URL: "/", UserAgent: testUA, IP: netip.MustParseAddr(tt.ip)})
			posts := r.drain(t)
			if len(posts) != 1 {
				t.Fatalf("got %d posts", len(posts))
			}
			if got := view(t, posts[0])["ip"]; got != tt.want {
				t.Errorf("ip = %v, want %s", got, tt.want)
			}
			if strings.Contains(string(posts[0].Body), "%") {
				t.Errorf("a zone reached Umami: %s", posts[0].Body)
			}
		})
	}
}

func TestNeverSendsQueryOrId(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"id route", "/notes/8841", "/notes/{id}"},
		{"id route with query", "/notes/8841?token=abc&session=zz", "/notes/{id}"},
		{"root with query", "/?utm_source=mail&reset=tok", "/"},
		{"fixed page with query", "/about?next=/notes/1", "/about"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)
			serve(r.c.Middleware(siteMux()), request(http.MethodGet, tt.target, nil))
			posts := r.drain(t)
			if len(posts) != 1 {
				t.Fatalf("got %d posts", len(posts))
			}
			if got := view(t, posts[0])["url"]; got != tt.want {
				t.Errorf("url = %v, want %q", got, tt.want)
			}
			body := string(posts[0].Body)
			for _, leak := range []string{"8841", "token", "abc", "utm_source", "reset", "session", "next="} {
				if strings.Contains(body, leak) {
					t.Errorf("body holds %q: %s", leak, body)
				}
			}
		})
	}
}

type replacedKey struct{}

// replaceRequest is a middleware that, like RequestID, hands the next handler
// a copy of the request. The mux then sets its pattern on the copy, and the
// middleware outside never sees it.
func replaceRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), replacedKey{}, 1)))
	})
}

func TestViewWithoutRoutePatternIsNotCounted(t *testing.T) {
	tests := []struct {
		name   string
		build  func(*Client) http.Handler
		target string
	}{
		{"plain handler with no mux", func(c *Client) http.Handler { return c.Middleware(http.HandlerFunc(htmlPage)) }, "/reset/abc123token"},
		{"request replaced between middleware and mux", func(c *Client) http.Handler {
			return c.Middleware(replaceRequest(siteMux()))
		}, "/notes/4711-secret-slug"},
		{"file server style fallback", func(c *Client) http.Handler {
			return c.Middleware(http.StripPrefix("/files", http.HandlerFunc(htmlPage)))
		}, "/files/a//b/../c?x=1&token=zz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)
			h := tt.build(r.c)
			for range 3 {
				serve(h, request(http.MethodGet, tt.target, nil))
			}
			if posts := r.drain(t); len(posts) != 0 {
				t.Fatalf("%d views counted without a pattern: %s", len(posts), posts[0].Body)
			}
			lines := r.logs.lines(t)
			if len(lines) != 1 || lines[0]["msg"] != "no route pattern, view not counted" {
				t.Fatalf("log lines = %v, want one warning", lines)
			}
			for _, leak := range []string{"secret", "abc123", "4711", "token", "reset"} {
				if strings.Contains(r.logs.String(), leak) {
					t.Errorf("log holds %q", leak)
				}
			}
		})
	}
}

func TestFallbackRegisteredThroughTheMuxIsCounted(t *testing.T) {
	r := newRig(t, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", htmlPage)
	serve(r.c.Middleware(mux), request(http.MethodGet, "/reset/abc123token?x=1", nil))
	posts := r.drain(t)
	if len(posts) != 1 || view(t, posts[0])["url"] != "/" {
		t.Fatalf("posts = %d, want one view of /", len(posts))
	}
	if strings.Contains(string(posts[0].Body), "abc123") {
		t.Error("the path reached Umami")
	}
}

func TestURLOptionNamesViewsWithoutPattern(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		want    string
		counted bool
	}{
		{"fixed page", "/de/impressum", "/de/impressum", true},
		{"query cut off", "/page?x=1&token=zz", "/page", true},
		{"fragment cut off", "/page#frag", "/page", true},
		{"empty", "", "", false},
		{"relative", "page", "", false},
		{"only a query", "?x=1", "", false},
		{"absolute url", "https://evil.example/x", "", false},
		{"scheme relative url", "//evil.example/x?q#f", "", false},
		{"backslash host", `/\evil.example/x`, "", false},
		{"single slash only", "/", "/", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, func(_ *Config, o *Options, _ *fakeUmami) {
				o.URL = func(*http.Request) string { return tt.answer }
			})
			serve(r.c.Middleware(http.HandlerFunc(htmlPage)), request(http.MethodGet, "/x/4711", nil))
			posts := r.drain(t)
			if (len(posts) == 1) != tt.counted {
				t.Fatalf("counted = %v, want %v", len(posts) == 1, tt.counted)
			}
			if tt.counted {
				if got := view(t, posts[0])["url"]; got != tt.want {
					t.Errorf("url = %v, want %q", got, tt.want)
				}
				if strings.Contains(string(posts[0].Body), "token") {
					t.Error("a query reached Umami")
				}
			}
		})
	}
}

func TestURLOptionIsNotAskedWhenThereIsAPattern(t *testing.T) {
	var asked atomic.Int64
	r := newRig(t, func(_ *Config, o *Options, _ *fakeUmami) {
		o.URL = func(*http.Request) string { asked.Add(1); return "/wrong" }
	})
	serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/notes/4711", nil))
	posts := r.drain(t)
	if asked.Load() != 0 || len(posts) != 1 || view(t, posts[0])["url"] != "/notes/{id}" {
		t.Errorf("asked %d times, posts %d", asked.Load(), len(posts))
	}
}

func TestPatternPath(t *testing.T) {
	tests := map[string]string{
		"GET /notes/{id}":        "/notes/{id}",
		"/{$}":                   "/",
		"GET /{$}":               "/",
		"GET example.test/a":     "/a",
		"example.test/":          "/",
		"example.test":           "/",
		"/files/{path...}":       "/files/{path...}",
		"/a/{$}":                 "/a/",
		"POST /":                 "/",
		"GET /u/{user}/p/{post}": "/u/{user}/p/{post}",
	}
	for in, want := range tests {
		if got := patternPath(in); got != want {
			t.Errorf("patternPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnlyAnsweredHTMLPagesAreCounted(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		target  string
		counted bool
	}{
		{"page", http.MethodGet, "/about", true},
		{"page with a sniffed content type", http.MethodGet, "/sniffed", true},
		{"page after early hints", http.MethodGet, "/early", true},
		{"404 from the handler", http.MethodGet, "/missing", false},
		{"404 from the mux", http.MethodGet, "/nowhere", false},
		{"500", http.MethodGet, "/boom", false},
		{"redirect", http.MethodGet, "/old", false},
		{"304", http.MethodGet, "/cached", false},
		{"json", http.MethodGet, "/api/notes", false},
		{"asset", http.MethodGet, "/assets/app.css", false},
		{"200 with no body", http.MethodGet, "/empty", false},
		{"POST", http.MethodPost, "/notes", false},
		{"PUT", http.MethodPut, "/about", false},
		{"HEAD", http.MethodHead, "/about", false},
		{"OPTIONS", http.MethodOptions, "/about", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)
			serve(r.c.Middleware(siteMux()), request(tt.method, tt.target, nil))
			if got := len(r.drain(t)) == 1; got != tt.counted {
				t.Errorf("counted = %v, want %v", got, tt.counted)
			}
		})
	}
}

func TestPanickingHandlerIsNotCountedAndPanicContinues(t *testing.T) {
	r := newRig(t, nil)
	h := r.c.Middleware(siteMux())
	func() {
		defer func() {
			got, _ := recover().(error)
			if !errors.Is(got, http.ErrAbortHandler) {
				t.Errorf("recovered %v, want the handler's own panic", got)
			}
		}()
		serve(h, request(http.MethodGet, "/panic", nil))
	}()
	if got := len(r.drain(t)); got != 0 {
		t.Errorf("%d views counted for a panic", got)
	}
}

func TestHealthAndMetricsRoutesAreNotCounted(t *testing.T) {
	for _, target := range []string{"/healthz", "/readyz", "/livez", "/metrics", "/metrics?x=1"} {
		t.Run(target, func(t *testing.T) {
			r := newRig(t, nil)
			serve(r.c.Middleware(siteMux()), request(http.MethodGet, target, nil))
			if got := len(r.drain(t)); got != 0 {
				t.Errorf("%d views counted", got)
			}
		})
	}
}

func TestHealthExclusionHoldsWithASkipOption(t *testing.T) {
	r := newRig(t, func(_ *Config, opts *Options, _ *fakeUmami) {
		opts.Skip = func(*http.Request) bool { return false }
	})
	serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/healthz", nil))
	if got := len(r.drain(t)); got != 0 {
		t.Errorf("%d views counted", got)
	}
}

func TestSkipOptionAddsExclusions(t *testing.T) {
	r := newRig(t, func(_ *Config, opts *Options, _ *fakeUmami) {
		opts.Skip = func(req *http.Request) bool { return strings.HasPrefix(req.URL.Path, "/notes/") }
	})
	h := r.c.Middleware(siteMux())
	serve(h, request(http.MethodGet, "/notes/1", nil))
	serve(h, request(http.MethodGet, "/about", nil))
	posts := r.drain(t)
	if len(posts) != 1 || view(t, posts[0])["url"] != "/about" {
		t.Errorf("posts = %d, want only /about", len(posts))
	}
}

func TestVisitorFilters(t *testing.T) {
	tests := []struct {
		name    string
		mod     func(*http.Request)
		tune    func(*Config, *Options, *fakeUmami)
		counted bool
	}{
		{"browser", nil, nil, true},
		{"googlebot", func(r *http.Request) {
			r.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
		}, nil, false},
		{"crawler", func(r *http.Request) { r.Header.Set("User-Agent", "ExampleCrawler/1.0") }, nil, false},
		{"curl", func(r *http.Request) { r.Header.Set("User-Agent", "curl/8.5.0") }, nil, false},
		{"go client", func(r *http.Request) { r.Header.Set("User-Agent", "Go-http-client/2.0") }, nil, false},
		{"headless chrome", func(r *http.Request) { r.Header.Set("User-Agent", "Mozilla/5.0 HeadlessChrome/130.0") }, nil, false},
		{"link preview", func(r *http.Request) { r.Header.Set("User-Agent", "Slack-LinkPreview/1.0") }, nil, false},
		{"no user agent", func(r *http.Request) { r.Header.Del("User-Agent") }, nil, false},
		{"blank user agent", func(r *http.Request) { r.Header.Set("User-Agent", "   ") }, nil, false},
		{"prefetch", func(r *http.Request) { r.Header.Set("Sec-Purpose", "prefetch") }, nil, false},
		{"prefetch with prerender", func(r *http.Request) { r.Header.Set("Sec-Purpose", "prefetch;prerender") }, nil, false},
		{"old purpose header", func(r *http.Request) { r.Header.Set("Purpose", "prefetch") }, nil, false},
		{"navigation", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "document") }, nil, true},
		{"fetch call", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "empty") }, nil, false},
		{"iframe", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "iframe") }, nil, false},
		{"private peer", func(r *http.Request) { r.RemoteAddr = "10.1.2.3:4000" }, nil, false},
		{"loopback peer", func(r *http.Request) { r.RemoteAddr = "127.0.0.1:4000" }, nil, false},
		{"link local peer", func(r *http.Request) { r.RemoteAddr = "169.254.1.1:4000" }, nil, false},
		{"shared address space", func(r *http.Request) { r.RemoteAddr = "100.64.1.1:4000" }, nil, false},
		{"unique local ipv6", func(r *http.Request) { r.RemoteAddr = "[fd00::1]:4000" }, nil, false},
		{"public ipv6", func(r *http.Request) { r.RemoteAddr = "[2001:db8::1]:4000" }, nil, true},
		{"no peer", func(r *http.Request) { r.RemoteAddr = "" }, nil, false},
		{
			"private peer in development", func(r *http.Request) { r.RemoteAddr = "10.1.2.3:4000" },
			func(_ *Config, o *Options, _ *fakeUmami) { o.AllowPrivateClients = true }, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, tt.tune)
			serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/about", tt.mod))
			if got := len(r.drain(t)) == 1; got != tt.counted {
				t.Errorf("counted = %v, want %v", got, tt.counted)
			}
		})
	}
}

func TestClientAddressComesFromTheResolver(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name     string
		resolver *clientip.Resolver
		peer     string
		xff      string
		want     string
	}{
		{"trusted proxy forwards a visitor", clientip.NewResolver(proxies), "10.1.2.3:5000", "198.51.100.9", "198.51.100.9"},
		{"spoofed header from an untrusted peer", clientip.NewResolver(proxies), testPeer, "198.51.100.9", "203.0.113.7"},
		{"no resolver trusts no header", nil, testPeer, "198.51.100.9", "203.0.113.7"},
		{"proxy forwards a private address", clientip.NewResolver(proxies), "10.1.2.3:5000", "192.168.1.5", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, func(_ *Config, o *Options, _ *fakeUmami) { o.ClientIP, o.FullIP = tt.resolver, true })
			serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/about", func(req *http.Request) {
				req.RemoteAddr = tt.peer
				req.Header.Set("X-Forwarded-For", tt.xff)
			}))
			posts := r.drain(t)
			if tt.want == "" {
				if len(posts) != 0 {
					t.Fatalf("counted a private client")
				}
				return
			}
			if len(posts) != 1 {
				t.Fatalf("got %d posts", len(posts))
			}
			if got := view(t, posts[0])["ip"]; got != tt.want {
				t.Errorf("ip = %v, want %s", got, tt.want)
			}
			if posts[0].Header.Get("X-Forwarded-For") != "" {
				t.Error("the forwarded header was copied to Umami")
			}
		})
	}
}

func TestMiddlewareChangesNothingTheVisitorSees(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("X-Custom", "a")
		w.Header().Add("X-Custom", "b")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>body</html>"))
	})
	r := newRig(t, nil)
	wrapped := serve(r.c.Middleware(handler), request(http.MethodGet, "/", nil))
	plain := serve(handler, request(http.MethodGet, "/", nil))
	r.drain(t)

	if wrapped.Code != plain.Code {
		t.Errorf("status %d, want %d", wrapped.Code, plain.Code)
	}
	if wrapped.Body.String() != plain.Body.String() {
		t.Errorf("body %q, want %q", wrapped.Body, plain.Body)
	}
	if !reflect.DeepEqual(wrapped.Header(), plain.Header()) {
		t.Errorf("headers %v, want %v", wrapped.Header(), plain.Header())
	}
	for _, name := range []string{"Set-Cookie", "Set-Cookie2"} {
		if got := wrapped.Header().Values(name); len(got) != 0 {
			t.Errorf("%s = %v, the middleware must set no cookie", name, got)
		}
	}
}

func TestMiddlewarePassesAnErrorStatusThrough(t *testing.T) {
	r := newRig(t, nil)
	rec := serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestNoCookieJarAndNoCookieToUmami(t *testing.T) {
	r := newRig(t, nil)
	serve(r.c.Middleware(siteMux()), request(http.MethodGet, "/about", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "session", Value: "cookie-secret"})
	}))
	posts := r.drain(t)
	if len(posts) != 1 {
		t.Fatalf("got %d posts", len(posts))
	}
	if posts[0].Header.Get("Cookie") != "" || strings.Contains(string(posts[0].Body), "cookie-secret") {
		t.Error("a visitor cookie reached Umami")
	}

	c, err := New(Config{URL: testURL, WebsiteID: testID}, Options{Hostname: testHost, Logger: r.c.logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(t.Context()) })
	if c.http.Jar != nil {
		t.Error("the default HTTP client keeps cookies")
	}
}

func TestDefaultClientRefusesRedirects(t *testing.T) {
	c, err := New(Config{URL: testURL, WebsiteID: testID}, Options{Hostname: testHost, Logger: newRig(t, nil).c.logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(t.Context()) })
	if got := c.http.CheckRedirect(nil, nil); !errors.Is(got, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect = %v, want ErrUseLastResponse", got)
	}
}

// hijackRecorder is a writer that can be hijacked.
type hijackRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

func TestWrapperPassesFlushAndHijackThrough(t *testing.T) {
	t.Run("http.Flusher assertion", func(t *testing.T) {
		r := newRig(t, nil)
		rec := httptest.NewRecorder()
		h := r.c.Middleware(routed(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			f, ok := w.(http.Flusher)
			if !ok {
				t.Error("the wrapper is no http.Flusher")
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
			f.Flush()
			_, _ = w.Write([]byte("</html>"))
		})))
		h.ServeHTTP(rec, request(http.MethodGet, "/", nil))
		if !rec.Flushed {
			t.Error("Flush did not reach the real writer")
		}
		if got := len(r.drain(t)); got != 1 {
			t.Errorf("flushed page counted %d times", got)
		}
	})
	t.Run("flush before any write", func(t *testing.T) {
		r := newRig(t, nil)
		rec := httptest.NewRecorder()
		h := r.c.Middleware(routed(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		})))
		h.ServeHTTP(rec, request(http.MethodGet, "/", nil))
		if !rec.Flushed {
			t.Error("Flush did not reach the real writer")
		}
		if got := len(r.drain(t)); got != 1 {
			t.Errorf("counted %d times", got)
		}
	})
	t.Run("flush before the body with no content type", func(t *testing.T) {
		// net/http sends no sniffed type for a flush before any body, so
		// the response is no HTML page for the visitor's browser to read as
		// one, and it is not counted.
		r := newRig(t, nil)
		h := r.c.Middleware(routed(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = http.NewResponseController(w).Flush()
			_, _ = w.Write([]byte("<!doctype html><html></html>"))
		})))
		serve(h, request(http.MethodGet, "/", nil))
		if got := len(r.drain(t)); got != 0 {
			t.Errorf("counted %d times", got)
		}
	})
	t.Run("empty write first then html", func(t *testing.T) {
		r := newRig(t, nil)
		h := r.c.Middleware(routed(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte{})
			_, _ = w.Write([]byte("<!doctype html><html></html>"))
		})))
		// A real server sniffs the type from the first byte it sends, and an
		// empty write sends none, so the page is HTML.
		serve(h, request(http.MethodGet, "/", nil))
		if got := len(r.drain(t)); got != 1 {
			t.Errorf("counted %d times, want 1", got)
		}
	})
	t.Run("ResponseController flush", func(t *testing.T) {
		r := newRig(t, nil)
		rec := httptest.NewRecorder()
		h := r.c.Middleware(routed(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush: %v", err)
			}
		})))
		h.ServeHTTP(rec, request(http.MethodGet, "/", nil))
		if !rec.Flushed {
			t.Error("Flush did not reach the real writer")
		}
	})
	t.Run("ResponseController hijack", func(t *testing.T) {
		r := newRig(t, nil)
		rec := &hijackRecorder{ResponseRecorder: httptest.NewRecorder()}
		h := r.c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
				t.Errorf("Hijack: %v", err)
			}
		}))
		h.ServeHTTP(rec, request(http.MethodGet, "/", nil))
		if !rec.hijacked {
			t.Error("Hijack did not reach the real writer")
		}
	})
}

func TestDisabledClientIsInert(t *testing.T) {
	c, err := New(Config{}, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Enabled() {
		t.Error("an empty config is enabled")
	}
	next := http.HandlerFunc(htmlPage)
	got := c.Middleware(next)
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(http.Handler(next)).Pointer() {
		t.Error("a disabled client wraps the handler")
	}
	c.Track(View{URL: "/"})
	if err := c.Close(t.Context()); err != nil {
		t.Errorf("Close: %v", err)
	}
	if c.Stats() != (Stats{}) {
		t.Errorf("stats = %+v", c.Stats())
	}
}
