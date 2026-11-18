// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAppOriginsAccepts(t *testing.T) {
	t.Parallel()

	got, err := NewAppOrigins([]string{
		"capacitor://localhost", "https://localhost", "https://app.example.test:8443", "ionic://localhost",
	}, "capacitor", "ionic")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.List()); n != 4 {
		t.Errorf("List has %d entries, want 4", n)
	}
	// The list is a copy.
	got.List()[0] = "https://changed.example.test"
	if !got.Contains("capacitor://localhost") {
		t.Error("changing the returned list changed the origins")
	}
}

func TestNewAppOriginsRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		origins []string
		schemes []string
		named   string // the offending value the error must name
	}{
		{"none", nil, nil, ""},
		{"a wildcard", []string{"*"}, nil, "*"},
		{"a wildcard among real ones", []string{"https://app.example.test", "*"}, nil, "*"},
		{"a wildcard subdomain", []string{"https://*.example.test"}, nil, "https://*.example.test"},
		{"a wildcard scheme", []string{"*://localhost"}, []string{"capacitor"}, "*://localhost"},
		{"null", []string{"null"}, nil, "null"},
		{"a path", []string{"https://app.example.test/app"}, nil, "https://app.example.test/app"},
		{"a trailing slash", []string{"https://app.example.test/"}, nil, "https://app.example.test/"},
		{"a query", []string{"https://app.example.test?x=1"}, nil, "https://app.example.test?x=1"},
		{"an empty query", []string{"https://app.example.test?"}, nil, "https://app.example.test?"},
		{"a fragment", []string{"https://app.example.test#x"}, nil, "https://app.example.test#x"},
		{"userinfo", []string{"https://user:pw@app.example.test"}, nil, "https://user:pw@app.example.test"},
		{"an upper case host", []string{"https://App.Example.test"}, nil, "https://App.Example.test"},
		{"an upper case scheme", []string{"HTTPS://app.example.test"}, nil, "HTTPS://app.example.test"},
		{"an upper case custom scheme", []string{"Capacitor://localhost"}, []string{"capacitor"}, "Capacitor://localhost"},
		{"surrounding space", []string{" https://app.example.test"}, nil, " https://app.example.test"},
		{"no scheme", []string{"app.example.test"}, nil, "app.example.test"},
		{"no host", []string{"https://"}, nil, "https://"},
		{"a port without a host", []string{"https://:8443"}, nil, "https://:8443"},
		{"not a URL", []string{"https://%zz"}, nil, "https://%zz"},
		{"http", []string{"http://localhost"}, nil, "http://localhost"},
		{"http even when listed as custom", []string{"http://localhost"}, []string{"http"}, "http"},
		{"a custom scheme that is not allowed", []string{"capacitor://localhost"}, nil, "capacitor://localhost"},
		{"another custom scheme than the allowed one", []string{"ionic://localhost"}, []string{"capacitor"}, "ionic://localhost"},
		{"a reserved custom scheme", []string{"file://localhost"}, []string{"file"}, "file"},
		{"javascript as custom scheme", []string{"javascript://localhost"}, []string{"javascript"}, "javascript"},
		{"an upper case allowed scheme", []string{"capacitor://localhost"}, []string{"Capacitor"}, "Capacitor"},
		{"an empty allowed scheme", []string{"https://app.example.test"}, []string{""}, `""`},
		{"an allowed scheme with a colon", []string{"https://app.example.test"}, []string{"capacitor:"}, "capacitor:"},
		{"a duplicate", []string{"https://app.example.test", "https://app.example.test"}, nil, "https://app.example.test"},
		{"a duplicate custom origin", []string{"capacitor://localhost", "capacitor://localhost"}, []string{"capacitor"}, "capacitor://localhost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewAppOrigins(tc.origins, tc.schemes...)
			if err == nil {
				t.Fatalf("NewAppOrigins(%q, %q) was accepted", tc.origins, tc.schemes)
			}
			if tc.named != "" && !strings.Contains(err.Error(), tc.named) {
				t.Errorf("error %q does not name %q", err, tc.named)
			}
			// The two names of a refusal that another rule would also catch by
			// accident, so a removed rule cannot hide behind the other.
			if strings.Contains(tc.named, "*") && !strings.Contains(err.Error(), "wildcard") {
				t.Errorf("error %q does not say wildcard", err)
			}
			if tc.named == "null" && !strings.Contains(err.Error(), "null origin") {
				t.Errorf("error %q does not say null origin", err)
			}
			if tc.named != "" && !errors.Is(err, errInvalidOrigin) {
				t.Errorf("error %v is not errInvalidOrigin", err)
			}
		})
	}
}

