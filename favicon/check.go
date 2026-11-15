// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package favicon

import (
	"errors"
	"fmt"
	"html"
	"io/fs"
	"path"
	"strings"
)

var (
	// ErrNoLink means a page lacks one of the required icon tags.
	ErrNoLink = errors.New("page has no such icon tag")
	// ErrBadHref means an icon link is not a root absolute path on the site.
	ErrBadHref = errors.New("icon link is not a root absolute path")
	// ErrWrongFile means an icon link points at another file than the one
	// the role has by convention.
	ErrWrongFile = errors.New("icon link does not point at the conventional file")
	// ErrNoPages means CheckPages found no HTML page to check, which would
	// make the check pass on nothing.
	ErrNoPages = errors.New("no html page found")
)

// tag is one link or meta element with its lower cased attribute names and
// entity decoded values. The first occurrence of an attribute wins, as in a
// browser.
type tag struct {
	name  string
	attrs map[string]string
}

// rawText lists the elements whose content is text, not markup. A tag
// written inside one is no tag.
var rawText = map[string]bool{
	"script": true, "style": true, "textarea": true, "title": true, "noscript": true,
	"template": true,
}

// headTags returns the link and meta elements of the head section of page,
// the way a browser reads them: comments are skipped, and an unterminated
// comment runs to the end of the page. The content of script, style,
// textarea, title, noscript and template is skipped, a tag with an unterminated quoted
// value or no closing bracket is dropped, and the scan ends at the closing
// head tag or the opening body tag.
func headTags(src string) []tag {
	var tags []tag
	for i := 0; i < len(src); {
		j := strings.IndexByte(src[i:], '<')
		if j < 0 {
			break
		}
		i += j
		if strings.HasPrefix(src[i:], "<!--") {
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		name, closing, attrs, next, ok := parseTag(src, i)
		if !ok {
			if next < 0 {
				break
			}
			i = next
			continue
		}
		i = next
		switch {
		case closing && name == "head", !closing && name == "body":
			return tags
		case closing:
		case name == "link" || name == "meta":
			tags = append(tags, tag{name: name, attrs: attrs})
		case rawText[name]:
			end := indexFold(src[i:], "</"+name)
			if end < 0 {
				return tags
			}
			i += end
		}
	}
	return tags
}

// parseTag parses the tag that starts at src[at] == '<'. It returns next, the
// index to continue at. ok is false when it is no tag, then next is at+1, or
// -1 when the tag never ends and the scan is over.
func parseTag(src string, at int) (name string, closing bool, attrs map[string]string, next int, ok bool) {
	i := at + 1
	if i < len(src) && src[i] == '/' {
		closing = true
		i++
	}
	start := i
	for i < len(src) && isNameByte(src[i]) {
		i++
	}
	if i == start {
		return "", false, nil, at + 1, false
	}
	name = asciiLower(src[start:i])
	attrs = map[string]string{}
	for {
		for i < len(src) && (isSpace(src[i]) || src[i] == '/') {
			i++
		}
		if i >= len(src) {
			return "", false, nil, -1, false
		}
		if src[i] == '>' {
			return name, closing, attrs, i + 1, true
		}
		start = i
		for i < len(src) && !isSpace(src[i]) && src[i] != '=' && src[i] != '>' && src[i] != '/' {
			i++
		}
		key := asciiLower(src[start:i])
		for i < len(src) && isSpace(src[i]) {
			i++
		}
		value := ""
		if i < len(src) && src[i] == '=' {
			i++
			for i < len(src) && isSpace(src[i]) {
				i++
			}
			if i >= len(src) {
				return "", false, nil, -1, false
			}
			if q := src[i]; q == '"' || q == '\'' {
				end := strings.IndexByte(src[i+1:], q)
				if end < 0 {
					return "", false, nil, -1, false
				}
				value = src[i+1 : i+1+end]
				i += end + 2
			} else {
				start = i
				for i < len(src) && !isSpace(src[i]) && src[i] != '>' {
					i++
				}
				value = src[start:i]
			}
		}
		if _, seen := attrs[key]; !seen && key != "" {
			attrs[key] = html.UnescapeString(value)
		}
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

// asciiLower folds ASCII letters only, so the length never changes.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// indexFold returns the index of the lower case ASCII sub in s, ignoring
// ASCII case, or -1.
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if asciiLower(s[i:i+len(sub)]) == sub {
			return i
		}
	}
	return -1
}

func hasRel(t tag, want string) bool {
	for _, f := range strings.Fields(strings.ToLower(t.attrs["rel"])) {
		if f == want {
			return true
		}
	}
	return false
}

// appliesAlways reports whether a link applies to every medium. A link with
// any other media query, print for example, is not the icon of the screen
// page and does not count. Only an empty value and all apply.
func appliesAlways(t tag) bool {
	m := strings.ToLower(strings.TrimSpace(t.attrs["media"]))
	return m == "" || m == "all"
}

// Present reports whether the head of page already carries an icon link, an
// apple-touch-icon link or a theme-color meta tag. themeboot.Shell uses it
// to refuse a page that would end up with the markup twice.
func Present(page []byte) bool {
	for _, t := range headTags(string(page)) {
		if t.name == "link" && (hasRel(t, "icon") || hasRel(t, "apple-touch-icon")) {
			return true
		}
		if t.name == "meta" && strings.EqualFold(t.attrs["name"], "theme-color") {
			return true
		}
	}
	return false
}

// CheckPage reports every way page lacks a working favicon. Only the head is
// read, with comments, script and textarea content skipped. It fails for
//
//   - no SVG icon link, no PNG icon link, no apple-touch-icon link or no
//     theme-color meta tag with a content,
//   - a link whose href is not a root absolute path,
//   - a link that points at another file than the role's own, favicon.svg,
//     favicon-32x32.png or apple-touch-icon.png,
//   - a linked file that is missing or empty in public, an SVG with no svg
//     element, or a PNG without the PNG signature.
//
// A link with a media query other than all does not count. Attribute values
// are entity decoded, so &#47;favicon.svg is the path /favicon.svg. A linked
// file may be a symbolic link, the file system decides. The problems are
// joined, so one run shows them all.
func CheckPage(page []byte, public fs.FS) error {
	var svg, png, touch []tag
	colors := 0
	for _, t := range headTags(string(page)) {
		if t.name == "meta" {
			if strings.EqualFold(t.attrs["name"], "theme-color") && strings.TrimSpace(t.attrs["content"]) != "" {
				colors++
			}
			continue
		}
		if !appliesAlways(t) {
			continue
		}
		href := strings.ToLower(t.attrs["href"])
		typ := strings.ToLower(t.attrs["type"])
		switch {
		case hasRel(t, "apple-touch-icon"):
			touch = append(touch, t)
		case hasRel(t, "icon") && (typ == "image/svg+xml" || strings.HasSuffix(href, ".svg")):
			svg = append(svg, t)
		case hasRel(t, "icon") && (typ == "image/png" || strings.HasSuffix(href, ".png")):
			png = append(png, t)
		}
	}

	var errs []error
	for _, g := range []struct {
		what string
		file string
		tags []tag
	}{
		{"svg icon link", SVGFile, svg},
		{"png icon link", PNGFile, png},
		{"apple-touch-icon link", TouchFile, touch},
	} {
		if len(g.tags) == 0 {
			errs = append(errs, fmt.Errorf("favicon: %s: %w", g.what, ErrNoLink))
			continue
		}
		for _, t := range g.tags {
			if err := checkHref(t.attrs["href"], g.file, public); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if colors == 0 {
		errs = append(errs, fmt.Errorf("favicon: theme-color meta tag: %w", ErrNoLink))
	}
	return errors.Join(errs...)
}

func checkHref(href, want string, public fs.FS) error {
	p, ok := publicPath(href)
	if !ok {
		return fmt.Errorf("favicon: link %q: %w", href, ErrBadHref)
	}
	if p != want {
		return fmt.Errorf("favicon: link %q points at %s, want %s: %w", href, p, want, ErrWrongFile)
	}
	if err := checkFile(public, "", want); err != nil {
		return fmt.Errorf("link %q: %w", href, err)
	}
	return nil
}

// CheckPages runs CheckPage on every .html file under the root of pages,
// the output directory of a prerender, against the public directory the
// links resolve in. Errors name the page. Finding no page is an error, so a
// wrong directory cannot pass. It is the guarantee that no page ships
// without icons, themeboot.Inject itself leaves a page without a head alone.
func CheckPages(pages, public fs.FS) error {
	var errs []error
	n := 0
	walkErr := fs.WalkDir(pages, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".html" {
			return nil
		}
		n++
		page, err := fs.ReadFile(pages, p)
		if err != nil {
			return err
		}
		if err := CheckPage(page, public); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
		return nil
	})
	if walkErr != nil {
		errs = append(errs, fmt.Errorf("favicon: walk pages: %w", walkErr))
	}
	if n == 0 {
		errs = append(errs, fmt.Errorf("favicon: %w", ErrNoPages))
	}
	return errors.Join(errs...)
}
