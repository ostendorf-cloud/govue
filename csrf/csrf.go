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
//
// A native app's bearer request can be exempted with [Protector.ExemptBearer].
// It is off by default and safe only in front of session.Middleware, which
// never reads a cookie on such a request. ADR 0001 records why.
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
	"strings"

	"github.com/ostendorf-cloud/govue/internal/bearer"
)

// HeaderName is where a fetch sends the token; FieldName is where a form does.
const (
	HeaderName = "X-CSRF-Token"
	FieldName  = "csrf"
)

// domain separates this derivation from any other use of the same signing key,
// so a token can never collide with a value another package derives.
const domain = "govue/csrf\x00v1\x00"

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
	cross *http.CrossOriginProtection
	// routes maps "METHOD path" to the check of a route with its own list of
	// trusted origins, see TrustOriginOnRoute.
	routes       map[string]*http.CrossOriginProtection
	expected     Expected
	respond      func(http.ResponseWriter, *http.Request, error)
	exemptBearer bool
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
//
// The error names the expected form and never the refused origin, and it does
// not wrap the error of the standard library, which quotes it: a startup
// error reaches CI and startup logs, and a value pasted wrongly can hold a
// credential. The caller names the variable or the position.
func (p *Protector) TrustOrigin(origin string) error {
	if p.cross.AddTrustedOrigin(origin) != nil {
		return errors.New("csrf: trusted origin " + originForm)
	}
	return nil
}

// originForm is the expected form of a trusted origin, for an error.
const originForm = "must be scheme://host[:port] without a path, a query or a fragment (the value is not shown)"

// TrustOriginOnRoute trusts the given origins for requests with exactly this
// method and exactly this path, and for no other route. It is for the one
// endpoint a native app calls from its own origin without a bearer token: the
// issue of a device token. Every other origin keeps the origin half of the
// check on that route, and so does every other route. The origins of
// TrustOrigin are not added on that route: its list is the one given here. The token half stays for
// all of them: it passes for a caller with no session, and a request that
// carries a session cookie still needs the CSRF token.
//
// It takes the origins, and not a switch for every origin, so that exempting
// a route that sets a cookie without a session, such as a login, cannot open
// it to login CSRF from any site. The endpoint should still check the origin
// itself, as devicehttp.Handlers.Issue does with IssueTrustedOrigins, as a
// second layer. It is safe only for an endpoint that carries no ambient
// credential and changes no browser state.
//
// method is POST, PUT, PATCH or DELETE: a safe method needs no origin check,
// so it is an error here and not a silent no-op, and CONNECT and TRACE are
// never a route of an API. path is an absolute path without a host, a query, a
// wildcard, a wildcard variable, "{$}", a trailing slash, "..", "//", "/." or
// "%", so it is exact and clean. It is compared byte for byte, and case
// sensitive, with the escaped path of the request. A neighbouring path, a path
// with a trailing slash, another case and another escaping of the same path,
// such as "%2F" for a slash or "%74" for a letter, keep the origin check.
// ServeMux may route such a spelling to the same handler: the app request then
// gets the refusal of the protector, which fails closed. Each origin follows the rules of TrustOrigin, at least one is
// required, and it is compared byte for byte with the Origin header. A second
// call for the same route is an error. It is meant to be called during setup
// like TrustOrigin.
func (p *Protector) TrustOriginOnRoute(method, path string, origins []string) error {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("csrf: trust origin on route: method %q must be POST, PUT, PATCH or DELETE", method)
	}
	if !strings.HasPrefix(path, "/") || path == "/" || strings.HasSuffix(path, "/") ||
		strings.ContainsAny(path, "{}*? #%\t\r\n") || strings.Contains(path, "..") ||
		strings.Contains(path, "//") || strings.Contains(path, "/.") {
		return fmt.Errorf("csrf: trust origin on route: path %q must be an absolute, exact and clean path without "+
			"a wildcard, a query, an escape or a trailing slash", path)
	}
	if len(origins) == 0 {
		return errors.New("csrf: trust origin on route: at least one origin is required")
	}
	key := method + " " + path
	if _, ok := p.routes[key]; ok {
		return fmt.Errorf("csrf: trust origin on route: %s is already set", key)
	}
	cross := http.NewCrossOriginProtection()
	for i, origin := range origins {
		if cross.AddTrustedOrigin(origin) != nil {
			return fmt.Errorf("csrf: trust origin on route: %s: origin %d %s", key, i+1, originForm)
		}
	}
	if p.routes == nil {
		p.routes = make(map[string]*http.CrossOriginProtection)
	}
	p.routes[key] = cross

	return nil
}

// ExemptBearer skips both halves of the check for a request with an
// Authorization header of the Bearer scheme, or with more than one
// Authorization header, and Expected is not called for it. It is off by
// default, and is meant to be called during setup like TrustOrigin.
//
// It is safe only when nothing behind the protector authenticates such a
// request by a cookie. Enable it only in front of session.Middleware, which
// never reads the cookie on such a request, or in front of an equivalent
// that keeps the same rule. A handler that reads a cookie of its own behind
// this exemption is open to cross site requests from any client that can
// add the header, such as a proxy that injects a bearer token.
//
// The classification is the module's one shared predicate, the same one the
// session package uses.
func (p *Protector) ExemptBearer() { p.exemptBearer = true }

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
//
// With [Protector.ExemptBearer], a bearer or malformed request skips both
// halves. A browser never attaches such a header by itself, and a cross site
// page cannot add one without a CORS preflight. Without it, every request
// keeps both halves.
func (p *Protector) Check(r *http.Request) error {
	if p.exemptBearer {
		if kind, _ := bearer.Classify(r.Header); kind != bearer.None {
			return nil
		}
	}
	cross := p.cross
	if route, ok := p.routes[r.Method+" "+r.URL.EscapedPath()]; ok {
		cross = route
	}
	if err := cross.Check(r); err != nil {
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
