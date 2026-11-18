// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/appassoc"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/middleware"
)

const testFingerprint = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"

func assocEnv() map[string]string {
	return map[string]string{
		"APPLE_APP_ID":         "ABCDE12345.com.example.app",
		"APPLE_APP_PATHS":      "/app/*",
		"APPLE_WEBCREDENTIALS": "true",
		"ANDROID_PACKAGE":      "com.example.app",
		"ANDROID_CERT_SHA256":  strings.ToLower(testFingerprint),
		"ANDROID_LOGIN_CREDS":  "true",
	}
}

func assocPaths() []string {
	return []string{appassoc.ApplePath, appassoc.AndroidPath}
}

func newReq(t *testing.T, method, target string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), method, target, nil)
}

func serveExample(t *testing.T, a *app, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, req)
	return rec
}

// TestAssociationFilesWinOverTheCatchAll pins that both files are answered
// by their own handler and not by the catch-all "/" behind the whole chain,
// that the chain neither redirects nor sets a cookie on them, and that the
// answer does not depend on a session or a credential header.
func TestAssociationFilesWinOverTheCatchAll(t *testing.T) {
	a, _ := newTestAppEnv(t, 1000, assocEnv())
	cookie := startSession(t, a)

	variants := map[string]func(*http.Request){
		"plain":          func(*http.Request) {},
		"session cookie": func(r *http.Request) { r.AddCookie(cookie) },
		"bad bearer":     func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-token") },
		"two headers": func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer a")
			r.Header.Add("Authorization", "Bearer b")
		},
		"foreign origin": func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.test") },
	}
	for _, path := range assocPaths() {
		for name, mod := range variants {
			t.Run(path+" "+name, func(t *testing.T) {
				req := newReq(t, http.MethodGet, path)
				mod(req)
				rec := serveExample(t, a, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
				}
				if got := rec.Header().Get("Content-Type"); got != "application/json" {
					t.Errorf("content type %q", got)
				}
				if got := rec.Header().Get("Cache-Control"); got != appassoc.CacheControl {
					t.Errorf("cache control %q", got)
				}
				if v := rec.Header().Values("Set-Cookie"); len(v) != 0 {
					t.Errorf("Set-Cookie %v", v)
				}
				if v := rec.Header().Values("Location"); len(v) != 0 {
					t.Errorf("Location %v", v)
				}
				// Proof that the outer chain ran: the request id and the
				// security headers are there.
				if rec.Header().Get(middleware.RequestIDHeader) == "" || rec.Header().Get("Content-Security-Policy") == "" {
					t.Errorf("the outer chain did not run: %v", rec.Header())
				}
				if strings.Contains(rec.Body.String(), "not found") {
					t.Errorf("the catch-all answered: %q", rec.Body.String())
				}
			})
		}
	}
}

func TestAssociationFilesAreNeverRedirectedOrSetACookie(t *testing.T) {
	a, _ := newTestAppEnv(t, 1000, assocEnv())
	for _, path := range assocPaths() {
		for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
			for _, target := range []string{path, path + "/"} {
				rec := serveExample(t, a, newReq(t, m, target))
				if v := rec.Header().Values("Location"); len(v) != 0 {
					t.Errorf("%s %s: redirects to %v (status %d)", m, target, v, rec.Code)
				}
				if v := rec.Header().Values("Set-Cookie"); len(v) != 0 {
					t.Errorf("%s %s: Set-Cookie %v", m, target, v)
				}
			}
		}
	}
}

// TestAppleFileWithPathsAndWebCredentialsHoldsBoth pins that a path keeps
// the applinks entry when webcredentials is on as well.
func TestAppleFileWithPathsAndWebCredentialsHoldsBoth(t *testing.T) {
	a, _ := newTestAppEnv(t, 1000, assocEnv())
	rec := serveExample(t, a, newReq(t, http.MethodGet, appassoc.ApplePath))
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"applinks", "webcredentials"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("%s missing: %s", key, rec.Body.String())
		}
	}
}

func TestAndroidLoginCredsFollowsTheSwitch(t *testing.T) {
	for _, on := range []bool{true, false} {
		env := assocEnv()
		if !on {
			delete(env, "ANDROID_LOGIN_CREDS")
		}
		a, _ := newTestAppEnv(t, 1000, env)
		body := serveExample(t, a, newReq(t, http.MethodGet, appassoc.AndroidPath)).Body.String()
		if !strings.Contains(body, "common.handle_all_urls") {
			t.Errorf("login creds %v: handle_all_urls missing: %s", on, body)
		}
		if got := strings.Contains(body, "common.get_login_creds"); got != on {
			t.Errorf("login creds %v: get_login_creds present = %v: %s", on, got, body)
		}
	}
}

