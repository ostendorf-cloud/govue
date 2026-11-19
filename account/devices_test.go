// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/api"
)

const (
	dOtherLogin    = "alice@example.com"
	dOtherPassword = "fake-handler-password-three"
)

var dKey = bytes.Repeat([]byte{7}, 32)

// dFixture is the handler fixture with the known device feature on and a
// ceiling that a test can run dry.
type dFixture struct {
	*hFixture

	dry      atomic.Bool
	ceilings atomic.Int64
	secrets  map[string]string
}

func newDFixture(t *testing.T, cfg account.KnownDevicesConfig) *dFixture {
	t.Helper()

	d := &dFixture{secrets: map[string]string{}}
	if cfg.Key == nil {
		cfg.Key = dKey
	}
	cfg.Now = func() time.Time { return d.now() }
	devices, err := account.NewKnownDevices(cfg)
	if err != nil {
		t.Fatalf("NewKnownDevices: %v", err)
	}
	d.hFixture = newHFixture(t, func(c *account.HandlerConfig) {
		c.KnownDevices = devices
		c.Ceiling = func(*http.Request, string) error {
			d.ceilings.Add(1)
			if d.dry.Load() {
				return api.TooManyRequests(time.Minute)
			}

			return nil
		}
	})
	d.secrets[hLogin] = d.register()

	return d
}

// login signs in with a fresh code and the given cookies.
func (d *dFixture) login(login, password string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	d.t.Helper()

	d.advance(time.Minute)
	code := d.code(d.secrets[login])

	return d.post(d.handlers.Login(), d.creds(login, password, code), func(r *http.Request) {
		for _, c := range cookies {
			r.AddCookie(c)
		}
	})
}

// deviceCookie is the cookie a response sets, or nil.
func deviceCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == account.DeviceCookieNameSecure || c.Name == account.DeviceCookieName {
			return c
		}
	}

	return nil
}

func (d *dFixture) knownCookie() *http.Cookie {
	d.t.Helper()

	w := d.login(hLogin, hPassword)
	if w.Code != http.StatusOK {
		d.t.Fatalf("login = %d %s", w.Code, w.Body.String())
	}
	c := deviceCookie(w)
	if c == nil {
		d.t.Fatal("login set no device cookie")
	}

	return c
}

func (d *dFixture) registerOther() {
	d.t.Helper()

	d.secrets[dOtherLogin] = d.signUp(dOtherLogin, dOtherPassword)
	w := d.post(d.handlers.ConfirmTOTP(), d.creds(dOtherLogin, dOtherPassword, d.code(d.secrets[dOtherLogin])))
	if w.Code != http.StatusOK {
		d.t.Fatalf("confirm = %d %s", w.Code, w.Body.String())
	}
}

func TestKnownDevicesValidate(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]account.KnownDevicesConfig{
		"no key":        {},
		"short key":     {Key: make([]byte, 31)},
		"negative life": {Key: dKey, Lifetime: -time.Hour},
		"too long life": {Key: dKey, Lifetime: account.MaxDeviceLifetime + time.Second},
	} {
		if _, err := account.NewKnownDevices(cfg); err == nil {
			t.Errorf("%s: NewKnownDevices succeeded, want an error", name)
		}
	}

	var cfg account.HandlerConfig
	_ = newHFixture(t, func(c *account.HandlerConfig) { cfg = *c })
	kd, err := account.NewKnownDevices(account.KnownDevicesConfig{Key: dKey})
	if err != nil {
		t.Fatal(err)
	}
	cfg.KnownDevices = kd
	if _, err := account.NewHandlers(cfg); err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Errorf("NewHandlers without ceiling = %v, want an error naming it", err)
	}
}

func TestKnownDeviceOffByDefault(t *testing.T) {
	t.Parallel()

	f := newHFixture(t, nil)
	secret := f.register()
	f.advance(time.Minute)

	w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret)))
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("cookies set with the feature off: %v", got)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("Set-Cookie header present with the feature off")
	}
}

