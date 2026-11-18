// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package bearer_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/internal/bearer"
)

// fakeToken is shaped like a device token and is obviously not a real one.
const fakeToken = "example_AAAAexampleexampleexampleexampleexampleAAAA_x1y2z3"

func header(values ...string) http.Header {
	h := http.Header{}
	for _, v := range values {
		h.Add("Authorization", v)
	}
	return h
}

func TestClassify(t *testing.T) {
	t.Parallel()

	long := "Bearer " + strings.Repeat("A", 64*1024)

	for _, tc := range []struct {
		name      string
		header    http.Header
		wantKind  bearer.Kind
		wantToken string
	}{
		{"no header", http.Header{}, bearer.None, ""},
		{"a nil header", nil, bearer.None, ""},
		{"an unrelated header", http.Header{"X-Authorization": {"Bearer " + fakeToken}}, bearer.None, ""},
		{"bearer and a token", header("Bearer " + fakeToken), bearer.Bearer, fakeToken},
		{"lower case scheme", header("bearer " + fakeToken), bearer.Bearer, fakeToken},
		{"upper case scheme", header("BEARER " + fakeToken), bearer.Bearer, fakeToken},
		{"mixed case scheme", header("bEaReR " + fakeToken), bearer.Bearer, fakeToken},
		// RFC 9110 section 11.4 separates scheme and credentials by 1*SP.
		{"two spaces before the token", header("Bearer  " + fakeToken), bearer.Bearer, fakeToken},
		{"many spaces before the token", header("Bearer     x"), bearer.Bearer, "x"},
		{"bearer without a token", header("Bearer"), bearer.Bearer, ""},
		{"bearer and a space only", header("Bearer "), bearer.Bearer, ""},
		{"a trailing space is part of the token", header("Bearer x "), bearer.Bearer, "x "},
		{"a tab after the scheme is no separator", header("Bearer\tx"), bearer.None, ""},
		{"a scheme glued to the token", header("Bearerx"), bearer.None, ""},
		{"a longer scheme that starts with bearer", header("Bearers x"), bearer.None, ""},
		{"basic", header("Basic dXNlcjpwYXNz"), bearer.None, ""},
		{"basic with a bearer later in the value", header("Basic x, Bearer " + fakeToken), bearer.None, ""},
		{"an empty value", header(""), bearer.None, ""},
		{"a leading space before the scheme", header(" Bearer x"), bearer.None, ""},
		{"comma joined bearer values are one value", header("Bearer a, Bearer b"), bearer.Bearer, "a, Bearer b"},
		{"illegal characters in the token", header("Bearer a\"b<c>"), bearer.Bearer, "a\"b<c>"},
		{"a control character in the token", header("Bearer a\x00b"), bearer.Bearer, "a\x00b"},
		{"non ASCII in the token", header("Bearer tökén"), bearer.Bearer, "tökén"},
		// U+0435 is a Cyrillic e. Only ASCII folds, so this is another scheme.
		{"non ASCII in the scheme", header("Bеarer x"), bearer.None, ""},
		{"a non ASCII byte inside the scheme", header("Bear\xffer x"), bearer.None, ""},
		{"a very long token", header(long), bearer.Bearer, long[len("Bearer "):]},
		{"two bearer headers", header("Bearer a", "Bearer b"), bearer.Malformed, ""},
		{"two identical bearer headers", header("Bearer "+fakeToken, "Bearer "+fakeToken), bearer.Malformed, ""},
		{"bearer and basic", header("Bearer a", "Basic b"), bearer.Malformed, ""},
		{"two basic headers", header("Basic a", "Basic b"), bearer.Malformed, ""},
		{"two empty headers", header("", ""), bearer.Malformed, ""},
		{"three headers", header("Bearer a", "Bearer b", "Bearer c"), bearer.Malformed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			kind, token := bearer.Classify(tc.header)
			if kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", kind, tc.wantKind)
			}
			if token != tc.wantToken {
				t.Errorf("token = %q, want %q", token, tc.wantToken)
			}
		})
	}
}

// TestClassifyReadsTheCanonicalKeyOnly pins that the predicate sees what
// net/http's own accessors see. The server canonicalises every incoming key,
// so a non canonical key only exists in a header built by hand.
func TestClassifyReadsTheCanonicalKeyOnly(t *testing.T) {
	t.Parallel()

	h := http.Header{"authorization": {"Bearer x"}}
	if kind, _ := bearer.Classify(h); kind != bearer.None {
		t.Errorf("kind = %v, want None: net/http's Get would not see this key either", kind)
	}
}

func TestKindString(t *testing.T) {
	t.Parallel()

	for kind, want := range map[bearer.Kind]string{
		bearer.None:      "none",
		bearer.Bearer:    "bearer",
		bearer.Malformed: "malformed",
		bearer.Kind(99):  "unknown",
	} {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
}

// TestKindZeroValueIsNone keeps the zero value from meaning a credential, so
// a Kind nobody set behaves like a request without one.
func TestKindZeroValueIsNone(t *testing.T) {
	t.Parallel()

	var zero bearer.Kind
	if zero != bearer.None {
		t.Errorf("zero Kind = %v, want None", zero)
	}
}
