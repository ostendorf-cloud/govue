// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package htmlhead finds the opening head tag of a page, the one place
// themeboot and frontend insert markup at. It is internal: the rule is
// shared, not a contract.
package htmlhead

import "strings"

// AfterHeadTag returns the index just after the opening head tag, or -1.
// Only a real head tag counts: not a header element and not a tag inside a
// comment.
//
// The scan runs on the original bytes with an ASCII only case fold. Lower
// casing the page would change its length for some non-ASCII runes, such as
// U+0130, and shift the returned index.
func AfterHeadTag(s string) int {
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				return -1
			}
			i += 4 + end + 3
		case hasPrefixASCIIFold(s[i:], "<head") && i+5 < len(s) &&
			(s[i+5] == '>' || s[i+5] == ' ' || s[i+5] == '\t' ||
				s[i+5] == '\n' || s[i+5] == '\r' || s[i+5] == '\f'):
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return -1
			}
			return i + end + 1
		default:
			i++
		}
	}
	return -1
}

// hasPrefixASCIIFold reports whether s starts with the lower case ASCII
// prefix, ignoring ASCII case only.
func hasPrefixASCIIFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}