func TestKnownDeviceCookieAttributes(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	c := d.knownCookie()

	if c.Name != "__Host-govue_device" {
		t.Errorf("name = %q", c.Name)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("attributes = %+v, want HttpOnly, Secure, Lax, Path=/, no Domain", c)
	}
	if want := int(account.DefaultDeviceLifetime / time.Second); c.MaxAge != want {
		t.Errorf("max age = %d, want %d", c.MaxAge, want)
	}
	if !c.Expires.Equal(d.now().Add(account.DefaultDeviceLifetime).Truncate(time.Second).UTC()) {
		t.Errorf("expires = %v", c.Expires)
	}
	for _, secret := range []string{hLogin, hPassword, "argon2", "memory-1"} {
		if strings.Contains(c.Value, secret) {
			t.Errorf("cookie value holds %q", secret)
		}
	}
}

func TestKnownDeviceCookieOverPlainHTTPAndLifetime(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{AllowPlainHTTP: true, Lifetime: 24 * time.Hour})
	c := d.knownCookie()

	if c.Name != "govue_device" || c.Secure || !c.HttpOnly || c.MaxAge != 86400 {
		t.Errorf("cookie = %+v, want plain name, not Secure, HttpOnly, one day", c)
	}
}

func TestKnownDeviceIDIsRandom128Bit(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	seen := map[string]bool{}
	for range 5 {
		c := d.knownCookie()
		parts := strings.Split(c.Value, ".")
		if len(parts) != 4 {
			t.Fatalf("cookie %q has %d parts", c.Value, len(parts))
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil || len(raw) != 16 {
			t.Fatalf("device id %q is not 16 random bytes: %v", parts[1], err)
		}
		if seen[parts[1]] {
			t.Fatalf("device id %q repeated for a device without a cookie", parts[1])
		}
		seen[parts[1]] = true
	}
}

func TestKnownDeviceKeepsItsIDAndRenewsExpiry(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	first := d.knownCookie()
	w := d.login(hLogin, hPassword, first)
	second := deviceCookie(w)
	if second == nil {
		t.Fatal("no renewed cookie")
	}
	if strings.Split(first.Value, ".")[1] != strings.Split(second.Value, ".")[1] {
		t.Error("a known device got a new id")
	}
	if first.Value == second.Value {
		t.Error("expiry was not renewed")
	}
}

func TestKnownDeviceSkipsTheCeiling(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	d.dry.Store(true)
	before := d.ceilings.Load()

	if w := d.login(hLogin, hPassword, known); w.Code != http.StatusOK {
		t.Fatalf("known device after a dry ceiling = %d %s, want 200", w.Code, w.Body.String())
	}
	if got := d.ceilings.Load(); got != before {
		t.Fatalf("ceiling calls for a known device = %d, want none", got-before)
	}

	w := d.login(hLogin, hPassword)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("unknown device = %d, want 429", w.Code)
	}
	if got := d.ceilings.Load(); got != before+1 {
		t.Fatalf("ceiling calls for an unknown device = %d, want 1", got-before)
	}
	if deviceCookie(w) != nil {
		t.Error("a refused request set a device cookie")
	}
}

func TestKnownDeviceStillNeedsTheCredentials(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	d.dry.Store(true)

	w := d.login(hLogin, hWrong, known)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password from a known device = %d, want 401", w.Code)
	}
	if deviceCookie(w) != nil {
		t.Error("a failed login set a device cookie")
	}
}

