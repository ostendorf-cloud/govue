// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/ostendorf-cloud/govue/internal/bearer"
)

// A request is a bearer request when it carries one Authorization header with
// the Bearer scheme, and a malformed one when it carries more than one. On
// both this package never reads or clears the session cookie or the login
// cookie, whether a device store is configured or not. Behind
// [Manager.Middleware], [Manager.Start] and [Manager.Rotate] refuse to set
// the session cookie. [Codec.SetLoginCookie] cannot see the request, so it
// is the caller's to keep off a bearer request.
// With a device store they authenticate by the device token only. Without one
// they are anonymous, so a product's own gateway or JWT keeps working. ADR
// 0001 records why. csrf.Protector.ExemptBearer skips the CSRF check for the
// same requests, through the same predicate, and that is only safe because
// nothing here falls back to the cookie.

// ErrBearerRequest is [Manager.Start] or [Manager.Rotate] called for a
// request that [Manager.Middleware] classified as a bearer or malformed
// request. Such a request never gets a session cookie. Without the middleware
// in front, neither method can tell.
var ErrBearerRequest = errors.New("session: a bearer request gets no session cookie")

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

// The two WWW-Authenticate challenges of RFC 6750 section 3.1: the bare one
// for a request without a token, and invalid_token for a token that fails.
const (
	challengeNoToken = "Bearer"
	challengeInvalid = `Bearer error="invalid_token"`
)

// readResult is the whole outcome of one read, the bearer refusal included,
// which [Manager.Read] does not report and [Manager.Middleware] answers.
type readResult struct {
	session Session
	ok      bool
	err     error
	// kind is the request's classification.
	kind bearer.Kind
	// refused is a bearer or malformed request whose token does not
	// authenticate against a configured device store: the one 401 of ADR
	// 0001.
	refused bool
	// noToken is a refused bearer request that carried no token at all.
	noToken bool
}

// read classifies the request once and reads the one credential it decides.
func (m *Manager) read(ctx context.Context, r *http.Request) readResult {
	kind, token := bearer.Classify(r.Header)
	if kind == bearer.None {
		s, ok, err := m.readCookie(ctx, r)
		return readResult{session: s, ok: ok, err: err, kind: kind}
	}
	if m.cfg.Devices.Store == nil {
		// Anonymous: the header is somebody else's, and the cookie is
		// still not read.
		return readResult{kind: kind}
	}
	if kind != bearer.Bearer {
		// Malformed, and any value a later change might add, fails closed.
		return readResult{kind: kind, refused: true}
	}
	s, err := m.readDevice(ctx, token)
	if errors.Is(err, ErrDeviceToken) {
		return readResult{kind: kind, refused: true, noToken: token == ""}
	}
	if err != nil {
		return readResult{err: err, kind: kind}
	}
	return readResult{session: s, ok: true, kind: kind}
}

// readDevice authenticates a bearer token against the device store.
func (m *Manager) readDevice(ctx context.Context, token string) (Session, error) {
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
	if m.cfg.Devices.Store == nil {
		return fmt.Errorf("session: end: %w", ErrDevicesDisabled)
	}
	if kind != bearer.Bearer {
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
// The headers are set before every write, so a custom refusal cannot drop
// them or set a cookie, and a refusal that writes nothing or another status
// still ends as a 401.
func (m *Manager) refuseBearer(w http.ResponseWriter, r *http.Request, noToken bool) {
	challenge := challengeInvalid
	if noToken {
		challenge = challengeNoToken
	}
	headers := func(h http.Header) {
		h.Set("WWW-Authenticate", challenge)
		h.Del("Set-Cookie")
		noStore(h)
	}
	headers(w.Header())
	if m.cfg.OnBearerRefusal != nil {
		guarded := &guardWriter{ResponseWriter: w, before: headers, status: http.StatusUnauthorized}
		m.cfg.OnBearerRefusal(guarded, r)
		if guarded.wrote {
			return
		}
		headers(w.Header())
	}
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// noStore keeps a response out of every cache, as RFC 6749 section 5.1 does
// for a token response. A non canonical spelling of the key is removed
// first, so exactly one Cache-Control field is sent. CDN-Cache-Control and
// Surrogate-Control are not touched: a product behind a CDN that reads them
// must not set them on a response to a bearer request.
func noStore(h http.Header) {
	for k := range h {
		if k != "Cache-Control" && strings.EqualFold(k, "Cache-Control") {
			delete(h, k)
		}
	}
	h.Set("Cache-Control", "no-store")
}

// guardWriter applies before to the headers on every status it writes, so a
// handler cannot override them, and replaces a final status with status when
// that is set.
//
// It keeps the interfaces of net/http's own writer: a type assertion for
// [http.Flusher], [http.Hijacker] or [io.ReaderFrom] works behind it, and
// [http.ResponseController] reaches the writer underneath through Unwrap.
// Two ways leave the guarantee on purpose and belong to deliberate code
// only: a hijacked connection carries whatever its handler writes, and a
// write through Unwrap bypasses the guard.
type guardWriter struct {
	http.ResponseWriter
	before func(http.Header)
	status int
	wrote  bool
}

// WriteHeader applies the headers and the forced status before it writes.
func (w *guardWriter) WriteHeader(code int) {
	w.before(w.Header())
	if code >= http.StatusOK {
		if w.wrote {
			return
		}
		w.wrote = true
		if w.status != 0 {
			code = w.status
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write writes the status first when nothing has, as net/http does.
func (w *guardWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// FlushError flushes through [http.ResponseController], after the headers
// are final.
func (w *guardWriter) FlushError() error {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Flush is [guardWriter.FlushError] for code that asserts [http.Flusher].
// A writer underneath that cannot flush makes it a no-op, as net/http does.
func (w *guardWriter) Flush() { _ = w.FlushError() }

// Hijack hands the connection to the handler, through the writer underneath.
// It is [http.ErrNotSupported] when that writer cannot hijack. No header is
// applied: after a hijack the header map is never sent, and whatever the
// handler writes on the connection is outside the no-store guarantee.
func (w *guardWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// ReadFrom writes the status first when nothing has, then copies through the
// writer underneath, with its own ReadFrom when it has one.
func (w *guardWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	// The writer underneath has no ReadFrom, so io.Copy uses its Write.
	return io.Copy(w.ResponseWriter, r)
}

// Unwrap lets [http.ResponseController] reach the writer underneath.
func (w *guardWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
