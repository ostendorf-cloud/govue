// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package bearer classifies a request by its Authorization header. It is the
// one predicate that session and csrf both import, so the two cannot disagree
// on which request is a bearer request. ADR 0001 records the rules.
//
// A bearer request skips both halves of the CSRF check and authenticates by
// its token only, never by a cookie. A drift between two copies of this
// predicate would let a request skip the CSRF check and still ride a cookie,
// which is why there is exactly one.
package bearer

import (
	"net/http"
	"strings"
)

// Kind is what a request carries in its Authorization header.
type Kind int

const (
	// None is a request without an Authorization header, or with one whose
	// scheme is not Bearer. It is a cookie request: both CSRF halves apply.
	None Kind = iota
	// Bearer is exactly one Authorization value with the Bearer scheme. The
	// token may be empty or malformed. It then fails, it never falls back.
	Bearer
	// Malformed is more than one Authorization value. It is handled exactly
	// like a bearer request whose token fails.
	Malformed
)

// String names the kind for a log line.
func (k Kind) String() string {
	switch k {
	case None:
		return "none"
	case Bearer:
		return "bearer"
	case Malformed:
		return "malformed"
	}
	return "unknown"
}

// header is the one header this predicate reads. It is the canonical key,
// the one net/http's server and its accessors use.
const header = "Authorization"

// scheme is compared in ASCII only, case insensitively (RFC 9110 section
// 11.1). Unicode folding would let a non ASCII character stand in for a
// letter of the scheme.
const scheme = "bearer"

// Classify returns the kind of a request's Authorization header and, for
// Bearer, the token as sent.
//
// Every value of the header counts, so two headers are Malformed whatever
// their schemes. A single value is split at its first space: the part before
// it is the scheme, and the token is the rest after any further spaces, since
// RFC 9110 section 11.4 separates the two by one or more spaces. Nothing else
// is trimmed or checked here. The token's own syntax is the caller's check,
// and a token that fails it is still a bearer request.
//
// It reads the header only. A query parameter or a form field never makes a
// request a bearer request (RFC 6750 access_token is not supported).
func Classify(h http.Header) (Kind, string) {
	values := h.Values(header)
	switch len(values) {
	case 0:
		return None, ""
	case 1:
	default:
		return Malformed, ""
	}
	name, rest, _ := strings.Cut(values[0], " ")
	if !asciiEqualFold(name, scheme) {
		return None, ""
	}
	return Bearer, strings.TrimLeft(rest, " ")
}

// asciiEqualFold reports whether s equals the lower case ASCII word lower,
// ignoring the case of ASCII letters only.
func asciiEqualFold(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}