func TestKnownDeviceCookieThatDoesNotVerifyIsUnknown(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	d.registerOther()
	good := d.knownCookie()
	other := deviceCookie(d.login(dOtherLogin, dOtherPassword))
	if other == nil {
		t.Fatal("no cookie for the other account")
	}
	parts := strings.Split(good.Value, ".")

	flip := func(s string) string {
		b := []byte(s)
		if b[0] == 'A' {
			b[0] = 'B'
		} else {
			b[0] = 'A'
		}

		return string(b)
	}
	cookie := func(v string) *http.Cookie { return &http.Cookie{Name: good.Name, Value: v} }

	tests := map[string]*http.Cookie{
		"other account":    other,
		"tampered mac":     cookie(strings.Join([]string{parts[0], parts[1], parts[2], flip(parts[3])}, ".")),
		"tampered id":      cookie(strings.Join([]string{parts[0], flip(parts[1]), parts[2], parts[3]}, ".")),
		"extended expiry":  cookie(strings.Join([]string{parts[0], parts[1], "99999999999", parts[3]}, ".")),
		"garbage":          cookie("garbage"),
		"empty":            cookie(""),
		"too long":         cookie(strings.Repeat("a", 5000)),
		"wrong version":    cookie(strings.Join([]string{"v2", parts[1], parts[2], parts[3]}, ".")),
		"short device id":  cookie(strings.Join([]string{parts[0], "AAAA", parts[2], parts[3]}, ".")),
		"mac not base64":   cookie(strings.Join([]string{parts[0], parts[1], parts[2], "!!"}, ".")),
		"missing mac part": cookie(strings.Join(parts[:3], ".")),
	}
	d.dry.Store(true)

	for name, c := range tests {
		if w := d.login(hLogin, hPassword, c); w.Code != http.StatusTooManyRequests {
			t.Errorf("%s: login = %d, want 429 (unknown device)", name, w.Code)
		}
	}

	// The good cookie still works after all that.
	if w := d.login(hLogin, hPassword, good); w.Code != http.StatusOK {
		t.Errorf("good cookie = %d, want 200", w.Code)
	}
}

func TestKnownDeviceCookieForAnUnknownLoginIsUnknown(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	d.dry.Store(true)

	w := d.post(d.handlers.Login(), d.creds("nobody@example.com", hPassword, "123456"), func(r *http.Request) { r.AddCookie(known) })
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("unknown login with a cookie = %d, want 429", w.Code)
	}
}

func TestKnownDeviceExpires(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{Lifetime: 48 * time.Hour})
	known := d.knownCookie()
	d.dry.Store(true)

	d.advance(47 * time.Hour)
	if w := d.login(hLogin, hPassword, known); w.Code != http.StatusOK {
		t.Fatalf("before the expiry = %d, want 200", w.Code)
	}

	d.advance(2 * time.Hour)
	if w := d.login(hLogin, hPassword, known); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after the expiry = %d, want 429", w.Code)
	}
}

