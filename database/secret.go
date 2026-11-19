// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// redacted stands in for a set secret wherever one is printed.
const redacted = "[redacted]"

// Secret holds a password so that nothing prints it. Build one with
// NewSecret and read it with Reveal.
//
// The value is captured by a closure. fmt, log/slog's text handler and
// encoding/json print a struct field they cannot call a method on, such as a
// Config in an unexported field of a product's own config, by walking its
// fields. A dumper that follows pointers and ignores methods, such as the
// diff of a test assertion library, walks further. A string or a pointer to
// one would be printed by either. reflect cannot read the variables a
// closure captured, so a walker sees a func and prints at most its address.
// Where the methods run, a set Secret prints as [redacted] and an empty one
// as nothing. Copies share the value, which is never changed after
// NewSecret.
//
// The price: reflect.DeepEqual of two Secrets, or of two Configs holding
// one, is false unless both are zero, because DeepEqual compares funcs as
// equal only when both are nil. == does not compile for a func field either,
// so compare Reveal where a test needs to.
type Secret struct {
	value func() string
}

// NewSecret wraps s. An empty s gives the zero Secret.
func NewSecret(s string) Secret {
	if s == "" {
		return Secret{}
	}
	return Secret{value: func() string { return s }}
}

// Reveal returns the secret. Call it only where the value is used, never to
// print or log it.
func (s Secret) Reveal() string {
	if s.value == nil {
		return ""
	}
	return s.value()
}

// IsZero reports whether no secret is set.
func (s Secret) IsZero() bool {
	return s.Reveal() == ""
}

// String shows whether a secret is set, never what it is.
func (s Secret) String() string {
	if s.IsZero() {
		return ""
	}
	return redacted
}

// GoString keeps %#v from printing the value.
func (s Secret) GoString() string { return s.String() }

// Format makes every fmt verb print String.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, s.String())
}

// MarshalJSON writes String as a JSON string.
func (s Secret) MarshalJSON() ([]byte, error) {
	out, err := json.Marshal(s.String())
	if err != nil {
		return nil, fmt.Errorf("database: marshal secret: %w", err)
	}
	return out, nil
}

// MarshalText writes String.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// LogValue writes String.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(s.String())
}
