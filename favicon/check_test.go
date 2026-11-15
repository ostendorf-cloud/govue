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

func page(lang, head string) string {
	return `<!doctype html><html lang="` + lang + `"><head><title>t</title>` + head + `</head><body></body></html>`
}

func fullHead(t *testing.T) string {
	t.Helper()
	icons, err := favicon.Load(publicFS(), "#0f766e")
	if err != nil {
		t.Fatal(err)
	}
	return icons.Links()
}

func TestCheckPage(t *testing.T) {
	t.Parallel()

	const (
		svg   = `<link rel="icon" href="/favicon.svg" type="image/svg+xml">`
		png   = `<link rel="icon" href="/favicon-32x32.png" type="image/png" sizes="32x32">`
		touch = `<link rel="apple-touch-icon" href="/apple-touch-icon.png">`
		color = `<meta name="theme-color" content="#0f766e">`
	)
	tests := []struct {
		name   string
		head   string
		public func(fstest.MapFS)
		want   error
	}{
		{name: "all links present", head: svg + png + touch + color},
		{name: "single quotes and shortcut rel", head: `<link rel='shortcut icon' href='/favicon.svg'>` + png + touch + color},
		{name: "query string on a link", head: strings.Replace(svg, `.svg"`, `.svg?v=3"`, 1) + png + touch + color},
		{name: "no svg link", head: png + touch + color, want: favicon.ErrNoLink},
		{name: "no png link", head: svg + touch + color, want: favicon.ErrNoLink},
		{name: "no touch icon link", head: svg + png + color, want: favicon.ErrNoLink},
		{name: "no theme colour", head: svg + png + touch, want: favicon.ErrNoLink},
		{name: "empty theme colour", head: svg + png + touch + `<meta name="theme-color" content="">`, want: favicon.ErrNoLink},
		{name: "no head markup at all", head: "", want: favicon.ErrNoLink},
		{
			name: "links inside a comment do not count",
			head: `<!--` + svg + png + touch + color + `-->`,
			want: favicon.ErrNoLink,
		},
		{
			name: "unterminated comment runs to the end of the page",
			head: `<!-- old ` + svg + png + touch + color,
			want: favicon.ErrNoLink,
		},
		{
			name: "links inside a script string do not count",
			head: `<script>var s = '` + svg + png + touch + color + `';</script>`,
			want: favicon.ErrNoLink,
		},
		{
			name: "links inside a textarea do not count",
			head: `<textarea>` + svg + png + touch + color + `</textarea>`,
			want: favicon.ErrNoLink,
		},
		{
			name: "links inside a template do not count",
			head: `<template>` + svg + png + touch + color + `</template>`,
			want: favicon.ErrNoLink,
		},
		{
			name: "a template before the real links does not hide them",
			head: `<template><link rel="icon" href="/x.ico"></template>` + svg + png + touch + color,
		},
		{
			name: "script content does not hide the real links behind it",
			head: `<script>var a = "<!--";</script>` + svg + png + touch + color,
		},
		{
			name: "print only svg link does not count",
			head: strings.Replace(svg, `<link `, `<link media="print" `, 1) + png + touch + color,
			want: favicon.ErrNoLink,
		},
		{
			name: "dark scheme only svg link does not count",
			head: strings.Replace(svg, `<link `, `<link media="(prefers-color-scheme: dark)" `, 1) + png + touch + color,
			want: favicon.ErrNoLink,
		},
		{
			name: "media all counts",
			head: strings.Replace(svg, `<link `, `<link media=" ALL " `, 1) + png + touch + color,
		},
		{
			name: "greater than sign inside an attribute value",
			head: strings.Replace(svg, `<link `, `<link title="a > b" `, 1) + png + touch + color,
		},
		{
			name: "numeric entity in the href",
			head: strings.Replace(svg, "/favicon.svg", "&#47;favicon.svg", 1) + png + touch + color,
		},
		{
			name: "named entity in the href",
			head: strings.Replace(png, "/favicon-32x32.png", "&sol;favicon-32x32.png", 1) + svg + touch + color,
		},
		{
			name:   "linked svg file is missing",
			head:   svg + png + touch + color,
			public: func(m fstest.MapFS) { delete(m, favicon.SVGFile) },
			want:   favicon.ErrMissing,
		},
		{
			name: "svg link points at another existing file",
			head: strings.Replace(svg, "/favicon.svg", "/other.svg", 1) + png + touch + color,
			public: func(m fstest.MapFS) {
				m["other.svg"] = &fstest.MapFile{Data: []byte("<svg/>")}
			},
			want: favicon.ErrWrongFile,
		},
		{
			name: "robots.txt typed as svg",
			head: strings.Replace(svg, "/favicon.svg", "/robots.txt", 1) + png + touch + color,
			public: func(m fstest.MapFS) {
				m["robots.txt"] = &fstest.MapFile{Data: []byte("User-agent: *\n")}
			},
			want: favicon.ErrWrongFile,
		},
		{
			name: "touch icon link points at the png fallback",
			head: svg + png + strings.Replace(touch, "apple-touch-icon.png", "favicon-32x32.png", 1) + color,
			want: favicon.ErrWrongFile,
		},
		{
			name:   "linked svg holds only whitespace",
			head:   svg + png + touch + color,
			public: func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("  \n ")} },
			want:   favicon.ErrEmpty,
		},
		{
			name:   "linked svg holds no svg element",
			head:   svg + png + touch + color,
			public: func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{Data: []byte("User-agent: *\n")} },
			want:   favicon.ErrInvalid,
		},
		{
			name:   "linked png is not a png",
			head:   svg + png + touch + color,
			public: func(m fstest.MapFS) { m[favicon.PNGFile] = &fstest.MapFile{Data: []byte("GIF89a")} },
			want:   favicon.ErrInvalid,
		},
		{
			name:   "linked svg is empty",
			head:   svg + png + touch + color,
			public: func(m fstest.MapFS) { m[favicon.SVGFile] = &fstest.MapFile{} },
			want:   favicon.ErrEmpty,
		},
		{
			name:   "linked touch icon is missing",
			head:   svg + png + touch + color,
			public: func(m fstest.MapFS) { delete(m, favicon.TouchFile) },
			want:   favicon.ErrMissing,
		},
		{
			name: "link to another host",
			head: strings.Replace(svg, "/favicon.svg", "https://cdn.example/favicon.svg", 1) + png + touch + color,
			want: favicon.ErrBadHref,
		},
		{
			name: "relative link",
			head: strings.Replace(svg, "/favicon.svg", "favicon.svg", 1) + png + touch + color,
			want: favicon.ErrBadHref,
		},
		{
			name: "link escaping the public directory",
			head: strings.Replace(svg, "/favicon.svg", "/../favicon.svg", 1) + png + touch + color,
			want: favicon.ErrBadHref,
		},
	}
	for _, tt := range tests {
		for _, lang := range []string{"en", "de"} {
			t.Run(tt.name+" "+lang, func(t *testing.T) {
				t.Parallel()

				public := publicFS()
				if tt.public != nil {
					tt.public(public)
				}
				err := favicon.CheckPage([]byte(page(lang, tt.head)), public)
				if tt.want == nil {
					if err != nil {
						t.Fatalf("CheckPage: %v", err)
					}
					return
				}
				if !errors.Is(err, tt.want) {
					t.Errorf("error %v does not match %v", err, tt.want)
				}
			})
		}
	}
}