func TestKnownDeviceAnotherKeyIsUnknown(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()

	// A second helper with another key, over the same store, never trusts it.
	other, err := account.NewKnownDevices(account.KnownDevicesConfig{Key: bytes.Repeat([]byte{9}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := d.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	r.AddCookie(known)
	if other.Known(r, rec) {
		t.Fatal("a cookie signed with another key was accepted")
	}
}

func TestKnownDeviceIsBoundToTheAccountID(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	rec, err := d.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatal(err)
	}
	// Another account that happens to hold the same password hash.
	twin, err := d.store.Create(t.Context(), account.Record{Login: dOtherLogin, PasswordHash: rec.PasswordHash})
	if err != nil {
		t.Fatal(err)
	}

	kd, err := account.NewKnownDevices(account.KnownDevicesConfig{Key: dKey, Now: d.now})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	r.AddCookie(known)
	if !kd.Known(r, rec) {
		t.Fatal("the cookie is not known for its own account")
	}
	if kd.Known(r, twin) {
		t.Fatal("a cookie of one account was accepted for another with the same hash")
	}
}

func TestKnownDeviceLogoutClearsTheCookie(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()

	w := d.post(d.handlers.Logout(), "", func(r *http.Request) { r.AddCookie(known) })
	c := deviceCookie(w)
	if w.Code != http.StatusNoContent || c == nil {
		t.Fatalf("logout = %d, cookie %v", w.Code, c)
	}
	if c.MaxAge >= 0 || c.Value != "" || c.Path != "/" || !c.Secure || !c.HttpOnly {
		t.Errorf("clearing cookie = %+v, want an expired, empty, Secure, HttpOnly Path=/ cookie", c)
	}
}

func TestKnownDevicePasswordChangeInvalidates(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	rec, err := d.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatal(err)
	}
	d.setCurrent(account.Identity{ID: rec.ID, Login: hLogin}, true)

	w := d.post(d.handlers.ChangePassword(),
		asJSON(t, map[string]string{"current_password": hPassword, "new_password": hNewPassword}),
		func(r *http.Request) { r.AddCookie(known) })
	if w.Code != http.StatusOK {
		t.Fatalf("change = %d %s", w.Code, w.Body.String())
	}

	// The changing device gets a fresh cookie bound to the new hash.
	fresh := deviceCookie(w)
	if fresh == nil {
		t.Fatal("password change set no device cookie")
	}
	if fresh.Name != known.Name || fresh.Value == known.Value || fresh.Value == "" {
		t.Errorf("fresh cookie = %+v, want a new value under the same name", fresh)
	}
	if !fresh.HttpOnly || !fresh.Secure || fresh.SameSite != http.SameSiteLaxMode ||
		fresh.Path != "/" || fresh.Domain != "" {
		t.Errorf("fresh attributes = %+v, want HttpOnly, Secure, Lax, Path=/, no Domain", fresh)
	}
	if want := int(account.DefaultDeviceLifetime / time.Second); fresh.MaxAge != want {
		t.Errorf("fresh max age = %d, want %d", fresh.MaxAge, want)
	}

	// A browser that kept the old cookie is unknown now, even with the new
	// password. The changing device stays known.
	d.dry.Store(true)
	if w := d.login(hLogin, hNewPassword, known); w.Code != http.StatusTooManyRequests {
		t.Fatalf("old cookie after a password change = %d, want 429", w.Code)
	}
	if w := d.login(hLogin, hNewPassword, fresh); w.Code != http.StatusOK {
		t.Fatalf("fresh cookie after a password change = %d %s, want 200", w.Code, w.Body.String())
	}
}

func TestKnownDevicePasswordChangeWithoutEndedSessionsClearsTheCookie(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	rec, err := d.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatal(err)
	}
	d.setCurrent(account.Identity{ID: rec.ID, Login: hLogin}, true)
	d.endAllErr = errors.New("session store down")

	w := d.post(d.handlers.ChangePassword(),
		asJSON(t, map[string]string{"current_password": hPassword, "new_password": hNewPassword}),
		func(r *http.Request) { r.AddCookie(known) })
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sessions_ended":false`) {
		t.Fatalf("change = %d %s, want 200 with sessions_ended false", w.Code, w.Body.String())
	}
	c := deviceCookie(w)
	if c == nil || c.MaxAge >= 0 || c.Value != "" || c.Path != "/" || !c.Secure || !c.HttpOnly {
		t.Fatalf("cookie = %+v, want a clearing cookie", c)
	}
}

func TestKnownDeviceDoesNotSkipSignup(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	d.dry.Store(true)
	before := d.ceilings.Load()

	// A signup has no account yet, so a device cookie for an existing login
	// must not skip the ceiling.
	w := d.post(d.handlers.Signup(),
		asJSON(t, map[string]string{"login": hLogin, "password": hPassword}),
		func(r *http.Request) { r.AddCookie(known) })
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("signup with a valid device cookie = %d %s, want 429", w.Code, w.Body.String())
	}
	if got := d.ceilings.Load(); got != before+1 {
		t.Fatalf("ceiling calls = %d, want 1", got-before)
	}
}

// forge signs a device cookie value the way the package does, to build values
// that carry a valid MAC and break one other rule.
func forge(key []byte, rec account.Record, id, exp string) string {
	m := hmac.New(sha256.New, key)
	for _, field := range []string{"govue/account/known-device/v1", id, exp, rec.ID, rec.PasswordHash} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(field)))
		m.Write(n[:])
		m.Write([]byte(field))
	}

	return strings.Join([]string{"v1", id, exp, base64.RawURLEncoding.EncodeToString(m.Sum(nil))}, ".")
}

