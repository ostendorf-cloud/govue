// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package csrf_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/csrf"
)

var key = []byte("a signing key long enough for hmac")

func TestToken(t *testing.T) {
	t.Parallel()

	first := csrf.Token(key, "session-1")
	if first == "" {
		t.Fatal("empty token")
	}
	if first != csrf.Token(key, "session-1") {
		t.Error("the token of one session is not stable")
	}

	for _, tc := range []struct {
		name      string
		key       []byte
		sessionID string
	}{
		{"another session", key, "session-2"},
		{"another key", []byte("another signing key that is long"), "session-1"},
		{"a session id with a trailing space", key, "session-1 "},
		{"a session id with a leading separator", key, "\x00session-1"},
		{"the empty session id", key, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := csrf.Token(tc.key, tc.sessionID); got == first {
				t.Error("the token is shared with session-1 under the original key")
			}
		})
	}
}

// Equal has to be constant time, and that property is not what the cases below
// assert: a timing measurement inside a unit test is dominated by the
// scheduler, the garbage collector and the CPU's own frequency scaling, so a
// threshold either misses a leak or fails at random in CI. Neither is worth
// having. What the cases assert is the behaviour, including the length
// mismatches a variable time implementation gets wrong first; the timing
// guarantee comes from crypto/subtle.ConstantTimeCompare over two fixed length
// digests, which is the only place in this package where a secret decides a
// branch.
func TestEqual(t *testing.T) {
	t.Parallel()

	token := csrf.Token(key, "session-1")

	for _, tc := range []struct {
		name     string
		expected string
		given    string
		want     bool
	}{
		{"the same token", token, token, true},
		{"another session's token", token, csrf.Token(key, "session-2"), false},
		{"a difference in the first byte", token, flip(token, 0), false},
		{"a difference in the last byte", token, flip(token, len(token)-1), false},
		{"a longer token", token, token + "x", false},
		{"a shorter token", token, token[:len(token)-1], false},
		{"no token given", token, "", false},
		{"no token expected", "", token, false},
		{"neither side has a token", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := csrf.Equal(tc.expected, tc.given); got != tc.want {
				t.Errorf("Equal = %v, want %v", got, tc.want)
			}
		})
	}
}

// flip returns s with the byte at i replaced by a different one, so a case
// stays a mismatch whatever the token happens to start or end with.
func flip(s string, i int) string {
	changed := []byte(s)
	if changed[i] == 'A' {
		changed[i] = 'B'
	} else {
		changed[i] = 'A'
	}
	return string(changed)
}

