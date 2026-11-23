// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package favicon gives every site built on govue the same favicon set and
// refuses a site that has none.
//
// A site provides one source SVG and generates the rest from it with the
// favicon:generate task target, so no file is edited by hand. The files
// live in one directory per site, conventionally web/public, which the
// frontend build copies to the root of its output. The names are fixed:
//
//	favicon.svg           the icon browsers prefer
//	favicon-32x32.png     the fallback for browsers without SVG icons
//	apple-touch-icon.png  180x180, used when a page is added to a home screen
//
// Load checks that all three exist and are not empty, and fails with an
// error naming the file and the path it was expected at. The result renders
// the head markup through Links. themeboot.WithFavicon puts that markup into
// every page its Shell and Inject serve, in the same step as the theme boot
// script, so no second injection point exists.
//
// CheckPage and CheckPages are the shared check a site's own test suite
// calls on its prerendered pages. They fail when a page has no icon link,
// when a link points off site, or when the linked file is missing or empty.
package favicon

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

// The fixed file names, relative to the root of the site.
const (
	SVGFile   = "favicon.svg"
	PNGFile   = "favicon-32x32.png"
	TouchFile = "apple-touch-icon.png"
)

// Sentinel errors, to be matched with errors.Is.
var (
	// ErrMissing means a required file does not exist.
	ErrMissing = errors.New("file is missing")
	// ErrEmpty means a required file exists with no content.
	ErrEmpty = errors.New("file is empty")
	// ErrInvalid means a file has the wrong content for its type: an SVG
	// without an svg element, or a PNG without the PNG signature.
	ErrInvalid = errors.New("file content does not match its type")
	// ErrThemeColor means the theme colour is not a #rgb or #rrggbb value.
	ErrThemeColor = errors.New("theme colour must be a hex value like #0f766e")
)

var (
	// pngMagic is the signature every PNG file starts with.
	pngMagic = []byte("\x89PNG\r\n\x1a\n")
	// commentPattern matches an XML comment, which is removed before the
	// search for an svg element. An unterminated one runs to the end.
	commentPattern = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)
	// svgPattern finds an svg element, a start tag named svg.
	svgPattern = regexp.MustCompile(`(?i)<svg[\s/>]`)
)

var colorPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// FileError names a file that failed the check and the path it is expected
// at. Err is ErrMissing or ErrEmpty.
type FileError struct {
	Name string
	Path string
	Err  error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("favicon: %s: %v, expected at %s", e.Name, e.Err, e.Path)
}

func (e *FileError) Unwrap() error { return e.Err }

// Icons is a checked favicon set. The zero value is not usable, get one from
// Load or LoadDir.
type Icons struct {
	themeColor string
	// darkColor is the theme colour of the dark mode, empty for one colour
	// in both modes.
	darkColor string
}

// Media queries of the two theme-color tags, the ones the theme boot script
// matches to pin the colour of a stored mode.
const (
	MediaLight = "(prefers-color-scheme: light)"
	MediaDark  = "(prefers-color-scheme: dark)"
)

// WithDarkThemeColor returns the icons with a theme colour of its own for the
// dark mode, a #rgb or #rrggbb value like the one Load took, which then
// becomes the light one. Links writes one theme-color tag per mode, each with
// its media query, so the browser follows the system mode, and the theme
// boot script of themeboot pins both tags to the colour of a stored mode. A
// colour of another form returns ErrThemeColor, without the value.
func (i Icons) WithDarkThemeColor(color string) (Icons, error) {
	if !colorPattern.MatchString(color) {
		return Icons{}, fmt.Errorf("favicon: dark theme colour: %w (the value is not shown)", ErrThemeColor)
	}
	i.darkColor = color
	return i, nil
}

// Load checks that the three icon files exist in the root of fsys, are not
// empty and hold what their type says: an svg element in the SVG, the PNG
// signature in the two PNG files. A symbolic link is accepted, the file
// system decides what it resolves to. It also checks that themeColor is a hex colour. Every problem is returned
// at once, joined. In an error, a file is named relative to fsys. Use
// LoadDir for the real path.
func Load(fsys fs.FS, themeColor string) (Icons, error) {
	return load(fsys, "", themeColor)
}

// LoadDir is Load on a directory of the file system, usually "web/public".
// Errors carry the directory in the expected path.
func LoadDir(dir, themeColor string) (Icons, error) {
	return load(os.DirFS(dir), dir, themeColor)
}

func load(fsys fs.FS, dir, themeColor string) (Icons, error) {
	var errs []error
	for _, name := range []string{SVGFile, PNGFile, TouchFile} {
		if err := checkFile(fsys, dir, name); err != nil {
			errs = append(errs, err)
		}
	}
	if !colorPattern.MatchString(themeColor) {
		// A refused value is never shown: a startup error reaches CI and
		// startup logs. ErrThemeColor names the expected form.
		errs = append(errs, fmt.Errorf("favicon: %w (the value is not shown)", ErrThemeColor))
	}
	if len(errs) > 0 {
		return Icons{}, errors.Join(errs...)
	}
	return Icons{themeColor: themeColor}, nil
}

func checkFile(fsys fs.FS, dir, name string) error {
	fail := func(err error) error {
		return &FileError{Name: name, Path: filepath.Join(dir, name), Err: err}
	}
	info, err := fs.Stat(fsys, name)
	if err != nil || info.IsDir() {
		return fail(ErrMissing)
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fail(ErrMissing)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fail(ErrEmpty)
	}
	if name == SVGFile {
		if !svgPattern.Match(commentPattern.ReplaceAll(data, nil)) {
			return fail(ErrInvalid)
		}
	} else if !bytes.HasPrefix(data, pngMagic) {
		return fail(ErrInvalid)
	}
	return nil
}

// Links returns the head markup: the SVG icon, the PNG fallback, the Apple
// touch icon and the theme colour meta tag, or one per mode with
// WithDarkThemeColor. It holds no script, so it needs no nonce. The links are
// root absolute, so a page in any language and at any depth resolves them the
// same way.
func (i Icons) Links() string {
	links := `<link rel="icon" href="/` + SVGFile + `" type="image/svg+xml">` +
		`<link rel="icon" href="/` + PNGFile + `" type="image/png" sizes="32x32">` +
		`<link rel="apple-touch-icon" href="/` + TouchFile + `">`
	if i.darkColor == "" {
		return links + `<meta name="theme-color" content="` + html.EscapeString(i.themeColor) + `">`
	}
	return links +
		`<meta name="theme-color" content="` + html.EscapeString(i.themeColor) + `" media="` + MediaLight + `">` +
		`<meta name="theme-color" content="` + html.EscapeString(i.darkColor) + `" media="` + MediaDark + `">`
}

// publicPath turns a root absolute href into a path inside the public
// directory. It reports false for anything else.
func publicPath(href string) (string, bool) {
	for k := 0; k < len(href); k++ {
		if href[k] == '?' || href[k] == '#' {
			href = href[:k]
			break
		}
	}
	if len(href) < 2 || href[0] != '/' || href[1] == '/' {
		return "", false
	}
	p := path.Clean(href[1:])
	if !fs.ValidPath(p) {
		return "", false
	}
	return p, true
}
