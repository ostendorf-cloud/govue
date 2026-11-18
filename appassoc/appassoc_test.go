// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package appassoc

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	fpLower = "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"
	fpUpper = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	fpOther = "01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF"
)

func validConfig() Config {
	id := AppleID{TeamID: "ABCDE12345", BundleID: "com.example.app"}
	return Config{
		Apple: AppleConfig{
			AppLinks: []AppleApp{{
				AppleID:      id,
				Paths:        []string{"/app/*", "/invite/?*"},
				ExcludePaths: []string{"/app/private/*"},
			}},
			WebCredentials: []AppleID{id},
		},
		Android: AndroidConfig{Apps: []AndroidApp{{
			PackageName:  "com.example.app",
			Fingerprints: []string{fpLower, fpOther},
			Relations:    []Relation{RelationGetLoginCreds, RelationHandleAllURLs},
		}}},
	}
}

func newHandlers(t *testing.T, cfg Config) *Handlers {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

// serve builds a mux like the example does: the files plus a catch-all.
func serve(t *testing.T, cfg Config, catchAll bool) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	newHandlers(t, cfg).Handle(mux, nil)
	if catchAll {
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "catch-all", http.StatusNotFound)
		})
	}
	return mux
}

func newRequest(method, target string) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), method, target, nil)
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(method, target))
	return rec
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s differs\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

func TestGoldenFiles(t *testing.T) {
	h := serve(t, validConfig(), true)
	for _, tc := range []struct{ path, golden string }{
		{ApplePath, "apple.golden.json"},
		{AndroidPath, "android.golden.json"},
	} {
		rec := do(h, http.MethodGet, tc.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.path, rec.Code)
		}
		golden(t, tc.golden, rec.Body.Bytes())
		if !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("%s: body is not valid JSON", tc.path)
		}
	}
}

func TestDocumentShapes(t *testing.T) {
	h := serve(t, validConfig(), true)

	var apple struct {
		AppLinks struct {
			Details []struct {
				AppIDs     []string `json:"appIDs"`
				Components []struct {
					Path    string `json:"/"`
					Exclude bool   `json:"exclude"`
				} `json:"components"`
			} `json:"details"`
		} `json:"applinks"`
		WebCredentials struct {
			Apps []string `json:"apps"`
		} `json:"webcredentials"`
	}
	if err := json.Unmarshal(do(h, http.MethodGet, ApplePath).Body.Bytes(), &apple); err != nil {
		t.Fatal(err)
	}
	d := apple.AppLinks.Details
	if len(d) != 1 || d[0].AppIDs[0] != "ABCDE12345.com.example.app" {
		t.Fatalf("applinks details: %+v", d)
	}
	if len(d[0].Components) != 3 || !d[0].Components[0].Exclude || d[0].Components[1].Exclude {
		t.Fatalf("excluded path must come first: %+v", d[0].Components)
	}
	if got := apple.WebCredentials.Apps; len(got) != 1 || got[0] != "ABCDE12345.com.example.app" {
		t.Fatalf("webcredentials apps: %v", got)
	}

	var android []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			PackageName  string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.Unmarshal(do(h, http.MethodGet, AndroidPath).Body.Bytes(), &android); err != nil {
		t.Fatal(err)
	}
	if len(android) != 1 || android[0].Target.Namespace != "android_app" || android[0].Target.PackageName != "com.example.app" {
		t.Fatalf("android: %+v", android)
	}
	wantRel := []string{"delegate_permission/common.handle_all_urls", "delegate_permission/common.get_login_creds"}
	if strings.Join(android[0].Relation, ",") != strings.Join(wantRel, ",") {
		t.Fatalf("relation: %v", android[0].Relation)
	}
	if android[0].Target.Fingerprints[0] != fpUpper {
		t.Fatalf("fingerprint is not normalised to uppercase: %v", android[0].Target.Fingerprints)
	}
}

