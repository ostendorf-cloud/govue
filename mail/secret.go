// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Secret holds the SMTP password in a closure, so no fmt verb, encoder, slog
// handler or reflection walk prints it, also through an unexported field.
// Call Reveal only where the value is used, never to print or log it.
//
// reflect.DeepEqual of two Secrets is false unless both are zero, because
// DeepEqual compares funcs as equal only when both are nil. Compare Reveal
// where a test needs to.
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

// Reveal returns the secret.
func (s Secret) Reveal() string {
	if s.value == nil {
		return ""
	}
	return s.value()
}

// IsZero reports whether no secret is set.
func (s Secret) IsZero() bool { return s.Reveal() == "" }

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
		return nil, fmt.Errorf("mail: marshal secret: %w", err)
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
