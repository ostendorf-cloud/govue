// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package csrf refuses a state changing request that did not come from this
// site: the browser's own origin signals plus a token the page carries.
//
// Two independent checks, because neither covers the whole range on its own.
// The origin half is net/http's CrossOriginProtection, which reads
// Sec-Fetch-Site and falls back to comparing Origin with Host; it allows a
// request that carries neither header, which is every non browser client and
// a handful of old browsers, so the token covers the rest. The token half
// binds a request to the session it claims to act for, which also catches a
// same site attacker, a subdomain the origin check cannot tell apart from the
// application itself.
//
// The token is derived from the session id with an HMAC rather than stored, so
// a session needs nothing kept alongside it and a token minted for one session
// is worthless in another. A caller supplies the expected token through an
// Expected function, which is why this package never imports a session
// package: it only ever sees a session id as a string.
package csrf

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
)

// HeaderName is where a fetch sends the token; FieldName is where a form does.
const (
	HeaderName = "X-CSRF-Token"
	FieldName  = "csrf"
)

// domain separates this derivation from any other use of the same signing key,
// so a token can never collide with a value another package derives.
const domain = "web-kit/csrf\x00v1\x00"

// ErrNoToken and ErrBadToken say which half refused, for the log line. They
// are for the application, not for the client: what a refused request is
// answered with carries no cause.
var (
	ErrNoToken  = errors.New("csrf: no token in the request")
	ErrBadToken = errors.New("csrf: token does not match the session")
)

// Token derives the token of a session from the signing key. The key is the
// application's secret and never leaves the server; the token does, so it is
// derived and not the key itself.
//
// The derivation is HMAC-SHA-256 over the domain separator, the length of the
// session id and the session id, base64 encoded. Length prefixing keeps the
// input unambiguous: no two session ids can produce the same byte string and
// therefore the same token.
//
// The token is stable for the lifetime of a session id. OWASP's HMAC pattern
// mixes a random value in as well, which makes a token single use at the cost
// of a surface this package does not have: Expected hands back one expected
// value for a request, so there is nothing to carry a per request random value
// in. Rotating the session id rotates the token with it, and the origin check
// is what stands in front of a leaked one.
func Token(key []byte, sessionID string) string {
	mac := hmac.New(sha256.New, key)
	// hash.Hash never returns an error from Write.
	_, _ = mac.Write([]byte(domain))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(sessionID)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Equal reports whether two tokens are the same, in constant time.
//
// Both sides are hashed first and the digests are compared with
// crypto/subtle. Comparing the tokens directly would be constant time in
// their contents but not in their length, because ConstantTimeCompare returns
// as soon as the two inputs differ in length; a digest is always 32 bytes, so
// the comparison tells an attacker nothing at all. An empty token is never
// equal to anything, including another empty token: an absent token is not a
// match.
func Equal(expected, given string) bool {
	if expected == "" || given == "" {
		return false
	}
	expectedSum := sha256.Sum256([]byte(expected))
	givenSum := sha256.Sum256([]byte(given))
	return subtle.ConstantTimeCompare(expectedSum[:], givenSum[:]) == 1
}

// FromRequest reads the token a request carries: the header first, so a fetch
// never has to touch the body, then the form field for a plain form submit.
//
// A JSON body is left alone and still readable by the handler, because parsing
// a form body only happens for a form content type. The query string is never
// read: a token there would end up in an access log, a proxy log and a
// Referer header.
func FromRequest(r *http.Request) string {
	if token := r.Header.Get(HeaderName); token != "" {
		return token
	}
	// PostFormValue parses the body for a urlencoded or multipart submit. For
	// any other content type, a JSON one included, it leaves the body unread.
	return r.PostFormValue(FieldName)
}

// Expected returns the token the request must carry, and false when there is
// no session to protect. It is how an application connects its session layer
// to this package without this package knowing about it: read the session of
// the request, derive its token with Token and return it.
type Expected func(*http.Request) (string, bool)

// Protector checks the origin signals and the token on every unsafe method.
type Protector struct {
	cross    *http.CrossOriginProtection
	expected Expected
	respond  func(http.ResponseWriter, *http.Request, error)
}

// New returns a Protector that trusts its own origin only and answers a
// refused request with 403 and no detail. It returns an error when expected
// is nil, which would otherwise panic on the first unsafe request.
//
// TrustOrigin and OnRefusal change that, and both are meant to be called
// during setup: a Protector is safe for concurrent use once it is serving, not
// while it is being configured.
func New(expected Expected) (*Protector, error) {
	if expected == nil {
		return nil, errors.New("csrf: expected is required")
	}
	return &Protector{
		cross:    http.NewCrossOriginProtection(),
		expected: expected,
		respond: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "cross origin or stale request, please reload", http.StatusForbidden)
		},
	}, nil
}

// TrustOrigin allows a cross origin caller, for a frontend served from another
// host. An API reached from its own origin only needs none. It returns an
// error for anything that is not a scheme and host without a trailing slash,
// so a typo cannot silently widen what is trusted.
func (p *Protector) TrustOrigin(origin string) error {
	if err := p.cross.AddTrustedOrigin(origin); err != nil {
		return fmt.Errorf("csrf: trusted origin %q: %w", origin, err)
	}
	return nil
}

// OnRefusal replaces what a refused request is answered with, for an
// application that answers HTML or an API error shape instead of plain text.
// The error says which check refused and belongs in the log, never in the
// response body.
func (p *Protector) OnRefusal(respond func(w http.ResponseWriter, r *http.Request, err error)) {
	p.respond = respond
}

// Middleware refuses an unsafe request that fails either check. Safe methods
// pass, which is why a GET must never change state.
func (p *Protector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := p.Check(r); err != nil {
			p.respond(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Check reports why a request is refused, or nil when it is accepted. The
// origin signals are checked first, then the token, so a cross origin request
// is refused whatever token it carries.
func (p *Protector) Check(r *http.Request) error {
	if err := p.cross.Check(r); err != nil {
		return fmt.Errorf("csrf: cross origin check: %w", err)
	}
	if safe(r.Method) {
		return nil
	}
	expected, ok := p.expected(r)
	if !ok {
		// Nothing to forge: without a session the request carries no authority
		// an attacker could borrow.
		return nil
	}
	given := FromRequest(r)
	if given == "" {
		return ErrNoToken
	}
	if !Equal(expected, given) {
		return ErrBadToken
	}
	return nil
}

// safe reports whether a method is one that must not change state, and
// therefore needs no token.
func safe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