func TestDefaultRelation(t *testing.T) {
	cfg := Config{Android: AndroidConfig{Apps: []AndroidApp{{PackageName: "com.example.app", Fingerprints: []string{fpUpper}}}}}
	body := do(serve(t, cfg, true), http.MethodGet, AndroidPath).Body.String()
	if !strings.Contains(body, "common.handle_all_urls") || strings.Contains(body, "get_login_creds") {
		t.Fatalf("default must be handle_all_urls alone: %s", body)
	}
}

func TestDuplicateFingerprintsCollapse(t *testing.T) {
	cfg := Config{Android: AndroidConfig{Apps: []AndroidApp{{PackageName: "com.example.app", Fingerprints: []string{fpLower, fpUpper}}}}}
	body := do(serve(t, cfg, true), http.MethodGet, AndroidPath).Body.String()
	if n := strings.Count(body, fpUpper); n != 1 {
		t.Fatalf("fingerprint appears %d times: %s", n, body)
	}
}

func TestGetHeaders(t *testing.T) {
	h := serve(t, validConfig(), true)
	for _, path := range []string{ApplePath, AndroidPath} {
		rec := do(h, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		hdr := rec.Header()
		if got := hdr.Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: content type %q", path, got)
		}
		if got := hdr.Get("Cache-Control"); got != CacheControl {
			t.Errorf("%s: cache control %q", path, got)
		}
		if got := hdr.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q", path, got)
		}
		if got := hdr.Get("Content-Length"); got != strconv.Itoa(rec.Body.Len()) {
			t.Errorf("%s: content length %q, body %d", path, got, rec.Body.Len())
		}
		for _, bad := range []string{"Set-Cookie", "Location"} {
			if v := hdr.Values(bad); len(v) != 0 {
				t.Errorf("%s: %s = %v", path, bad, v)
			}
		}
	}
}

func TestHead(t *testing.T) {
	h := serve(t, validConfig(), true)
	for _, path := range []string{ApplePath, AndroidPath} {
		get := do(h, http.MethodGet, path)
		head := do(h, http.MethodHead, path)
		if head.Code != http.StatusOK {
			t.Fatalf("%s: HEAD status %d", path, head.Code)
		}
		if head.Body.Len() != 0 {
			t.Errorf("%s: HEAD wrote %d body bytes", path, head.Body.Len())
		}
		for _, k := range []string{"Content-Type", "Cache-Control", "Content-Length"} {
			if head.Header().Get(k) != get.Header().Get(k) {
				t.Errorf("%s: HEAD %s %q, GET %q", path, k, head.Header().Get(k), get.Header().Get(k))
			}
		}
	}
}

func TestOtherMethodsAre405(t *testing.T) {
	for _, catchAll := range []bool{true, false} {
		h := serve(t, validConfig(), catchAll)
		for _, path := range []string{ApplePath, AndroidPath} {
			for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
				rec := do(h, m, path)
				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("catchAll=%v %s %s: status %d", catchAll, m, path, rec.Code)
				}
				if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("catchAll=%v %s %s: Allow %q", catchAll, m, path, got)
				}
				if rec.Body.Len() != 0 {
					t.Errorf("%s %s: 405 carries a body", m, path)
				}
			}
		}
	}
}