// TestWebCredentialsOnlyAppleFile covers the passkey only product: the app
// id with APPLE_WEBCREDENTIALS and no path gives a file without applinks.
func TestWebCredentialsOnlyAppleFile(t *testing.T) {
	env := assocEnv()
	delete(env, "APPLE_APP_PATHS")
	a, _ := newTestAppEnv(t, 1000, env)
	rec := serveExample(t, a, newReq(t, http.MethodGet, appassoc.ApplePath))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["applinks"]; ok {
		t.Errorf("applinks present: %s", rec.Body.String())
	}
	if _, ok := doc["webcredentials"]; !ok {
		t.Errorf("webcredentials missing: %s", rec.Body.String())
	}

	// Without the switch, no path stays an error.
	env = assocEnv()
	delete(env, "APPLE_APP_PATHS")
	delete(env, "APPLE_WEBCREDENTIALS")
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err == nil {
		_, err = newApp(cfg, logging.New(io.Discard, cfg.logLevel))
	}
	if err == nil {
		t.Error("an app id without a path and without webcredentials must fail")
	}
}

func TestAssociationFilesMethodsThroughTheChain(t *testing.T) {
	a, _ := newTestAppEnv(t, 1000, assocEnv())
	for _, path := range assocPaths() {
		head := serveExample(t, a, newReq(t, http.MethodHead, path))
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Errorf("HEAD %s: status %d, %d body bytes", path, head.Code, head.Body.Len())
		}
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
			rec := serveExample(t, a, newReq(t, m, path))
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: status %d Allow %q", m, path, rec.Code, rec.Header().Get("Allow"))
			}
		}
		if rec := serveExample(t, a, newReq(t, http.MethodGet, path+"/")); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s/: status %d, want 404 from the catch-all", path, rec.Code)
		}
	}
}

// TestAssociationFilesAreOffByDefault pins the opt in: without the
// variables nothing is mounted and the catch-all answers.
func TestAssociationFilesAreOffByDefault(t *testing.T) {
	a, _ := newTestApp(t, 1000)
	if a.assoc != nil {
		t.Fatal("handlers exist without configuration")
	}
	for _, path := range assocPaths() {
		if rec := serveExample(t, a, newReq(t, http.MethodGet, path)); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestOneFileConfiguredMountsOnlyThatFile(t *testing.T) {
	env := assocEnv()
	delete(env, "ANDROID_PACKAGE")
	delete(env, "ANDROID_CERT_SHA256")
	a, _ := newTestAppEnv(t, 1000, env)
	if rec := serveExample(t, a, newReq(t, http.MethodGet, appassoc.ApplePath)); rec.Code != http.StatusOK {
		t.Errorf("apple: status %d", rec.Code)
	}
	rec := serveExample(t, a, newReq(t, http.MethodGet, appassoc.AndroidPath))
	if rec.Code != http.StatusNotFound {
		t.Errorf("android: status %d, want 404, not an empty document", rec.Code)
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "json") && strings.TrimSpace(rec.Body.String()) == "[]" {
		t.Error("an empty document was served")
	}
}

func TestAssociationConfigurationErrors(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"apple id without a dot":  {"APPLE_APP_ID": "nodot"},
		"apple id without paths":  {"APPLE_APP_ID": "ABCDE12345.com.example.app"},
		"exclusion without path":  {"APPLE_APP_ID": "ABCDE12345.com.example.app", "APPLE_APP_EXCLUDE_PATHS": "/a", "APPLE_WEBCREDENTIALS": "true"},
		"android without a print": {"ANDROID_PACKAGE": "com.example.app"},
		"android bad print":       {"ANDROID_PACKAGE": "com.example.app", "ANDROID_CERT_SHA256": "AA:BB"},
		"bad bool":                {"APPLE_APP_ID": "ABCDE12345.com.example.app", "APPLE_APP_PATHS": "/a", "APPLE_WEBCREDENTIALS": "maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t, 10)
			for k, v := range extra {
				env[k] = v
			}
			cfg, err := loadConfig(func(key string) string { return env[key] })
			if err == nil {
				if _, err = newApp(cfg, nil); err == nil {
					t.Fatal("want an error")
				}
			}
		})
	}
}