func mustAppOrigins(t *testing.T, origins ...string) AppOrigins {
	t.Helper()
	got, err := NewAppOrigins(origins, "capacitor")
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// An overlap is a startup error that names the origin, whichever list holds it.
func TestAppOriginsRejectOverlap(t *testing.T) {
	t.Parallel()

	app := mustAppOrigins(t, "capacitor://localhost", "https://localhost")

	for _, tc := range []struct {
		name    string
		web     []string
		trusted []string
		named   string
		list    string
	}{
		{"on the web list", []string{"https://localhost"}, nil, "https://localhost", "web CORS list"},
		{"on the trusted list", nil, []string{"capacitor://localhost"}, "capacitor://localhost", "CSRF trusted origins"},
		{"on the web list with a slash and case", []string{"HTTPS://Localhost/"}, nil, "https://localhost", "web CORS list"},
		{"on the trusted list with space", nil, []string{" capacitor://localhost "}, "capacitor://localhost", "CSRF trusted origins"},
		{"on both", []string{"https://localhost"}, []string{"capacitor://localhost"}, "https://localhost", "web CORS list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := app.RejectOverlap(tc.web, tc.trusted)
			if err == nil {
				t.Fatal("the overlap was not detected")
			}
			if !strings.Contains(err.Error(), tc.named) || !strings.Contains(err.Error(), tc.list) {
				t.Errorf("error %q does not name %q and %q", err, tc.named, tc.list)
			}
		})
	}

	if err := app.RejectOverlap([]string{"https://app.example.test"}, []string{"https://other.example.test"}); err != nil {
		t.Errorf("disjoint lists: %v", err)
	}
	if err := app.RejectOverlap(nil, nil); err != nil {
		t.Errorf("empty lists: %v", err)
	}
}

func TestNewAppCORSRefusesTheZeroList(t *testing.T) {
	t.Parallel()

	if _, err := NewAppCORS(AppOrigins{}); err == nil {
		t.Error("the zero AppOrigins was accepted")
	}
}

func appCORSHandler(t *testing.T, reached *bool) http.Handler {
	t.Helper()
	c, err := NewAppCORS(mustAppOrigins(t, "capacitor://localhost", "https://localhost"))
	if err != nil {
		t.Fatal(err)
	}

	return c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		_, _ = w.Write([]byte("served"))
	}))
}

func appPreflight(t *testing.T, origin string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/auth/token", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	r.Header.Set("Access-Control-Request-Headers", "authorization, content-type")

	return r
}