func TestFromRequest(t *testing.T) {
	t.Parallel()

	token := csrf.Token(key, "session-1")

	form := url.Values{csrf.FieldName: {token}, "title": {"lamp"}}
	urlencoded := func(t *testing.T) *http.Request {
		t.Helper()

		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	for _, tc := range []struct {
		name    string
		request func(*testing.T) *http.Request
		want    string
	}{
		{
			name: "the header",
			request: func(t *testing.T) *http.Request {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
				r.Header.Set(csrf.HeaderName, token)
				return r
			},
			want: token,
		},
		{
			name:    "a urlencoded form field",
			request: urlencoded,
			want:    token,
		},
		{
			name:    "a multipart form field",
			request: func(t *testing.T) *http.Request { return multipartRequest(t, token) },
			want:    token,
		},
		{
			name: "the header wins over the form field",
			request: func(t *testing.T) *http.Request {
				r := urlencoded(t)
				r.Header.Set(csrf.HeaderName, csrf.Token(key, "session-2"))
				return r
			},
			want: csrf.Token(key, "session-2"),
		},
		{
			name: "nothing at all",
			request: func(t *testing.T) *http.Request {
				return httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
			},
			want: "",
		},
		{
			name: "the query string is not a place to send a token",
			request: func(t *testing.T) *http.Request {
				return httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things?"+form.Encode(), nil)
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := csrf.FromRequest(tc.request(t)); got != tc.want {
				t.Errorf("FromRequest = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFromRequestLeavesAJSONBodyAlone(t *testing.T) {
	t.Parallel()

	body := `{"title":"lamp"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	if got := csrf.FromRequest(req); got != "" {
		t.Errorf("FromRequest = %q, want empty", got)
	}

	// The handler behind the middleware still has to be able to decode the body.
	read, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	if string(read) != body {
		t.Errorf("the body reads %q, want %q", read, body)
	}
}

// sessionOf fakes what reads the session out of a request. In an application
// this closes over the session layer and derives the token with Token; the
// package under test never learns which it was.
func sessionOf(token string, ok bool) csrf.Expected {
	return func(*http.Request) (string, bool) { return token, ok }
}

func TestProtectorCheck(t *testing.T) {
	t.Parallel()

	token := csrf.Token(key, "session-1")

	for _, tc := range []struct {
		name     string
		method   string
		expected csrf.Expected
		header   string
		wantErr  error
	}{
		{"a safe method needs no token", http.MethodGet, sessionOf(token, true), "", nil},
		{"a head needs no token", http.MethodHead, sessionOf(token, true), "", nil},
		{"an options needs no token", http.MethodOptions, sessionOf(token, true), "", nil},
		{"a write with the right token", http.MethodPost, sessionOf(token, true), token, nil},
		{"a write without a session", http.MethodPost, sessionOf("", false), "", nil},
		{"a write without a token", http.MethodPost, sessionOf(token, true), "", csrf.ErrNoToken},
		{"a write with another session's token", http.MethodPost, sessionOf(token, true), csrf.Token(key, "session-2"), csrf.ErrBadToken},
		{"a put is checked too", http.MethodPut, sessionOf(token, true), "", csrf.ErrNoToken},
		{"a patch is checked too", http.MethodPatch, sessionOf(token, true), "", csrf.ErrNoToken},
		{"a delete is checked too", http.MethodDelete, sessionOf(token, true), "", csrf.ErrNoToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/things", nil)
			if tc.header != "" {
				req.Header.Set(csrf.HeaderName, tc.header)
			}

			protector, err := csrf.New(tc.expected)
			if err != nil {
				t.Fatal(err)
			}
			if err := protector.Check(req); !errors.Is(err, tc.wantErr) {
				t.Errorf("Check = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestProtectorCheckRefusesACrossSiteRequest(t *testing.T) {
	t.Parallel()

	token := csrf.Token(key, "session-1")

	for _, tc := range []struct {
		name   string
		header map[string]string
	}{
		{"the browser says cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"the browser says same-site, which a subdomain is too", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"no fetch metadata and a foreign origin", map[string]string{"Origin": "https://attacker.example.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A valid token on purpose: the origin half has to refuse on its own.
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
			req.Header.Set(csrf.HeaderName, token)
			for name, value := range tc.header {
				req.Header.Set(name, value)
			}

			protector, err := csrf.New(sessionOf(token, true))
			if err != nil {
				t.Fatal(err)
			}
			if err := protector.Check(req); err == nil {
				t.Error("Check = nil, want the request refused")
			}
		})
	}
}

func TestProtectorTrustOrigin(t *testing.T) {
	t.Parallel()

	token := csrf.Token(key, "session-1")

	t.Run("a trusted origin passes", func(t *testing.T) {
		t.Parallel()

		protector, err := csrf.New(sessionOf(token, true))
		if err != nil {
			t.Fatal(err)
		}
		if err := protector.TrustOrigin("https://app.example.test"); err != nil {
			t.Fatalf("TrustOrigin: %v", err)
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://app.example.test")
		req.Header.Set(csrf.HeaderName, token)

		if err := protector.Check(req); err != nil {
			t.Errorf("Check = %v, want the trusted origin accepted", err)
		}
	})

	t.Run("a trusted origin still needs the token", func(t *testing.T) {
		t.Parallel()

		protector, err := csrf.New(sessionOf(token, true))
		if err != nil {
			t.Fatal(err)
		}
		if err := protector.TrustOrigin("https://app.example.test"); err != nil {
			t.Fatalf("TrustOrigin: %v", err)
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://app.example.test")

		if err := protector.Check(req); !errors.Is(err, csrf.ErrNoToken) {
			t.Errorf("Check = %v, want %v", err, csrf.ErrNoToken)
		}
	})

	for _, origin := range []string{"not an origin", "https://app.example.test/", "app.example.test", "*", ""} {
		t.Run("TrustOrigin refuses "+origin, func(t *testing.T) {
			t.Parallel()

			protector, err := csrf.New(sessionOf(token, true))
			if err != nil {
				t.Fatal(err)
			}
			if err := protector.TrustOrigin(origin); err == nil {
				t.Errorf("TrustOrigin(%q) = nil, want an error", origin)
			}
		})
	}
}

func TestProtectorMiddleware(t *testing.T) {
	t.Parallel()

	token := csrf.Token(key, "session-1")

	t.Run("a refused request never reaches the handler", func(t *testing.T) {
		t.Parallel()

		protector, err := csrf.New(sessionOf(token, true))
		if err != nil {
			t.Fatal(err)
		}
		served := false
		handler := protector.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
		if served {
			t.Error("the refused request reached the handler")
		}
		if body := rec.Body.String(); strings.Contains(body, csrf.ErrNoToken.Error()) {
			t.Errorf("the response names the cause: %q", body)
		}
	})

	t.Run("an accepted request reaches the handler", func(t *testing.T) {
		t.Parallel()

		protector, err := csrf.New(sessionOf(token, true))
		if err != nil {
			t.Fatal(err)
		}
		served := false
		handler := protector.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil)
		req.Header.Set(csrf.HeaderName, token)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !served {
			t.Error("the accepted request did not reach the handler")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}

func TestProtectorOnRefusal(t *testing.T) {
	t.Parallel()

	protector, err := csrf.New(sessionOf(csrf.Token(key, "session-1"), true))
	if err != nil {
		t.Fatal(err)
	}
	protector.OnRefusal(func(w http.ResponseWriter, _ *http.Request, err error) {
		if !errors.Is(err, csrf.ErrNoToken) {
			t.Errorf("err = %v, want %v", err, csrf.ErrNoToken)
		}
		http.Error(w, "reload the page", http.StatusConflict)
	})

	rec := httptest.NewRecorder()
	protector.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", nil))

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if body := rec.Body.String(); !strings.Contains(body, "reload the page") {
		t.Errorf("body = %q, want the replacement response", body)
	}
}

func TestNewRequiresExpected(t *testing.T) {
	t.Parallel()

	_, err := csrf.New(nil)
	if err == nil {
		t.Fatal("a nil Expected was accepted")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("error %q does not name the missing Expected", err)
	}
}

// multipartRequest builds the other kind of form submit, the one a file upload
// uses, with the token in a field beside the payload.
func multipartRequest(t *testing.T, token string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range map[string]string{csrf.FieldName: token, "title": "lamp"} {
		if err := form.WriteField(name, value); err != nil {
			t.Fatalf("writing the %s field: %v", name, err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatalf("closing the form: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/things", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}
