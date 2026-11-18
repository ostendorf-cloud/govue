// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
)

var (
	now    = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	alice  = account.Identity{ID: "memory-1", Login: "alice@example.test"}
	bob    = account.Identity{ID: "memory-2", Login: "bob@example.test"}
	handle = []byte("0123456789abcdef0123456789abcdef")
)

// fixture is a set of handlers over a memory store, with the session and the
// other factors of the request under the test's control.
type fixture struct {
	store      *passkeytest.MemoryStore
	h          *passkey.Handlers
	who        account.Identity
	loggedIn   bool
	loginAt    time.Time
	others     int
	sessErr    error
	factorsErr error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: passkeytest.NewMemoryStore(), who: alice, loggedIn: true, loginAt: now.Add(-time.Minute)}
	f.store.PutAccount(alice)
	f.store.PutAccount(bob)
	h, err := passkey.New(passkey.Config{
		RPID:          "app.example.com",
		RPDisplayName: "Example App",
		Origins:       []string{"https://app.example.com"},
		Store:         f.store,
		StartSession: func(context.Context, http.ResponseWriter, *http.Request, account.Identity) error {
			return errors.New("not used here")
		},
		CurrentAccount: func(*http.Request) (account.Identity, time.Time, bool, error) {
			return f.who, f.loginAt, f.loggedIn, f.sessErr
		},
		OtherFactors: func(_ context.Context, id account.Identity) (int, error) {
			if id != f.who {
				return 0, errors.New("other factors asked for the wrong account")
			}
			return f.others, f.factorsErr
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.h = h
	return f
}

func (f *fixture) add(t *testing.T, owner account.Identity, id string) {
	t.Helper()
	err := f.store.AddCredential(context.Background(), passkey.Credential{
		ID: []byte(id), AccountID: owner.ID, UserHandle: handle, Name: "key " + id, CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) count(t *testing.T, owner account.Identity) int {
	t.Helper()
	creds, err := f.store.Credentials(context.Background(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	return len(creds)
}

func newRequest(method, target string, body io.Reader) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), method, target, body)
}

func encoded(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }

func deleteRequest(id string) *http.Request {
	r := newRequest(http.MethodPost, "/account/passkeys/delete", strings.NewReader(`{"id":"`+encoded(id)+`"}`))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", w.Body, err)
	}
	return body.Error.Code
}

func TestList(t *testing.T) {
	f := newFixture(t)
	f.add(t, alice, "a1")
	f.add(t, bob, "b1")
	used := now.Add(-time.Second)
	if err := f.store.UpdateCredential(context.Background(), passkey.Credential{
		ID: []byte("a1"), AccountID: alice.ID, SignCount: 1, LastUsedAt: used,
	}, 0); err != nil {
		t.Fatal(err)
	}
	f.add(t, alice, "a2")

	w := serve(f.h.List(), newRequest(http.MethodGet, "/account/passkeys", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	want := `{"passkeys":[` +
		`{"id":"` + encoded("a1") + `","name":"key a1","created_at":"2026-11-18T09:00:00Z","last_used_at":"2026-11-18T09:59:59Z"},` +
		`{"id":"` + encoded("a2") + `","name":"key a2","created_at":"2026-11-18T09:00:00Z","last_used_at":null}]}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestListEmptyIsArray(t *testing.T) {
	f := newFixture(t)
	w := serve(f.h.List(), newRequest(http.MethodGet, "/account/passkeys", nil))
	if got := strings.TrimSpace(w.Body.String()); got != `{"passkeys":[]}` {
		t.Fatalf("body %s", got)
	}
}

func TestListAndDeleteRefusals(t *testing.T) {
	tests := []struct {
		name     string
		handler  func(*passkey.Handlers) http.Handler
		request  func() *http.Request
		setup    func(*fixture)
		status   int
		code     string
		accounts int // alice's credentials afterwards, -1 to skip
	}{
		{
			"list without session", (*passkey.Handlers).List,
			func() *http.Request { return newRequest(http.MethodGet, "/", nil) },
			func(f *fixture) { f.loggedIn = false }, http.StatusUnauthorized, "unauthenticated", -1,
		},
		{
			"list session store down", (*passkey.Handlers).List,
			func() *http.Request { return newRequest(http.MethodGet, "/", nil) },
			func(f *fixture) { f.sessErr = errors.New("down") }, http.StatusServiceUnavailable, "internal", -1,
		},
		{
			"list by POST", (*passkey.Handlers).List,
			func() *http.Request { return newRequest(http.MethodPost, "/", nil) },
			func(*fixture) {}, http.StatusMethodNotAllowed, "method_not_allowed", -1,
		},
		{
			"delete without session", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("a1") },
			func(f *fixture) { f.loggedIn = false }, http.StatusUnauthorized, "unauthenticated", 2,
		},
		{
			"delete with stale login", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("a1") },
			func(f *fixture) { f.loginAt = now.Add(-passkey.DefaultReauthWindow - time.Second) },
			http.StatusForbidden, passkey.CodeReauthenticationRequired, 2,
		},
		{
			"delete with zero login time", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("a1") },
			func(f *fixture) { f.loginAt = time.Time{} }, http.StatusForbidden, passkey.CodeReauthenticationRequired, 2,
		},
		{
			"delete with future login time", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("a1") },
			func(f *fixture) { f.loginAt = now.Add(time.Minute) }, http.StatusForbidden, passkey.CodeReauthenticationRequired, 2,
		},
		{"delete cross site", (*passkey.Handlers).Delete, func() *http.Request {
			r := deleteRequest("a1")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			return r
		}, func(*fixture) {}, http.StatusForbidden, "permission_denied", 2},
		{"delete foreign origin", (*passkey.Handlers).Delete, func() *http.Request {
			r := deleteRequest("a1")
			r.Header.Set("Origin", "https://evil.example.org")
			return r
		}, func(*fixture) {}, http.StatusForbidden, "permission_denied", 2},
		{
			"delete by GET", (*passkey.Handlers).Delete,
			func() *http.Request { return newRequest(http.MethodGet, "/", nil) },
			func(*fixture) {}, http.StatusMethodNotAllowed, "method_not_allowed", 2,
		},
		{"delete form body", (*passkey.Handlers).Delete, func() *http.Request {
			r := deleteRequest("a1")
			r.Header.Set("Content-Type", "text/plain")
			return r
		}, func(*fixture) {}, http.StatusUnsupportedMediaType, "unsupported_media_type", 2},
		{"delete unknown field", (*passkey.Handlers).Delete, func() *http.Request {
			r := newRequest(http.MethodPost, "/", strings.NewReader(`{"id":"x","extra":1}`))
			r.Header.Set("Content-Type", "application/json")
			return r
		}, func(*fixture) {}, http.StatusBadRequest, "invalid_argument", 2},
		{"delete bad id", (*passkey.Handlers).Delete, func() *http.Request {
			r := newRequest(http.MethodPost, "/", strings.NewReader(`{"id":"!!"}`))
			r.Header.Set("Content-Type", "application/json")
			return r
		}, func(*fixture) {}, http.StatusNotFound, "not_found", 2},
		{
			"delete unknown id", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("zz") },
			func(*fixture) {}, http.StatusNotFound, "not_found", 2,
		},
		{
			"delete another account's passkey", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("b1") },
			func(*fixture) {}, http.StatusNotFound, "not_found", 2,
		},
		{
			"delete another account's passkey with keepOne", (*passkey.Handlers).Delete,
			func() *http.Request { return deleteRequest("a1") },
			func(f *fixture) { f.who, f.others = bob, 0 }, http.StatusNotFound, "not_found", 2,
		},
		{
			"delete other factors failing", (*passkey.Handlers).Delete, func() *http.Request { return deleteRequest("a1") },
			func(f *fixture) { f.factorsErr = errors.New("down") },
			http.StatusInternalServerError, "internal", 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.others = 1
			f.add(t, alice, "a1")
			f.add(t, alice, "a2")
			f.add(t, bob, "b1")
			tt.setup(f)
			w := serve(tt.handler(f.h), tt.request())
			if w.Code != tt.status {
				t.Fatalf("status: got %d, want %d, body %s", w.Code, tt.status, w.Body)
			}
			if code := errorCode(t, w); code != tt.code {
				t.Fatalf("code: got %q, want %q", code, tt.code)
			}
			if tt.accounts >= 0 && f.count(t, alice) != tt.accounts {
				t.Fatalf("alice has %d passkeys, want %d", f.count(t, alice), tt.accounts)
			}
		})
	}
}

func TestDeleteLastFactor(t *testing.T) {
	tests := []struct {
		name     string
		others   int
		keys     []string
		target   string
		status   int
		code     string
		leftOver int
	}{
		{"one passkey and totp", 1, []string{"a1"}, "a1", http.StatusNoContent, "", 0},
		{"two passkeys no totp", 0, []string{"a1", "a2"}, "a1", http.StatusNoContent, "", 1},
		{"last passkey no totp", 0, []string{"a1"}, "a1", http.StatusConflict, passkey.CodeLastFactor, 1},
		{"unknown id no totp", 0, []string{"a1"}, "zz", http.StatusNotFound, "not_found", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.others = tt.others
			for _, k := range tt.keys {
				f.add(t, alice, k)
			}
			w := serve(f.h.Delete(), deleteRequest(tt.target))
			if w.Code != tt.status {
				t.Fatalf("status: got %d, want %d, body %s", w.Code, tt.status, w.Body)
			}
			if tt.code != "" {
				if code := errorCode(t, w); code != tt.code {
					t.Fatalf("code: got %q, want %q", code, tt.code)
				}
			} else if w.Body.Len() != 0 {
				t.Fatalf("204 with a body: %s", w.Body)
			}
			if got := f.count(t, alice); got != tt.leftOver {
				t.Fatalf("alice has %d passkeys, want %d", got, tt.leftOver)
			}
		})
	}
}

func TestDeleteRecencyBoundary(t *testing.T) {
	f := newFixture(t)
	f.others = 1
	f.add(t, alice, "a1")
	f.loginAt = now.Add(-passkey.DefaultReauthWindow)
	if w := serve(f.h.Delete(), deleteRequest("a1")); w.Code != http.StatusNoContent {
		t.Fatalf("a login exactly at the window edge was refused: %d %s", w.Code, w.Body)
	}
}
