// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package devicehttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/session"
	"github.com/ostendorf-cloud/govue/session/devicehttp"
)

func decodeToken(t *testing.T, w *httptest.ResponseRecorder) devicehttp.TokenResult {
	t.Helper()
	var res devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Token == "" {
		t.Fatalf("token body = %s, err %v", w.Body.String(), err)
	}

	return res
}

// The trusted origins of Issue are for Issue alone. A web page of an origin
// trusted for the app's sign in must not ride a cookie on the other routes.
func TestIssueTrustedOriginsApplyToIssueOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a1 := f.issue(credsA(), "Pixel")
	a2 := f.issue(credsA(), "Tablet")

	trusted := func(r *http.Request) {
		r.Header.Set("Origin", appOrigin)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	if w := f.do(http.MethodPost, "/auth/token", credsA(), trusted); w.Code != http.StatusCreated {
		t.Fatalf("issue from the trusted origin = %d, want 201", w.Code)
	}

	cookie := f.cookieFor(subjectA)
	w := f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil, withCookie(cookie), trusted)
	wantStatus(t, w, http.StatusForbidden)
	if _, err := f.manager.AuthenticateDevice(t.Context(), a2.Token); err != nil {
		t.Fatalf("a request from an origin trusted for issue revoked a device: %v", err)
	}
	// A bearer request is no ambient credential, whatever its origin.
	w = f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil, bearerOf(a1.Token), trusted)
	wantStatus(t, w, http.StatusNoContent)
}

func TestNewHandlersRefusesAManagerWithoutADeviceStore(t *testing.T) {
	t.Parallel()
	c := &clock{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	manager, err := session.New(session.Config{Store: session.NewMemoryStore(c.Now, 10), Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = devicehttp.NewHandlers(devicehttp.HandlerConfig{
		Sessions: manager,
		VerifyCredentials: func(context.Context, devicehttp.Credentials) (string, error) {
			return subjectA, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "device store") {
		t.Fatalf("NewHandlers = %v, want a refusal that names the device store", err)
	}
}

func TestEveryAnswerOfAnOwnersRevokeIsNoStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a1 := f.issue(credsA(), "Pixel")
	a2 := f.issue(credsA(), "Tablet")
	cookie := f.cookieFor(subjectA)

	for _, tc := range []struct {
		name string
		id   string
		opts []option
		want int
	}{
		{"cookie, own device", a2.TokenID, []option{withCookie(cookie)}, http.StatusNoContent},
		{"cookie, unknown id", "unknown", []option{withCookie(cookie)}, http.StatusNotFound},
		{"bearer, own device", a1.TokenID, []option{bearerOf(a1.Token)}, http.StatusNoContent},
		{"no credential", a1.TokenID, nil, http.StatusUnauthorized},
	} {
		w := f.do(http.MethodDelete, "/auth/devices/"+tc.id, nil, tc.opts...)
		if w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, w.Code, tc.want)
		}
		wantNoStore(t, w)
	}
}

func TestStoreFailuresAreNeverAnEmptyAnswerOrASuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a := f.issue(credsA(), "Pixel")
	cookie := f.cookieFor(subjectA)
	boom := errors.New("db-internal.example.test refused the connection")

	// A full store at issue is the one 503 a server chooses on purpose.
	f.store.fail(session.ErrDeviceStoreFull, nil, nil, nil)
	w := f.do(http.MethodPost, "/auth/token", credsA())
	wantStatus(t, w, http.StatusServiceUnavailable)
	wantNoStore(t, w)
	// Any other store error at issue is an opaque 500.
	f.store.fail(boom, nil, nil, nil)
	w = f.do(http.MethodPost, "/auth/token", credsA())
	wantStatus(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "db-internal") {
		t.Errorf("a cause reached the client: %s", w.Body.String())
	}

	// A list that fails is a 500, never an empty list.
	f.store.fail(nil, boom, nil, nil)
	w = f.do(http.MethodGet, "/auth/devices", nil, withCookie(cookie))
	wantStatus(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "devices") || strings.Contains(w.Body.String(), "db-internal") {
		t.Errorf("body = %s", w.Body.String())
	}

	// A revoke that fails is a 500, not a 204 and not a 404.
	f.store.fail(nil, nil, nil, boom)
	w = f.do(http.MethodDelete, "/auth/devices/"+a.TokenID, nil, withCookie(cookie))
	wantStatus(t, w, http.StatusInternalServerError)
	wantNoStore(t, w)

	// A store that cannot answer the token lookup is the middleware's read
	// error, and List and Revoke answer 503. It is no 401: the app must not
	// drop a token that is fine.
	f.store.fail(nil, nil, boom, nil)
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/auth/devices"},
		{http.MethodDelete, "/auth/devices/" + a.TokenID},
	} {
		w = f.do(req.method, req.path, nil, bearerOf(a.Token))
		wantStatus(t, w, http.StatusServiceUnavailable)
		wantNoStore(t, w)
		if strings.Contains(w.Body.String(), "db-internal") {
			t.Errorf("a cause reached the client: %s", w.Body.String())
		}
	}

	// A store error inside rotation and sign out is an opaque 500.
	f.clock.Add(2 * session.DefaultDeviceMinRotateInterval)
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/auth/token/rotate", `{"nonce":"n1"}`},
		{http.MethodDelete, "/auth/token", ""},
	} {
		var body any
		if req.body != "" {
			body = req.body
		}
		w = f.do(req.method, req.path, body, bearerOf(a.Token))
		if w.Code == http.StatusOK || w.Code == http.StatusNoContent {
			t.Errorf("%s %s succeeded on a failing store", req.method, req.path)
		}
	}

	// When the store is back the device is still there.
	f.store.fail(nil, nil, nil, nil)
	if _, err := f.manager.AuthenticateDevice(t.Context(), a.Token); err != nil {
		t.Errorf("the device is gone after the outage: %v", err)
	}
}