func TestNoRedirect(t *testing.T) {
	for _, catchAll := range []bool{true, false} {
		h := serve(t, validConfig(), catchAll)
		for _, path := range []string{ApplePath + "/", AndroidPath + "/", "/.well-known/apple-app-site-association.json"} {
			rec := do(h, http.MethodGet, path)
			if rec.Code != http.StatusNotFound {
				t.Errorf("catchAll=%v %s: status %d, want 404", catchAll, path, rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("%s: redirects to %q", path, loc)
			}
		}
		// The exact paths themselves never redirect either.
		for _, path := range []string{ApplePath, AndroidPath} {
			if rec := do(h, http.MethodGet, path); rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
				t.Errorf("%s: status %d location %q", path, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
}

// TestExactPatternsOnly pins that the files do not capture neighbouring
// paths: the catch-all answers them.
func TestExactPatternsOnly(t *testing.T) {
	h := serve(t, validConfig(), true)
	for _, path := range []string{"/.well-known/", "/.well-known/other", "/.well-known/assetlinks.json/x", "/", "/x"} {
		rec := do(h, http.MethodGet, path)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "catch-all") {
			t.Errorf("%s: status %d body %q, want the catch-all", path, rec.Code, rec.Body.String())
		}
	}
}

func TestUnconfiguredFileIsNotMounted(t *testing.T) {
	appleOnly := validConfig()
	appleOnly.Android = AndroidConfig{}
	androidOnly := validConfig()
	androidOnly.Apple = AppleConfig{}

	for _, catchAll := range []bool{true, false} {
		h := serve(t, appleOnly, catchAll)
		if rec := do(h, http.MethodGet, AndroidPath); rec.Code != http.StatusNotFound {
			t.Errorf("apple only, android path: status %d, want 404", rec.Code)
		}
		if rec := do(h, http.MethodGet, ApplePath); rec.Code != http.StatusOK {
			t.Errorf("apple only, apple path: status %d", rec.Code)
		}
		h = serve(t, androidOnly, catchAll)
		if rec := do(h, http.MethodGet, ApplePath); rec.Code != http.StatusNotFound {
			t.Errorf("android only, apple path: status %d, want 404", rec.Code)
		}
		if rec := do(h, http.MethodGet, AndroidPath); rec.Code != http.StatusOK {
			t.Errorf("android only, android path: status %d", rec.Code)
		}
	}
}

func TestEmptyConfigIsRefused(t *testing.T) {
	h, err := New(Config{})
	if !errors.Is(err, ErrEmpty) || h != nil {
		t.Fatalf("New(Config{}) = %v, %v; want ErrEmpty", h, err)
	}
}

func TestWrapIsApplied(t *testing.T) {
	var wrapped int
	mux := http.NewServeMux()
	newHandlers(t, validConfig()).Handle(mux, func(next http.Handler) http.Handler {
		wrapped++
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "yes")
			next.ServeHTTP(w, r)
		})
	})
	if wrapped != 2 {
		t.Fatalf("wrap called %d times, want 2", wrapped)
	}
	if got := do(mux, http.MethodGet, ApplePath).Header().Get("X-Wrapped"); got != "yes" {
		t.Fatalf("wrapper did not run")
	}
}

