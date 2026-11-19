// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultDeviceLifetime is how long a known device cookie lasts when
	// [KnownDevicesConfig.Lifetime] is zero.
	DefaultDeviceLifetime = 90 * 24 * time.Hour

	// MaxDeviceLifetime is the longest lifetime accepted. Browsers cap a
	// cookie at 400 days anyway.
	MaxDeviceLifetime = 400 * 24 * time.Hour

	// MinDeviceKeyLength is the shortest HMAC key accepted, in bytes.
	MinDeviceKeyLength = 32

	// DeviceCookieName is the name of the known device cookie when
	// [KnownDevicesConfig.AllowPlainHTTP] is set.
	DeviceCookieName = "govue_device"

	// DeviceCookieNameSecure is its name in the default, Secure form.
	DeviceCookieNameSecure = "__Host-" + DeviceCookieName

	deviceIDBytes   = 16
	deviceMaxToken  = 256
	deviceTokenTag  = "v1"
	deviceMACDomain = "govue/account/known-device/v1"
)

// KnownDevicesConfig configures [NewKnownDevices].
type KnownDevicesConfig struct {
	// Key is the HMAC key that signs the cookie, at least
	// [MinDeviceKeyLength] bytes from a secret store or a CSPRNG. Required.
	// It has to be the same on every replica and survive a restart, or every
	// device is unknown again. Changing it makes every device unknown at once.
	// Use a dedicated key, not the session key or the CSRF key.
	Key []byte

	// Lifetime is how long a device stays known after its last successful
	// sign in. Zero is [DefaultDeviceLifetime]. At most [MaxDeviceLifetime].
	Lifetime time.Duration

	// AllowPlainHTTP drops the Secure attribute and the __Host- prefix, for
	// development on a loopback address. Never set it in production.
	AllowPlainHTTP bool

	// Now is the clock. Nil is time.Now. Tests set it.
	Now func() time.Time
}

// KnownDevices recognises a browser that signed in to an account before, so
// that the per login ceiling of a consumer does not refuse the owner while a
// distributed attacker holds that ceiling dry. It is optional: nothing sets
// or reads the cookie unless it is given to [HandlerConfig.KnownDevices].
//
// The cookie is signed, HttpOnly, SameSite=Lax and Path=/, Secure with the
// __Host- prefix unless [KnownDevicesConfig.AllowPlainHTTP] is set, and has
// no Domain. It holds a version tag, a random 128 bit device id, an expiry
// and an HMAC-SHA-256 over those and two values the cookie does not carry: the
// account id and the account's password hash. Neither the id, the login nor
// the hash can be read from it, and a cookie from another account, with a
// changed expiry or any changed byte does not verify.
//
// Because the hash is in the signature, a password change makes every device
// of the account unknown again with no store method and no server side list.
// So does the rehash a login does when the hash parameters are outdated: the
// next successful login sets a fresh cookie. A cookie cannot be revoked
// without one of those changes: there is no per device list. Rotating the
// key revokes every device of every account.
//
// What a known device gains is the skip of the ceiling only. The password,
// the TOTP code and the per client limits still apply. A stolen cookie alone
// logs nobody in.
//
// One browser holds one cookie, so a second account signed in on the same
// browser replaces the known status of the first.
//
// It is safe for concurrent use.
type KnownDevices struct {
	key      []byte
	lifetime time.Duration
	secure   bool
	name     string
	now      func() time.Time
}

// NewKnownDevices validates cfg and returns the helper.
func NewKnownDevices(cfg KnownDevicesConfig) (*KnownDevices, error) {
	var problems []error
	if len(cfg.Key) < MinDeviceKeyLength {
		problems = append(problems, fmt.Errorf("key has %d bytes, need at least %d", len(cfg.Key), MinDeviceKeyLength))
	}
	if cfg.Lifetime < 0 || cfg.Lifetime > MaxDeviceLifetime {
		problems = append(problems, fmt.Errorf("lifetime %s is outside 0 to %s", cfg.Lifetime, MaxDeviceLifetime))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("account known devices: %w", err)
	}

	k := &KnownDevices{
		key:      append([]byte(nil), cfg.Key...),
		lifetime: cfg.Lifetime,
		secure:   !cfg.AllowPlainHTTP,
		name:     DeviceCookieNameSecure,
		now:      cfg.Now,
	}
	if k.lifetime == 0 {
		k.lifetime = DefaultDeviceLifetime
	}
	if cfg.AllowPlainHTTP {
		k.name = DeviceCookieName
	}
	if k.now == nil {
		k.now = time.Now
	}

	return k, nil
}

// CookieName is the name of the cookie this helper sets.
func (k *KnownDevices) CookieName() string { return k.name }

// Known reports whether r carries a valid, unexpired device cookie for rec.
// A record without a password hash is never known.
func (k *KnownDevices) Known(r *http.Request, rec Record) bool {
	_, ok := k.verify(r, rec)

	return ok
}

// Remember sets the device cookie for rec on w, after a successful sign in.
// A device that is already known keeps its id, and the expiry starts again.
func (k *KnownDevices) Remember(w http.ResponseWriter, r *http.Request, rec Record) error {
	if rec.ID == "" || rec.PasswordHash == "" {
		return errors.New("account known devices: record has no id or password hash")
	}

	id, ok := k.verify(r, rec)
	if !ok {
		raw := make([]byte, deviceIDBytes)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("account known devices: device id: %w", err)
		}
		id = base64.RawURLEncoding.EncodeToString(raw)
	}

	expires := k.now().Add(k.lifetime)
	http.SetCookie(w, &http.Cookie{
		Name:     k.name,
		Value:    k.token(rec, id, expires.Unix()),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(k.lifetime / time.Second),
		HttpOnly: true,
		Secure:   k.secure,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}

// Forget clears the device cookie on w, for a sign out.
func (k *KnownDevices) Forget(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     k.name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   k.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// token builds the cookie value.
func (k *KnownDevices) token(rec Record, id string, expires int64) string {
	exp := strconv.FormatInt(expires, 10)
	mac := k.sign(rec, id, exp)

	return strings.Join([]string{deviceTokenTag, id, exp, base64.RawURLEncoding.EncodeToString(mac)}, ".")
}

// verify returns the device id of the cookie of r when it is valid for rec.
func (k *KnownDevices) verify(r *http.Request, rec Record) (string, bool) {
	if rec.ID == "" || rec.PasswordHash == "" {
		return "", false
	}
	c, err := r.Cookie(k.name)
	if err != nil || len(c.Value) == 0 || len(c.Value) > deviceMaxToken {
		return "", false
	}

	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 || parts[0] != deviceTokenTag {
		return "", false
	}
	id, exp := parts[1], parts[2]
	mac, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	if raw, err := base64.RawURLEncoding.DecodeString(id); err != nil || len(raw) != deviceIDBytes {
		return "", false
	}

	// The signature first, so the expiry is read only from a value we signed.
	if !hmac.Equal(mac, k.sign(rec, id, exp)) {
		return "", false
	}
	expires, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || !k.now().Before(time.Unix(expires, 0)) {
		return "", false
	}

	return id, true
}

// sign is the HMAC over the device id, the expiry, the account id and the
// password hash. Every field is length prefixed, so no two inputs share an
// encoding.
func (k *KnownDevices) sign(rec Record, id, exp string) []byte {
	m := hmac.New(sha256.New, k.key)
	for _, field := range []string{deviceMACDomain, id, exp, rec.ID, rec.PasswordHash} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(field)))
		m.Write(n[:])
		m.Write([]byte(field))
	}

	return m.Sum(nil)
}
