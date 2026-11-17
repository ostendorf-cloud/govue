// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package legal checks that a site ships an imprint and a privacy page, each
// in German and English.
//
// The Vue pages of govue mark their content with a data-govue-legal attribute
// and a lang attribute. Check reads those marks back from the prerendered HTML,
// so it fails for a page that was never rendered, for one rendered in the wrong
// language and for one whose English text is a copy of the German text. It is
// the legal counterpart of the favicon package: a site calls it from its own
// tests, and the build fails when a page is missing.
//
// The check proves the structure. It does not read the words and it is not a
// legal review.
package legal

import (
	"errors"
	"fmt"
	"html"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Page kinds, the value of the data-govue-legal attribute.
const (
	KindImprint = "imprint"
	KindPrivacy = "privacy"
)

// MinText is the least number of characters of visible text a legal page
// holds. A page with less is an empty shell, not an imprint.
const MinText = 100

// Required lists the languages every site ships, German and English. The
// German text is the binding one.
var Required = []string{"de", "en"}

var (
	// ErrNoLanguage means the site lists no pages for a required language.
	ErrNoLanguage = errors.New("no pages for this language")
	// ErrNoPage means a page is not listed, or its file cannot be read.
	ErrNoPage = errors.New("page is missing")
	// ErrNoMarker means the file holds no element marked as this legal page.
	ErrNoMarker = errors.New("page has no marked legal content")
	// ErrWrongLanguage means the marked content carries another lang than the
	// language it is listed under.
	ErrWrongLanguage = errors.New("marked content has another language")
	// ErrNoText means the marked content has too little visible text.
	ErrNoText = errors.New("marked content has too little text")
	// ErrSameText means the texts of two languages are identical, so one
	// language was served twice.
	ErrSameText = errors.New("texts of two languages are identical")
)

// Page names the prerendered files of one language, relative to the root of
// the file system given to Check.
type Page struct {
	Imprint string
	Privacy string
}

// Pages maps a language code to its two pages.
type Pages map[string]Page

var (
	tagRE     = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|'[^']*'|[^>"'])*)>`)
	commentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	attrRE    = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	spaceRE   = regexp.MustCompile(`\s+`)
)

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true,
	"wbr": true,
}

// Check reports every way site lacks a legal page. Each of the Required
// languages needs an imprint and a privacy page. Every listed language is
// checked the same way, so a third language is held to the same rules. The
// problems are joined, so one run shows them all.
func Check(pages fs.FS, site Pages) error {
	var errs []error
	for _, code := range Required {
		if _, ok := site[code]; !ok {
			errs = append(errs, fmt.Errorf("legal: %s: %w", code, ErrNoLanguage))
		}
	}

	codes := make([]string, 0, len(site))
	for code := range site {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	text := map[string]map[string]string{KindImprint: {}, KindPrivacy: {}}
	for _, code := range codes {
		for _, one := range []struct{ kind, file string }{
			{KindImprint, site[code].Imprint},
			{KindPrivacy, site[code].Privacy},
		} {
			got, err := checkPage(pages, code, one.kind, one.file)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			text[one.kind][code] = got
		}
	}

	for _, kind := range []string{KindImprint, KindPrivacy} {
		for i, a := range codes {
			for _, b := range codes[i+1:] {
				if text[kind][a] != "" && text[kind][a] == text[kind][b] {
					errs = append(errs, fmt.Errorf("legal: %s in %s and %s: %w", kind, a, b, ErrSameText))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// checkPage reads one file and returns the visible text of the marked content.
func checkPage(pages fs.FS, code, kind, file string) (string, error) {
	name := fmt.Sprintf("legal: %s %s", kind, code)
	if file == "" {
		return "", fmt.Errorf("%s: no file listed: %w", name, ErrNoPage)
	}
	raw, err := fs.ReadFile(pages, file)
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w", name, file, errors.Join(ErrNoPage, err))
	}

	lang, content, ok := marked(string(raw), kind)
	if !ok {
		return "", fmt.Errorf("%s: %s: %w", name, file, ErrNoMarker)
	}
	if !strings.EqualFold(lang, code) {
		return "", fmt.Errorf("%s: %s has lang %q: %w", name, file, lang, ErrWrongLanguage)
	}
	visible := strings.TrimSpace(spaceRE.ReplaceAllString(html.UnescapeString(stripTags(content)), " "))
	if utf8.RuneCountInString(visible) < MinText {
		return "", fmt.Errorf("%s: %s has %d characters, want %d: %w",
			name, file, utf8.RuneCountInString(visible), MinText, ErrNoText)
	}
	return visible, nil
}

// marked finds the first element whose data-govue-legal is kind, outside a
// comment, and returns its lang and its inner HTML.
func marked(page, kind string) (lang, content string, ok bool) {
	page = commentRE.ReplaceAllString(page, "")
	depth := 0
	start := 0
	name := ""
	for _, m := range tagRE.FindAllStringSubmatchIndex(page, -1) {
		closing := page[m[2]:m[3]] == "/"
		tag := strings.ToLower(page[m[4]:m[5]])
		attrs := page[m[6]:m[7]]
		if depth == 0 {
			if closing || voidTags[tag] {
				continue
			}
			values := attributes(attrs)
			if values["data-govue-legal"] != kind {
				continue
			}
			name, start, lang, depth = tag, m[1], values["lang"], 1
			continue
		}
		if tag != name || voidTags[tag] {
			continue
		}
		if closing {
			depth--
			if depth == 0 {
				return lang, page[start:m[0]], true
			}
		} else {
			depth++
		}
	}
	return "", "", false
}

func attributes(src string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRE.FindAllStringSubmatch(src, -1) {
		key := strings.ToLower(m[1])
		if _, seen := out[key]; seen {
			continue
		}
		value := m[2] + m[3] + m[4]
		out[key] = html.UnescapeString(value)
	}
	return out
}

// stripTags drops every tag and the content of script and style elements.
func stripTags(s string) string {
	var b strings.Builder
	skip := ""
	last := 0
	for _, m := range tagRE.FindAllStringSubmatchIndex(s, -1) {
		if skip == "" {
			b.WriteString(s[last:m[0]])
			b.WriteByte(' ')
		}
		last = m[1]
		closing := s[m[2]:m[3]] == "/"
		tag := strings.ToLower(s[m[4]:m[5]])
		switch {
		case skip == "" && !closing && (tag == "script" || tag == "style"):
			skip = tag
		case skip != "" && closing && tag == skip:
			skip = ""
		}
	}
	if skip == "" {
		b.WriteString(s[last:])
	}
	return b.String()
}