// Rotate and SignOut check the token themselves. Behind the middleware a bad
// token never reaches them, so these tests mount the handlers without it.
func TestRotateAndSignOutAnswer401ForATokenThatDoesNotAuthenticateWithoutTheMiddleware(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	gone := f.issue(credsA(), "Pixel")
	if w := f.do(http.MethodDelete, "/auth/token", nil, bearerOf(gone.Token)); w.Code != http.StatusNoContent {
		t.Fatalf("sign out = %d", w.Code)
	}
	f.clock.Add(2 * session.DefaultDeviceMinRotateInterval)

	for name, token := range map[string]string{"revoked": gone.Token, "junk": "example_junk_token"} {
		for _, req := range []struct{ method, path, body string }{
			{http.MethodPost, "/auth/token/rotate", `{"nonce":"n1"}`},
			{http.MethodDelete, "/auth/token", ""},
		} {
			r := httptest.NewRequestWithContext(t.Context(), req.method, req.path, strings.NewReader(req.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with a %s token = %d, want 401: %s", req.method, req.path, name, w.Code, w.Body.String())
			}
			if got := w.Header().Get("WWW-Authenticate"); got != `Bearer error="invalid_token"` {
				t.Errorf("%s %s with a %s token: WWW-Authenticate = %q", req.method, req.path, name, got)
			}
			wantNoStore(t, w)
		}
	}
}

// A holder of the old token inside the grace window is a full credential of
// the device, except for rotation. The documentation says so, and this pins it.
func TestOldTokenInsideTheGraceCanListRevokeAndSignOutButNotRotate(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	old := f.issue(credsA(), "Pixel")
	other := f.issue(credsA(), "Tablet")
	f.clock.Add(session.DefaultDeviceMinRotateInterval + 1)

	w := f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n1"}, bearerOf(old.Token))
	wantStatus(t, w, http.StatusOK)
	next := decodeToken(t, w)
	if got, _ := f.list(bearerOf(next.Token)); got.Code != http.StatusOK {
		t.Fatalf("the new token = %d", got.Code)
	}

	// Rotation with the old token is refused and issues nothing.
	w = f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n2"}, bearerOf(old.Token))
	wantStatus(t, w, http.StatusConflict)
	if strings.Contains(w.Body.String(), `"token"`) {
		t.Error("a refused rotation carries a token")
	}
	// It lists, revokes another device and signs the device out.
	if got, devices := f.list(bearerOf(old.Token)); got.Code != http.StatusOK || len(devices.Devices) != 2 {
		t.Fatalf("list with the old token = %d, %+v", got.Code, devices.Devices)
	}
	w = f.do(http.MethodDelete, "/auth/devices/"+other.TokenID, nil, bearerOf(old.Token))
	wantStatus(t, w, http.StatusNoContent)
	w = f.do(http.MethodDelete, "/auth/token", nil, bearerOf(old.Token))
	wantStatus(t, w, http.StatusNoContent)
	// The new token is out with it, and no reuse was logged.
	if got, _ := f.list(bearerOf(next.Token)); got.Code != http.StatusUnauthorized {
		t.Errorf("the new token after the sign out = %d, want 401", got.Code)
	}
	if strings.Contains(f.logs.String(), "reused") {
		t.Error("a reuse was logged for a token inside its grace")
	}
}
