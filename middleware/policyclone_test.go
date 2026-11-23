// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import "testing"

func TestPolicyCloneLeavesTheOriginalUnchanged(t *testing.T) {
	original := DefaultPolicy()
	before := original.Header("n")

	clone := original.Clone()
	clone.Add("script-src", "'sha256-abc'").Set("img-src", "'self'").Add("worker-src", "'self'").NonceIn("img-src")

	if got := original.Header("n"); got != before {
		t.Errorf("the original changed with its clone:\n got %s\nwant %s", got, before)
	}
	want := "default-src 'none'; script-src 'self' 'sha256-abc' 'nonce-n'; style-src 'self' 'nonce-n'; " +
		"img-src 'self' 'nonce-n'; font-src 'self'; connect-src 'self'; form-action 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'; object-src 'none'; worker-src 'self'"
	if got := clone.Header("n"); got != want {
		t.Errorf("clone:\n got %s\nwant %s", got, want)
	}
}

// TestPolicyCloneSharesNoSourceList adds to both sides after the clone. A
// clone that shared a source list with spare capacity would let the second
// Add overwrite the first.
func TestPolicyCloneSharesNoSourceList(t *testing.T) {
	original := NewPolicy().Set("script-src", "'self'", "'a'", "'b'")
	// Drop the last source, so the list has spare capacity for an Add.
	original.values["script-src"] = original.values["script-src"][:2]
	clone := original.Clone()
	clone.Add("script-src", "'clone'")
	original.Add("script-src", "'original'")
	if got := clone.String(); got != "script-src 'self' 'a' 'clone'" {
		t.Errorf("clone = %q, the original's Add reached it", got)
	}
	if got := original.String(); got != "script-src 'self' 'a' 'original'" {
		t.Errorf("original = %q", got)
	}
}

func TestPolicyCloneOfNilIsNil(t *testing.T) {
	var p *Policy
	if p.Clone() != nil {
		t.Error("the clone of a nil policy is not nil")
	}
}

func TestPolicyHas(t *testing.T) {
	p := NewPolicy().Set("script-src", "'self'").Set("upgrade-insecure-requests")
	for directive, want := range map[string]bool{
		"script-src":                true,
		"upgrade-insecure-requests": true,
		"style-src":                 false,
	} {
		if got := p.Has(directive); got != want {
			t.Errorf("Has(%q) = %v, want %v", directive, got, want)
		}
	}
}