func TestNoSessionNeeded(t *testing.T) {
	h := serve(t, validConfig(), true)
	for _, auth := range []string{"Bearer not-a-token", "Basic x", "garbage"} {
		req := newRequest(http.MethodGet, ApplePath)
		req.Header.Set("Authorization", auth)
		req.AddCookie(&http.Cookie{Name: "session", Value: "bogus"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Authorization %q: status %d", auth, rec.Code)
		}
	}
}

// TestEncoderEscapesHostileValues pins that the document is built with
// encoding/json: a value that validation would refuse still cannot break
// the document if one ever reached it.
func TestEncoderEscapesHostileValues(t *testing.T) {
	hostile := "\"},{\"x\":\"\n\\</script>" + strings.Repeat("a", 5000)
	body, err := marshal("test", aasaFile{
		AppLinks: &aasaAppLinks{Details: []aasaDetail{{
			AppIDs:     []string{hostile},
			Components: []aasaComponent{{Path: hostile}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var first aasaFile
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("hostile value broke the JSON: %v", err)
	}
	if first.AppLinks.Details[0].AppIDs[0] != hostile {
		t.Fatal("hostile value did not round trip")
	}

	short := "\"},{\"x\":\"\n\\</script> "
	body, err = marshal("test", []linkStatement{{Target: linkTarget{PackageName: short, Fingerprints: []string{short}}}})
	if err != nil {
		t.Fatal(err)
	}
	var back []linkStatement
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("hostile value broke the JSON: %v", err)
	}
	if back[0].Target.PackageName != short {
		t.Fatal("hostile value did not round trip")
	}
	if strings.Contains(string(body), "</script>") {
		t.Fatal("angle brackets are not escaped")
	}
}

func TestDocumentSizeIsCapped(t *testing.T) {
	long := make([]string, 100)
	for i := range long {
		long[i] = "/" + strings.Repeat("a", 255)
	}
	var apps []AppleApp
	for i := range 20 {
		apps = append(apps, AppleApp{
			AppleID: AppleID{TeamID: "ABCDE12345", BundleID: "com.example.a" + strconv.Itoa(i)},
			Paths:   long,
		})
	}
	_, err := New(Config{Apple: AppleConfig{AppLinks: apps}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "document is") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
}

func TestHostileConfigurationIsRefused(t *testing.T) {
	hostile := []string{
		"\"},{\"x\":\"",
		"a\nb",
		"a\\b",
		"</script>",
		"\x00",
		strings.Repeat("a", 10_000),
	}
	for _, v := range hostile {
		cfg := Config{
			Apple: AppleConfig{
				AppLinks:       []AppleApp{{AppleID: AppleID{TeamID: v, BundleID: v}, Paths: []string{v}, ExcludePaths: []string{v}}},
				WebCredentials: []AppleID{{TeamID: v, BundleID: v}},
			},
			Android: AndroidConfig{Apps: []AndroidApp{{PackageName: v, Fingerprints: []string{v}, Relations: []Relation{Relation(v)}}}},
		}
		_, err := New(cfg)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("hostile %.20q: err = %v", v, err)
		}
		if len(err.Error()) > 8000 {
			t.Errorf("hostile %.20q: error is %d bytes, the value is not cut", v, len(err.Error()))
		}
	}
}

func TestValidation(t *testing.T) {
	app := func(mod func(*AppleApp)) Config {
		a := AppleApp{AppleID: AppleID{TeamID: "ABCDE12345", BundleID: "com.example.app"}, Paths: []string{"/app/*"}}
		mod(&a)
		return Config{Apple: AppleConfig{AppLinks: []AppleApp{a}}}
	}
	android := func(mod func(*AndroidApp)) Config {
		a := AndroidApp{PackageName: "com.example.app", Fingerprints: []string{fpUpper}}
		mod(&a)
		return Config{Android: AndroidConfig{Apps: []AndroidApp{a}}}
	}
	many := func(n int, s string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = s
		}
		return out
	}

	tests := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"valid apple", app(func(*AppleApp) {}), true},
		{"valid android", android(func(*AndroidApp) {}), true},
		{"lowercase fingerprint", android(func(a *AndroidApp) { a.Fingerprints = []string{fpLower} }), true},
		{"team id lowercase", app(func(a *AppleApp) { a.TeamID = "abcde12345" }), false},
		{"team id short", app(func(a *AppleApp) { a.TeamID = "ABCDE1234" }), false},
		{"team id long", app(func(a *AppleApp) { a.TeamID = "ABCDE123456" }), false},
		{"team id empty", app(func(a *AppleApp) { a.TeamID = "" }), false},
		{"team id dot", app(func(a *AppleApp) { a.TeamID = "ABCDE.2345" }), false},
		{"bundle id empty", app(func(a *AppleApp) { a.BundleID = "" }), false},
		{"bundle id wildcard", app(func(a *AppleApp) { a.BundleID = "com.example.*" }), false},
		{"bundle id double dot", app(func(a *AppleApp) { a.BundleID = "com..app" }), false},
		{"bundle id space", app(func(a *AppleApp) { a.BundleID = "com.example app" }), false},
		{"bundle id too long", app(func(a *AppleApp) { a.BundleID = strings.Repeat("a", 156) }), false},
		{"no paths", app(func(a *AppleApp) { a.Paths = nil }), false},
		{"path without slash", app(func(a *AppleApp) { a.Paths = []string{"app"} }), false},
		{"path with query", app(func(a *AppleApp) { a.Paths = []string{"/a?b=c&d"} }), false},
		{"path with quote", app(func(a *AppleApp) { a.Paths = []string{`/a"b`} }), false},
		{"path with space", app(func(a *AppleApp) { a.Paths = []string{"/a b"} }), false},
		{"path with fragment", app(func(a *AppleApp) { a.Paths = []string{"/a#b"} }), false},
		{"path too long", app(func(a *AppleApp) { a.Paths = []string{"/" + strings.Repeat("a", 256)} }), false},
		{"path at limit", app(func(a *AppleApp) { a.Paths = []string{"/" + strings.Repeat("a", 255)} }), true},
		{"exclude path bad", app(func(a *AppleApp) { a.ExcludePaths = []string{"x"} }), false},
		{"too many paths", app(func(a *AppleApp) { a.Paths = many(101, "/a") }), false},
		{"paths at limit", app(func(a *AppleApp) { a.Paths = many(100, "/a") }), true},
		{"duplicate app", Config{Apple: AppleConfig{AppLinks: []AppleApp{
			{AppleID: AppleID{TeamID: "ABCDE12345", BundleID: "a.b"}, Paths: []string{"/x"}},
			{AppleID: AppleID{TeamID: "ABCDE12345", BundleID: "a.b"}, Paths: []string{"/y"}},
		}}}, false},
		{"too many apps", Config{Apple: AppleConfig{WebCredentials: func() []AppleID {
			out := make([]AppleID, 21)
			for i := range out {
				out[i] = AppleID{TeamID: "ABCDE12345", BundleID: "a.b" + strconv.Itoa(i)}
			}
			return out
		}()}}, false},
		{"path with percent", app(func(a *AppleApp) { a.Paths = []string{"/a%2Fb"} }), false},
		{"applinks apps at limit", Config{Apple: AppleConfig{AppLinks: manyApple(20)}}, true},
		{"too many applinks apps", Config{Apple: AppleConfig{AppLinks: manyApple(21)}}, false},
		{"android apps at limit", Config{Android: AndroidConfig{Apps: manyAndroid(20)}}, true},
		{"too many android apps", Config{Android: AndroidConfig{Apps: manyAndroid(21)}}, false},
		{"webcredentials at limit", Config{Apple: AppleConfig{WebCredentials: manyIDs(20)}}, true},
		{"duplicate webcredentials", Config{Apple: AppleConfig{WebCredentials: []AppleID{
			{TeamID: "ABCDE12345", BundleID: "a.b"},
			{TeamID: "ABCDE12345", BundleID: "a.b"},
		}}}, false},
		{"webcredentials bad id", Config{Apple: AppleConfig{WebCredentials: []AppleID{{TeamID: "x", BundleID: "a.b"}}}}, false},
		{"package one segment", android(func(a *AndroidApp) { a.PackageName = "example" }), false},
		{"package digit start", android(func(a *AndroidApp) { a.PackageName = "com.1example" }), false},
		{"package hyphen", android(func(a *AndroidApp) { a.PackageName = "com.my-app" }), false},
		{"package empty", android(func(a *AndroidApp) { a.PackageName = "" }), false},
		{"package trailing dot", android(func(a *AndroidApp) { a.PackageName = "com.example." }), false},
		{"package underscore", android(func(a *AndroidApp) { a.PackageName = "com.my_app" }), true},
		{"package too long", android(func(a *AndroidApp) { a.PackageName = "com." + strings.Repeat("a", 252) }), false},
		{"no fingerprint", android(func(a *AndroidApp) { a.Fingerprints = nil }), false},
		{"fingerprint short", android(func(a *AndroidApp) { a.Fingerprints = []string{fpUpper[:len(fpUpper)-3]} }), false},
		{"fingerprint long", android(func(a *AndroidApp) { a.Fingerprints = []string{fpUpper + ":00"} }), false},
		{"fingerprint no colons", android(func(a *AndroidApp) { a.Fingerprints = []string{strings.ReplaceAll(fpUpper, ":", "")} }), false},
		{"fingerprint dashes", android(func(a *AndroidApp) { a.Fingerprints = []string{strings.ReplaceAll(fpUpper, ":", "-")} }), false},
		{"fingerprint non hex", android(func(a *AndroidApp) { a.Fingerprints = []string{"ZZ" + fpUpper[2:]} }), false},
		{"fingerprint padded", android(func(a *AndroidApp) { a.Fingerprints = []string{" " + fpUpper} }), false},
		{"too many fingerprints", android(func(a *AndroidApp) {
			a.Fingerprints = nil
			for i := range 11 {
				a.Fingerprints = append(a.Fingerprints, strings.Replace(fpUpper, "AA", strings.ToUpper(strconv.FormatInt(int64(16+i), 16)), 1))
			}
		}), false},
		{"unknown relation", android(func(a *AndroidApp) { a.Relations = []Relation{"delegate_permission/common.other"} }), false},
		{"duplicate package", Config{Android: AndroidConfig{Apps: []AndroidApp{
			{PackageName: "com.example.app", Fingerprints: []string{fpUpper}},
			{PackageName: "com.example.app", Fingerprints: []string{fpOther}},
		}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(tc.cfg)
			if tc.ok {
				if err != nil || h == nil {
					t.Fatalf("New = %v, want success", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("New err = %v, want ErrInvalid", err)
			}
			if h != nil {
				t.Fatal("a refused configuration returned handlers")
			}
		})
	}
}

func manyApple(n int) []AppleApp {
	out := make([]AppleApp, n)
	for i := range out {
		out[i] = AppleApp{
			AppleID: AppleID{TeamID: "ABCDE12345", BundleID: "com.example.a" + strconv.Itoa(i)},
			Paths:   []string{"/a"},
		}
	}
	return out
}

func manyIDs(n int) []AppleID {
	out := make([]AppleID, n)
	for i := range out {
		out[i] = AppleID{TeamID: "ABCDE12345", BundleID: "com.example.a" + strconv.Itoa(i)}
	}
	return out
}

func manyAndroid(n int) []AndroidApp {
	out := make([]AndroidApp, n)
	for i := range out {
		out[i] = AndroidApp{PackageName: "com.example.a" + strconv.Itoa(i), Fingerprints: []string{fpUpper}}
	}
	return out
}

// TestDocumentSizeBoundary pins the cap at 64 KiB with a literal, so a
// change of the constant fails here: the largest accepted document is just
// under it and the next path is refused.
func TestDocumentSizeBoundary(t *testing.T) {
	const limit = 64 << 10
	path := "/" + strings.Repeat("a", 255)
	cfg := func(n int) AppleConfig {
		var apps []AppleApp
		for left, i := n, 0; left > 0; i++ {
			k := min(left, maxPaths)
			paths := make([]string, k)
			for j := range paths {
				paths[j] = path
			}
			apps = append(apps, AppleApp{
				AppleID: AppleID{TeamID: "ABCDE12345", BundleID: "com.example.a" + strconv.Itoa(i)},
				Paths:   paths,
			})
			left -= k
		}
		return AppleConfig{AppLinks: apps}
	}
	// 20 apps of 100 paths hold about 580 KB, far over the cap.
	n := 1
	for ; n <= maxApps*maxPaths; n++ {
		if _, errs := buildApple(cfg(n)); len(errs) > 0 {
			break
		}
	}
	if n == 1 || n > maxApps*maxPaths {
		t.Fatalf("no boundary found, n = %d", n)
	}
	body, errs := buildApple(cfg(n - 1))
	if len(errs) > 0 {
		t.Fatalf("%d paths refused: %v", n-1, errs)
	}
	if len(body) > limit || len(body) < limit-400 {
		t.Fatalf("largest accepted document is %d bytes, want just under %d", len(body), limit)
	}
}

func TestValidationReportsEveryProblem(t *testing.T) {
	_, err := New(Config{
		Apple:   AppleConfig{AppLinks: []AppleApp{{AppleID: AppleID{TeamID: "x", BundleID: ""}}}},
		Android: AndroidConfig{Apps: []AndroidApp{{PackageName: "x"}}},
	})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"team id", "bundle id", "at least one path", "package name", "at least one fingerprint"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}
