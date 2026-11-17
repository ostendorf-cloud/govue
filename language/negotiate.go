// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package language

import "strings"

const (
	// maxHeaderLen is the longest Accept-Language value Negotiate reads. A
	// longer one is treated as absent: no browser sends that much.
	maxHeaderLen = 4 << 10
	// maxEntries bounds how many comma separated entries Negotiate reads,
	// the same bound clientip uses for X-Forwarded-For.
	maxEntries = 32
)

// score is the best quality seen for a language and the position of the
// entry that gave it. A q of -1 means the language was not named.
type score struct {
	q, pos int
}

// Negotiate picks the entry of languages that an Accept-Language header
// prefers. It returns false when the header is empty, longer than 4 KiB, or
// accepts none of the languages, and the caller then applies its default.
//
// Tags match by primary subtag and ignore case, so de-AT matches de. The
// quality is read in thousandths and must be 0 to 1 with at most three
// decimals. An entry with any other quality is dropped and the rest still
// count. A quality of 0 excludes a language. Equal qualities keep header
// order. Only the first 32 entries are read.
//
// A * entry gives its quality to every language the header does not name.
// Among those, the earlier one in languages wins, so a caller that wants the
// wildcard to mean its default lists the default first.
//
// The entries of languages must be lowercase primary subtags such as de. A
// code like EN or en-US never matches.
//
// The result is always an element of languages, never text from the header.
func Negotiate(header string, languages []string) (string, bool) {
	if header == "" || len(header) > maxHeaderLen || len(languages) == 0 {
		return "", false
	}

	named := make([]score, len(languages))
	for i := range named {
		named[i].q = -1
	}
	wild := score{q: -1}

	rest := header
	for pos := 0; pos < maxEntries && rest != ""; pos++ {
		var entry string
		entry, rest, _ = strings.Cut(rest, ",")
		tag, q, ok := parseEntry(entry)
		if !ok {
			continue
		}
		if tag == "*" {
			if q > wild.q {
				wild = score{q: q, pos: pos}
			}
			continue
		}
		primary, _, _ := strings.Cut(tag, "-")
		for i, code := range languages {
			if code == primary && q > named[i].q {
				named[i] = score{q: q, pos: pos}
			}
		}
	}

	best := -1
	var bestScore score
	for i := range languages {
		s := named[i]
		if s.q < 0 {
			s = wild
		}
		if s.q <= 0 {
			continue
		}
		if best < 0 || s.q > bestScore.q || (s.q == bestScore.q && s.pos < bestScore.pos) {
			best, bestScore = i, s
		}
	}
	if best < 0 {
		return "", false
	}
	return languages[best], true
}

// parseEntry splits one entry into its lowercase tag and its quality in
// thousandths. It reports false for an empty tag or an invalid quality.
func parseEntry(entry string) (tag string, q int, ok bool) {
	tagPart, params, _ := strings.Cut(entry, ";")
	tag = strings.ToLower(strings.TrimSpace(tagPart))
	if tag == "" {
		return "", 0, false
	}
	q = 1000
	seenQ := false
	for params != "" {
		var p string
		p, params, _ = strings.Cut(params, ";")
		key, val, hasVal := strings.Cut(p, "=")
		if !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		if seenQ || !hasVal {
			return "", 0, false
		}
		seenQ = true
		parsed, valid := parseQ(strings.TrimSpace(val))
		if !valid {
			return "", 0, false
		}
		q = parsed
	}
	return tag, q, true
}

// parseQ reads a quality value: 0 or 1, optionally followed by a dot and one
// to three digits, and above 1 only 1 with zeros. It returns thousandths.
func parseQ(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	whole := int(s[0] - '0')
	if s[0] != '0' && s[0] != '1' {
		return 0, false
	}
	if len(s) == 1 {
		return whole * 1000, true
	}
	if s[1] != '.' || len(s) < 3 || len(s) > 5 {
		return 0, false
	}
	frac := 0
	scale := 100
	for i := 2; i < len(s); i++ {
		d := int(s[i] - '0')
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		frac += d * scale
		scale /= 10
	}
	if whole == 1 && frac != 0 {
		return 0, false
	}
	return whole*1000 + frac, true
}
