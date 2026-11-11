package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewCORSRefusesWhatCannotBeSafe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origins []string
	}{
		{"none", nil},
		{"wildcard", []string{"*"}},
		{"wildcard among real ones", []string{"https://app.example.test", "*"}},
		{"no scheme", []string{"app.example.test"}},
		{"with a path", []string{"https://app.example.test/app"}},
		{"with userinfo", []string{"https://user:pw@app.example.test"}},
		{"not a URL", []string{"https://%zz"}},
		{"the literal null", []string{"null"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCORS(CORSConfig{Origins: tc.origins}); err == nil {
				t.Errorf("NewCORS(%v) was accepted", tc.origins)
			}
		})
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	cors, err := NewCORS(CORSConfig{
		Origins:     []string{"https://app.example.test/"},
		Credentials: true,
		MaxAge:      10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("served"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://app.example.test")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.test" {
		t.Errorf("allow origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow credentials = %q", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want Origin", got)
	}
	if rec.Body.String() != "served" {
		t.Errorf("body = %q, the request did not reach the handler", rec.Body.String())
	}
}

func TestCORSUnknownOriginGetsNoHeaders(t *testing.T) {
	cors, err := NewCORS(CORSConfig{Origins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("served"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow origin = %q, want nothing", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want Origin even without a match", got)
	}
	// The browser refuses the answer; the server still handled it, which is
	// what makes a simple GET cross origin safe to serve.
	if rec.Body.String() != "served" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestCORSOriginNullNeverMatches(t *testing.T) {
	cors, err := NewCORS(CORSConfig{Origins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "null")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow origin = %q, want nothing for Origin: null", got)
	}
}

func TestCORSOriginMatchesCaseInsensitively(t *testing.T) {
	cors, err := NewCORS(CORSConfig{Origins: []string{"https://App.Example.Test"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://app.example.test")
	handler.ServeHTTP(rec, req)

	// The request's own value is echoed back, byte for byte, even though
	// the configured allow-list entry differed in letter case.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.test" {
		t.Errorf("allow origin = %q, want the request's own origin echoed back", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	cors, err := NewCORS(CORSConfig{
		Origins: []string{"https://app.example.test"},
		MaxAge:  time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	reached := false
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("allow methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
		t.Errorf("allow headers = %q, want Content-Type", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Errorf("max age = %q", got)
	}
	if reached {
		t.Error("the preflight reached the handler")
	}
}

func TestCORSPreflightFromAnUnknownOrigin(t *testing.T) {
	cors, err := NewCORS(CORSConfig{Origins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the preflight reached the handler")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestCORSPlainOptionsIsNotAPreflight(t *testing.T) {
	cors, err := NewCORS(CORSConfig{Origins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	reached := false
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/things", nil))

	if !reached {
		t.Error("an OPTIONS without the preflight header must reach the router")
	}
}

func TestCORSHeadersDefaultToContentTypeOnly(t *testing.T) {
	cors, err := NewCORS(CORSConfig{Origins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Errorf("allow headers = %q, want exactly Content-Type with no csrf package pulled in", got)
	}
}

func TestCORSNeverSendsAWildcardWithCredentials(t *testing.T) {
	cors, err := NewCORS(CORSConfig{
		Origins:     []string{"https://app.example.test"},
		Credentials: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := cors.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("a wildcard origin was sent alongside credentials")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("credentials = %q, want unset without a matched origin", got)
	}
}