func TestAppCORSPreflight(t *testing.T) {
	t.Parallel()

	for _, origin := range []string{"capacitor://localhost", "https://localhost"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			var reached bool
			rec := httptest.NewRecorder()
			appCORSHandler(t, &reached).ServeHTTP(rec, appPreflight(t, origin))

			if reached {
				t.Error("the preflight reached the next handler")
			}
			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", rec.Code)
			}
			h := rec.Header()
			for key, want := range map[string]string{
				"Access-Control-Allow-Origin":  origin,
				"Access-Control-Allow-Methods": "GET, POST, DELETE",
				"Access-Control-Allow-Headers": "Authorization, Content-Type",
				"Access-Control-Max-Age":       "600",
			} {
				if got := h.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			if _, ok := h["Access-Control-Allow-Credentials"]; ok {
				t.Errorf("credentials header sent: %q", h.Values("Access-Control-Allow-Credentials"))
			}
			vary := strings.Join(h.Values("Vary"), ",")
			for _, want := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
				if !strings.Contains(vary, want) {
					t.Errorf("Vary = %q, missing %q", vary, want)
				}
			}
			if strings.Contains(h.Get("Access-Control-Allow-Headers"), "*") ||
				strings.Contains(h.Get("Access-Control-Allow-Methods"), "*") {
				t.Error("a wildcard in the allowed headers or methods")
			}
			if strings.Contains(h.Get("Access-Control-Allow-Methods"), "PUT") ||
				strings.Contains(h.Get("Access-Control-Allow-Methods"), "PATCH") {
				t.Errorf("methods too wide: %q", h.Get("Access-Control-Allow-Methods"))
			}
		})
	}
}

func TestAppCORSActualRequest(t *testing.T) {
	t.Parallel()

	var reached bool
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/token", nil)
	r.Header.Set("Origin", "capacitor://localhost")
	appCORSHandler(t, &reached).ServeHTTP(rec, r)

	if !reached {
		t.Fatal("the request did not reach the next handler")
	}
	h := rec.Header()
	if got := h.Get("Access-Control-Allow-Origin"); got != "capacitor://localhost" {
		t.Errorf("allow origin = %q", got)
	}
	if _, ok := h["Access-Control-Allow-Credentials"]; ok {
		t.Error("credentials header sent")
	}
	if !strings.Contains(strings.Join(h.Values("Vary"), ","), "Origin") {
		t.Errorf("Vary = %q", h.Values("Vary"))
	}
	// Only the preflight carries the allowed methods and headers.
	if h.Get("Access-Control-Allow-Methods") != "" || h.Get("Access-Control-Max-Age") != "" {
		t.Errorf("preflight headers on a real response: %v", h)
	}
}

// An origin that is not listed gets no Access-Control header, never an echo,
// and the request goes on to the next handler, which is the web policy.
func TestAppCORSUnlistedOrigin(t *testing.T) {
	t.Parallel()

	for _, origin := range []string{
		"https://evil.example.test",
		"null",
		"capacitor://LOCALHOST",
		"CAPACITOR://localhost",
		"https://localhost:8443",
		"http://localhost",
		"capacitor://localhost/",
		"capacitor://localhost.evil.example.test",
		"ionic://localhost",
		"*",
		"",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			for _, req := range []*http.Request{
				appPreflight(t, origin),
				httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil),
			} {
				req.Header.Set("Origin", origin)
				var reached bool
				rec := httptest.NewRecorder()
				appCORSHandler(t, &reached).ServeHTTP(rec, req)

				if !reached {
					t.Errorf("%s from %q did not pass to the next handler", req.Method, origin)
				}
				for key := range rec.Header() {
					if strings.HasPrefix(key, "Access-Control-") {
						t.Errorf("%s from %q got %s", req.Method, origin, key)
					}
				}
				if got := rec.Header().Get("Vary"); got != "Origin" {
					t.Errorf("Vary = %q, want Origin", got)
				}
			}
		})
	}
}

// A request without an Origin header passes through, a preflight lookalike
// from an app origin without the request method header is not a preflight.
func TestAppCORSOptionsWithoutRequestMethodIsNotAPreflight(t *testing.T) {
	t.Parallel()

	var reached bool
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/x", nil)
	r.Header.Set("Origin", "https://localhost")
	appCORSHandler(t, &reached).ServeHTTP(rec, r)

	if !reached {
		t.Error("an OPTIONS request without Access-Control-Request-Method was answered as a preflight")
	}
}
