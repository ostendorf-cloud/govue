// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package favicon_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
)

func publicFS() fstest.MapFS {
	return fstest.MapFS{
		favicon.SVGFile:   {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		favicon.PNGFile:   {Data: []byte("\x89PNG\r\n\x1a\n32")},
		favicon.TouchFile: {Data: []byte("\x89PNG\r\n\x1a\n180")},
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(fstest.MapFS)
		color     string
		wantErrs  []error
		wantFiles []string
	}{
		{name: "complete set", color: "#0f766e"},
		{name: "short hex colour", color: "#0fe"},
		{
			name:      "missing svg",
			mutate:    func(m fstest.MapFS) { delete(m, favicon.SVGFile) },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrMissing},
			wantFiles: []string{"expected at " + favicon.SVGFile},
		},
		{
			name:      "missing touch icon",
			mutate:    func(m fstest.MapFS) { delete(m, favicon.TouchFile) },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrMissing},
			wantFiles: []string{"expected at " + favicon.TouchFile},
		},
		{
			name:      "missing png",
			mutate:    func(m fstest.MapFS) { delete(m, favicon.PNGFile) },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrMissing},
			wantFiles: []string{favicon.PNGFile},
		},
		{
			name:      "empty svg",
			mutate:    func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrEmpty},
			wantFiles: []string{favicon.SVGFile},
		},
		{
			name:      "whitespace only svg",
			mutate:    func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte(" \n\t ")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrEmpty},
			wantFiles: []string{favicon.SVGFile},
		},
		{
			name:      "svg file holding another text",
			mutate:    func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("User-agent: *\n")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrInvalid},
			wantFiles: []string{favicon.SVGFile},
		},
		{
			name:      "tag that only starts like svg",
			mutate:    func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("<svgfoo/>")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrInvalid},
			wantFiles: []string{favicon.SVGFile},
		},
		{
			name: "svg with a leading xml declaration",
			mutate: func(m fstest.MapFS) {
				m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("\n<?xml version=\"1.0\"?>\n<SVG viewBox=\"0 0 1 1\"></SVG>")}
			},
			color: "#0f766e",
		},
		{
			name:      "svg element only inside a comment",
			mutate:    func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("<!-- <svg> -->")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrInvalid},
			wantFiles: []string{favicon.SVGFile},
		},
		{
			name:      "svg element inside an unterminated comment",
			mutate:    func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("<!-- <svg>")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrInvalid},
			wantFiles: []string{favicon.SVGFile},
		},
		{
			name:   "svg element after a comment",
			mutate: func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("<!-- logo --><svg/>")} },
			color:  "#0f766e",
		},
		{
			name:      "png fallback that is a jpeg",
			mutate:    func(m fstest.MapFS) { m[favicon.PNGFile] = &fstest.MapFile{Data: []byte("\xff\xd8\xff\xe0jfif")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrInvalid},
			wantFiles: []string{favicon.PNGFile},
		},
		{
			name:      "touch icon that is an svg",
			mutate:    func(m fstest.MapFS) { m[favicon.TouchFile] = &fstest.MapFile{Data: []byte("<svg/>")} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrInvalid},
			wantFiles: []string{favicon.TouchFile},
		},
		{
			name:      "directory instead of a file",
			mutate:    func(m fstest.MapFS) { m[favicon.TouchFile] = &fstest.MapFile{Mode: 1 << 31} },
			color:     "#0f766e",
			wantErrs:  []error{favicon.ErrMissing},
			wantFiles: []string{favicon.TouchFile},
		},
		{
			name:     "no theme colour",
			color:    "",
			wantErrs: []error{favicon.ErrThemeColor},
		},
		{
			name:     "colour name instead of hex",
			color:    "teal",
			wantErrs: []error{favicon.ErrThemeColor},
		},
		{
			name:      "every problem at once",
			mutate:    func(m fstest.MapFS) { clear(m) },
			color:     "red",
			wantErrs:  []error{favicon.ErrMissing, favicon.ErrThemeColor},
			wantFiles: []string{favicon.SVGFile, favicon.PNGFile, favicon.TouchFile},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := publicFS()
			if tt.mutate != nil {
				tt.mutate(m)
			}
			_, err := favicon.Load(m, tt.color)
			if len(tt.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			for _, want := range tt.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("error %v does not match %v", err, want)
				}
			}
			for _, name := range tt.wantFiles {
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Errorf("error does not name %q: %v", name, err)
				}
			}
		})
	}
}

func TestLoadDirNamesTheExpectedPath(t *testing.T) {
	t.Parallel()

	_, err := favicon.LoadDir(t.TempDir(), "#0f766e")
	var fe *favicon.FileError
	if !errors.As(err, &fe) {
		t.Fatalf("want a FileError, got %v", err)
	}
	if !errors.Is(err, favicon.ErrMissing) {
		t.Errorf("want ErrMissing, got %v", err)
	}
	if !strings.HasSuffix(fe.Path, fe.Name) || fe.Path == fe.Name {
		t.Errorf("path %q does not carry the directory in front of %q", fe.Path, fe.Name)
	}
}

func TestLinks(t *testing.T) {
	t.Parallel()

	icons, err := favicon.Load(publicFS(), "#0f766e")
	if err != nil {
		t.Fatal(err)
	}
	got := icons.Links()
	for _, want := range []string{
		`<link rel="icon" href="/favicon.svg" type="image/svg+xml">`,
		`<link rel="icon" href="/favicon-32x32.png" type="image/png" sizes="32x32">`,
		`<link rel="apple-touch-icon" href="/apple-touch-icon.png">`,
		`<meta name="theme-color" content="#0f766e">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("links lack %s: %s", want, got)
		}
	}
}