func TestKnownDeviceValueRules(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	rec, err := d.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := account.NewKnownDevices(account.KnownDevicesConfig{Key: dKey, Now: d.now})
	if err != nil {
		t.Fatal(err)
	}

	id := func(n int) string { return base64.RawURLEncoding.EncodeToString(make([]byte, n)) }
	exp := strconv.FormatInt(d.now().Add(time.Hour).Unix(), 10)
	known := func(rec account.Record, value string) bool {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: kd.CookieName(), Value: value})

		return kd.Known(r, rec)
	}

	good := forge(dKey, rec, id(16), exp)
	if !known(rec, good) {
		t.Fatal("a correctly signed cookie is not known")
	}

	// Non canonical base64 in the MAC decodes to the same bytes without Strict.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := good[len(good)-1]
	variant := good[:len(good)-1] + string(alphabet[strings.IndexByte(alphabet, last)|1])
	if variant == good {
		t.Fatal("test setup: the variant equals the cookie")
	}
	if known(rec, variant) {
		t.Error("a MAC with non canonical base64 was accepted")
	}

	// A signed value over 256 bytes: the expiry carries leading zeros.
	long := forge(dKey, rec, id(16), strings.Repeat("0", 300)+exp)
	if len(long) <= 256 {
		t.Fatalf("test setup: long cookie has %d bytes", len(long))
	}
	if known(rec, long) {
		t.Error("a signed cookie over 256 bytes was accepted")
	}
	if !known(rec, forge(dKey, rec, id(16), strings.Repeat("0", 100)+exp)) {
		t.Error("a signed cookie under the cap with leading zeros was refused")
	}

	// A signed value whose device id is not 16 bytes.
	for _, n := range []int{0, 8, 15, 17, 32} {
		if known(rec, forge(dKey, rec, id(n), exp)) {
			t.Errorf("a signed cookie with a %d byte device id was accepted", n)
		}
	}

	// A record without an id or a password hash is never known, even when
	// the cookie is signed for exactly that record.
	for name, empty := range map[string]account.Record{
		"no hash": {ID: rec.ID},
		"no id":   {PasswordHash: rec.PasswordHash},
		"neither": {},
	} {
		if known(empty, forge(dKey, empty, id(16), exp)) {
			t.Errorf("%s: a signed cookie for an incomplete record was accepted", name)
		}
	}
}

func TestKnownDeviceDisabledAccountIsUnknown(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	known := d.knownCookie()
	rec, err := d.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatal(err)
	}
	d.store.Disable(rec.ID)
	d.dry.Store(true)

	if w := d.login(hLogin, hPassword, known); w.Code != http.StatusTooManyRequests {
		t.Fatalf("disabled account = %d, want 429", w.Code)
	}
}

func TestKnownDeviceConfirmSetsTheCookie(t *testing.T) {
	t.Parallel()

	d := newDFixture(t, account.KnownDevicesConfig{})
	secret := d.signUp(dOtherLogin, dOtherPassword)
	w := d.post(d.handlers.ConfirmTOTP(), d.creds(dOtherLogin, dOtherPassword, d.code(secret)))
	if w.Code != http.StatusOK || deviceCookie(w) == nil {
		t.Fatalf("confirm = %d, cookie %v", w.Code, deviceCookie(w))
	}
}

func TestCeilingWithoutKnownDevices(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	f := newHFixture(t, func(c *account.HandlerConfig) {
		c.Ceiling = func(_ *http.Request, login string) error {
			calls.Add(1)
			if login != hLogin {
				return nil
			}

			return assertErr{}
		}
	})
	w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, "123456"))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "ceiling-detail") {
		t.Fatalf("a plain ceiling error = %d %s, want an opaque 500", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("ceiling calls = %d, want 1", calls.Load())
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "ceiling-detail" }