func TestCheckPageReadsOnlyTheHead(t *testing.T) {
	t.Parallel()

	links := fullHead(t)
	tests := []struct {
		name string
		page string
		want error
	}{
		{
			name: "links only in the body",
			page: `<!doctype html><html><head><title>t</title></head><body>` + links + `</body></html>`,
			want: favicon.ErrNoLink,
		},
		{
			name: "links in the head",
			page: `<!doctype html><html><head>` + links + `</head><body></body></html>`,
		},
		{
			name: "links after a header element still in the head",
			page: `<!doctype html><html><head><title>header</title>` + links + `</head><body><header></header></body></html>`,
		},
		{
			name: "no closing head tag, links before the body",
			page: `<!doctype html><html><head>` + links + `<body></body></html>`,
		},
		{
			name: "tag without a closing bracket",
			page: `<!doctype html><html><head><link rel="icon" href="/favicon.svg"`,
			want: favicon.ErrNoLink,
		},
		{
			name: "unterminated quote swallows the rest",
			page: `<!doctype html><html><head><link title="x ` + links + `</head>`,
			want: favicon.ErrNoLink,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := favicon.CheckPage([]byte(tt.page), publicFS())
			if tt.want == nil {
				if err != nil {
					t.Fatalf("CheckPage: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("error %v does not match %v", err, tt.want)
			}
		})
	}
}

func TestPresent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page string
		want bool
	}{
		{name: "none", page: `<html><head><title>t</title></head></html>`},
		{name: "icon link", page: `<html><head><link rel="icon" href="/x.ico"></head></html>`, want: true},
		{name: "touch icon", page: `<html><head><link rel="apple-touch-icon" href="/x.png"></head></html>`, want: true},
		{name: "theme colour", page: `<html><head><meta name="theme-color" content="#fff"></head></html>`, want: true},
		{name: "stylesheet only", page: `<html><head><link rel="stylesheet" href="/a.css"></head></html>`},
		{name: "in a template", page: `<html><head><template><link rel="icon" href="/x.ico"></template></head></html>`},
		{name: "in a comment", page: `<html><head><!-- <link rel="icon" href="/x.ico"> --></head></html>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := favicon.Present([]byte(tt.page)); got != tt.want {
				t.Errorf("Present = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckPageAcceptsTheShellMarkup(t *testing.T) {
	t.Parallel()

	if err := favicon.CheckPage([]byte(page("en", fullHead(t))), publicFS()); err != nil {
		t.Fatalf("the markup Links writes fails its own check: %v", err)
	}
}

func TestCheckPages(t *testing.T) {
	t.Parallel()

	good := page("en", fullHead(t))
	tests := []struct {
		name    string
		pages   fstest.MapFS
		want    error
		wantMsg string
	}{
		{
			name: "every language has the links",
			pages: fstest.MapFS{
				"index.html":       {Data: []byte(good)},
				"de/index.html":    {Data: []byte(strings.Replace(good, `lang="en"`, `lang="de"`, 1))},
				"de/about/x.html":  {Data: []byte(strings.Replace(good, `lang="en"`, `lang="de"`, 1))},
				"robots.txt":       {Data: []byte("User-agent: *")},
				"assets/app.js":    {Data: []byte("x")},
				"de/info/_unused":  {Data: []byte("x")},
				"fr/index.html.js": {Data: []byte("x")},
			},
		},
		{
			name: "one language lacks the links",
			pages: fstest.MapFS{
				"index.html":    {Data: []byte(good)},
				"de/index.html": {Data: []byte(page("de", ""))},
			},
			want:    favicon.ErrNoLink,
			wantMsg: "de/index.html",
		},
		{
			name:  "no page at all",
			pages: fstest.MapFS{"robots.txt": {Data: []byte("x")}},
			want:  favicon.ErrNoPages,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := favicon.CheckPages(tt.pages, publicFS())
			if tt.want == nil {
				if err != nil {
					t.Fatalf("CheckPages: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error %v does not match %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error does not name %q: %v", tt.wantMsg, err)
			}
		})
	}
}
