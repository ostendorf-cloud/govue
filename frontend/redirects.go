// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Redirect sends an old path of a moved page to its page for good, with a
// 301 that keeps the query.
type Redirect struct {
	// From is the exact old path, decoded, such as /de/agb or /de/agb/. A
	// trailing slash is part of the path: list both forms to redirect both.
	From string
	// To is the path of a page of the sitemap, decoded, such as /de/terms.
	To string
}

// checkRedirectSources checks the From of every entry for the path rules a
// request must pass and for ServeMux pattern syntax. A refused value is never
// quoted, the error gives its position.
func checkRedirectSources(redirects []Redirect) error {
	var problems []error
	seen := map[string]int{}
	for i, r := range redirects {
		switch {
		case !redirectSourceOK(r.From):
			problems = append(problems, fmt.Errorf("frontend: Config.Redirects entry %d: the source must be an "+
				"absolute path of plain segments, no empty, dot, percent, backslash, brace, space or control "+
				"segment, no ? or #, a trailing slash allowed, and not / (the value is not shown)", i+1))
		case seen[r.From] > 0:
			problems = append(problems, fmt.Errorf("frontend: Config.Redirects entry %d repeats the source of "+
				"entry %d (the value is not shown)", i+1, seen[r.From]))
		default:
			seen[r.From] = i + 1
		}
	}
	return errors.Join(problems...)
}

// redirectSourceOK reports whether from passes canonicalPath and holds no
// ServeMux pattern syntax: every segment is plain, only the last one may be
// empty, and / itself is the language redirect.
func redirectSourceOK(from string) bool {
	// A ? or # would never match: a request carries them in its query or
	// fragment, never in its path.
	if len(from) < 2 || from[0] != '/' || !utf8.ValidString(from) || strings.ContainsAny(from, "?#") {
		return false
	}
	segments := strings.Split(from[1:], "/")
	for i, seg := range segments {
		if seg == "" && i == len(segments)-1 {
			continue
		}
		if !plainSegment(seg) {
			return false
		}
	}
	return true
}

// mountRedirects registers each redirect on mux, after every other route
// but the catch-all. A target that is not a page, a source under assets/
// and a source that is already a route of the site are refused. The last is
// found by ServeMux itself: registering a pattern that is already taken
// panics, and the panic, which quotes the pattern, is turned into an error
// that does not.
func (s *site) mountRedirects(mux *http.ServeMux, redirects []Redirect, route func(http.Handler) http.Handler) error {
	locations := make(map[string]string, len(s.pages))
	for _, p := range s.pages {
		locations[p.path] = p.location
	}
	var problems []error
	for i, r := range redirects {
		location, ok := locations[r.To]
		if !ok {
			problems = append(problems, fmt.Errorf("frontend: Config.Redirects entry %d: the target is not a page "+
				"of %s (the value is not shown)", i+1, sitemapFile))
			continue
		}
		if r.From == "/"+assetsDir || strings.HasPrefix(r.From, "/"+assetsDir+"/") {
			problems = append(problems, fmt.Errorf("frontend: Config.Redirects entry %d: the source is under "+
				"/%s/, where the build's files are served (the value is not shown)", i+1, assetsDir))
			continue
		}
		pattern := "GET " + r.From
		if strings.HasSuffix(r.From, "/") {
			pattern += "{$}"
		}
		if !register(mux, pattern, route(redirectTo(location))) {
			problems = append(problems, fmt.Errorf("frontend: Config.Redirects entry %d: the source is already a "+
				"route of the site, a page, its slash form, an alias or a file (the value is not shown)", i+1))
		}
	}
	return errors.Join(problems...)
}

// register adds pattern to mux and reports false when ServeMux refused it as
// taken or in conflict. The panic value is dropped: it quotes the pattern.
func register(mux *http.ServeMux, pattern string, h http.Handler) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	mux.Handle(pattern, h)
	return true
}
