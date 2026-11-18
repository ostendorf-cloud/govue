// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ostendorf-cloud/govue/internal/bearer"
)

// A request is a bearer request when it carries one Authorization header with
// the Bearer scheme, and a malformed one when it carries more than one. Both
// authenticate by the device token only and never by the cookie: on them this
// package does not read the session cookie or the login cookie at all, and
// does not clear either. ADR 0001 records why. The csrf package skips both of
// its checks for the same requests, through the same predicate, and that is
// only safe because nothing here falls back to the cookie.

// Credential says how a request authenticated.
type Credential int

const (
	// CredentialNone is no session. It is the zero value, so a Session
	// nobody marked never counts as authenticated by anything.
	CredentialNone Credential = iota
	// CredentialCookie is the session cookie of a browser.
	CredentialCookie
	// CredentialDevice is the bearer token of a native app's device session.
	CredentialDevice
)

// String names the credential for a log line.
func (c Credential) String() string {
	switch c {
	case CredentialNone:
		return "none"
	case CredentialCookie:
		return "cookie"
	case CredentialDevice:
		return "device"
	}
	return "unknown"
}

// readResult is the whole outcome of one read, the bearer refusal included,
// which [Manager.Read] does not report and [Manager.Middleware] answers.
type readResult struct {
	session Session
	ok      bool
	err     error
	// kind is the request's classification.
	kind bearer.Kind
	// refused is a bearer or malformed request whose token does not
	// authenticate: the one 401 of ADR 0001.
	refused bool
}

// read classifies the request once and reads the one credential it decides.
func (m *Manager) read(ctx context.Context, r *http.Request) readResult {
	kind, token := bearer.Classify(r.Header)
	switch kind {
	case bearer.None:
		s, ok, err := m.readCookie(ctx, r)
		return readResult{session: s, ok: ok, err: err, kind: kind}
	case bearer.Bearer:
		s, err := m.readDevice(ctx, token)
		if errors.Is(err, ErrDeviceToken) {
			return readResult{kind: kind, refused: true}
		}
		if err != nil {
			return readResult{err: err, kind: kind}
		}
		return readResult{session: s, ok: true, kind: kind}
	}
	// Malformed, and any value a later change might add, fails closed.
	return readResult{kind: kind, refused: true}
}

// readDevice authenticates a bearer token. Without a device store no token
// authenticates, which is the same [ErrDeviceToken] as an unknown one.
func (m *Manager) readDevice(ctx context.Context, token string) (Session, error) {
	if m.cfg.Devices.Store == nil {
		return Session{}, ErrDeviceToken
	}
	d, err := m.AuthenticateDevice(ctx, token)
	if err != nil {
		return Session{}, err
	}
	return Session{
		Subject:    d.Subject,
		DeviceID:   d.ID,
		Credential: CredentialDevice,
		IssuedAt:   d.CreatedAt,
		LastSeen:   d.LastSeen,
		ExpiresAt:  d.ExpiresAt,
	}, nil
}

// endDevice signs out the device session a bearer request carries.
func (m *Manager) endDevice(ctx context.Context, kind bearer.Kind, token string) error {
	if kind != bearer.Bearer || m.cfg.Devices.Store == nil {
		return fmt.Errorf("session: end: %w", ErrDeviceToken)
	}
	d, err := m.AuthenticateDevice(ctx, token)
	if err != nil {
		return fmt.Errorf("session: end: %w", err)
	}
	// A device revoked concurrently is signed out either way.
	if err := m.RevokeDevice(ctx, d.Subject, d.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("session: end: %w", err)
	}
	return nil
}

// refuseBearer answers a bearer request whose token does not authenticate.
// The headers are set first, so a custom refusal cannot forget them.
func (m *Manager) refuseBearer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	if m.cfg.OnBearerRefusal != nil {
		m.cfg.OnBearerRefusal(w, r)
		return
	}
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// noStore keeps a response to a bearer request out of every cache, as RFC
// 6749 section 5.1 does for a token response.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
