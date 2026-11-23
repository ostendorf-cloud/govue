// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/middleware"
)

// TestNewRefusesAnIncompleteBuild names each build New must refuse to start
// with and the text its error must carry.
func TestNewRefusesAnIncompleteBuild(t *testing.T) {
	tests := []struct {
		name   string
		change func(fstest.MapFS)
		want   string
	}{
		{"no 404 page", func(b fstest.MapFS) { delete(b, "en/404.html") }, "no usable 404 page en/404.html"},
		{"404 page is a directory", func(b fstest.MapFS) {
			delete(b, "de/404.html")
			b["de/404.html/index.html"] = &fstest.MapFile{Data: html("de", "x")}
		}, "no usable 404 page de/404.html"},
		{"404 page without head", func(b fstest.MapFS) {
			b["de/404.html"] = &fstest.MapFile{Data: []byte("<p>404</p>")}
		}, "de/404.html: the page has no head element"},
		{"no imprint", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin+"/de/", origin+"/de/privacy", origin+"/en/",
				origin+"/en/imprint", origin+"/en/privacy")}
		}, "no legal page /de/imprint"},
		{"no privacy page in one language", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin+"/de/imprint", origin+"/de/privacy",
				origin+"/en/imprint")}
		}, "no legal page /en/privacy"},
		{"terms in one language only", func(b fstest.MapFS) {
			b["de/terms/index.html"] = &fstest.MapFile{Data: html("de", "terms")}
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin+"/de/imprint", origin+"/de/privacy",
				origin+"/de/terms", origin+"/en/imprint", origin+"/en/privacy")}
		}, "terms page in 1 of 2 languages"},
		{"no sitemap", func(b fstest.MapFS) { delete(b, "sitemap.xml") }, "no sitemap.xml"},
		{"sitemap index", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: []byte(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"/>`)}
		}, "must be a urlset"},
		{"page file missing", func(b fstest.MapFS) { delete(b, "en/imprint/index.html") }, "page en/imprint/index.html"},
		{"page without head", func(b fstest.MapFS) {
			b["de/index.html"] = &fstest.MapFile{Data: []byte("<p>no head</p>")}
		}, "page de/index.html: the page has no head element"},
		{"page with its own icons", func(b fstest.MapFS) {
			b["de/index.html"] = &fstest.MapFile{Data: []byte(`<html><head><link rel="icon" href="/x.svg"></head></html>`)}
		}, "already carries icon links"},
		{"loc of another origin", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin+"/de/imprint", "https://other.test/de/privacy")}
		}, "has another origin"},
		{"loc outside a language", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin + "/fr/imprint")}
		}, "is not under a configured language prefix"},
		{"loc with encoded slash", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin + "/de/a%2Fb")}
		}, "has an encoded slash"},
		{"loc with dot segment", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin + "/de/../secret")}
		}, "is not a plain path segment"},
		{"loc with dotfile segment", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin + "/de/.vite")}
		}, "is not a plain path segment"},
		{"loc with query", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin + "/de/imprint?x=1")}
		}, "has a query"},
		{"bare language prefix", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin + "/de")}
		}, "without its trailing slash"},
		{"loc listed twice", func(b fstest.MapFS) {
			b["sitemap.xml"] = &fstest.MapFile{Data: sitemapOf(origin+"/de/imprint", origin+"/de/imprint")}
		}, "listed twice"},
		{"no favicon", func(b fstest.MapFS) { delete(b, favicon.TouchFile) }, favicon.TouchFile},
		{"public file missing", func(b fstest.MapFS) { delete(b, ogImage) }, "Config.PublicFiles entry 1: file does not exist"},
		{"asset with unknown extension", func(b fstest.MapFS) {
			b["assets/page.html"] = &fstest.MapFile{Data: []byte("<html>")}
		}, "asset assets/page.html has an extension that is not served"},
		{"asset with an odd name", func(b fstest.MapFS) {
			b["assets/a b.js"] = &fstest.MapFile{Data: []byte("x")}
		}, "asset assets/a b.js: the name"},
		{"asset is a symbolic link", func(b fstest.MapFS) {
			b["assets/link.js"] = &fstest.MapFile{Data: []byte("../.env"), Mode: fs.ModeSymlink}
		}, "asset assets/link.js is not a regular file"},
		{"page is a symbolic link", func(b fstest.MapFS) {
			b["de/index.html"] = &fstest.MapFile{Data: []byte("../de/secret/index.html"), Mode: fs.ModeSymlink}
		}, "page de/index.html: not a regular file"},
		{"sitemap is a symbolic link", func(b fstest.MapFS) {
			b["real-sitemap.xml"] = b["sitemap.xml"]
			b["sitemap.xml"] = &fstest.MapFile{Data: []byte("real-sitemap.xml"), Mode: fs.ModeSymlink}
		}, "no sitemap.xml: not a regular file"},
		{"favicon is a symbolic link", func(b fstest.MapFS) {
			b["icons/favicon.svg"] = b[favicon.SVGFile]
			b[favicon.SVGFile] = &fstest.MapFile{Data: []byte("icons/favicon.svg"), Mode: fs.ModeSymlink}
		}, "public file favicon.svg: not a regular file"},
		{"touch icon is a symbolic link", func(b fstest.MapFS) {
			b["icons/apple-touch-icon.png"] = b[favicon.TouchFile]
			b[favicon.TouchFile] = &fstest.MapFile{Data: []byte("icons/apple-touch-icon.png"), Mode: fs.ModeSymlink}
		}, "public file apple-touch-icon.png: not a regular file"},
		{"assets is a file", func(b fstest.MapFS) {
			for name := range b {
				if strings.HasPrefix(name, "assets/") {
					delete(b, name)
				}
			}
			b["assets"] = &fstest.MapFile{Data: []byte("x")}
		}, "assets of the build is not a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			build := buildOf(routes)
			tt.change(build)
			_, err := frontend.New(testConfig(build, io.Discard))
			if err == nil {
				t.Fatal("New accepted the build")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not say %q:\n%v", tt.want, err)
			}
		})
	}
}

func TestNewNeedsBuildAndLogger(t *testing.T) {
	_, err := frontend.New(frontend.Config{ThemeColor: themeColor})
	if err == nil {
		t.Fatal("New accepted an empty config")
	}
	for _, want := range []string{"Config.Build is required", "Config.Logger is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q: %v", want, err)
		}
	}
}

func TestNewNeverQuotesARefusedValue(t *testing.T) {
	const secret = "hunter2-SECRET-value"
	tests := []struct {
		name   string
		change func(*frontend.Config)
		want   string
	}{
		{
			"public file name", func(c *frontend.Config) { c.PublicFiles = []string{ogImage, "../" + secret + ".png"} },
			"Config.PublicFiles entry 2",
		},
		{
			"public file without known extension", func(c *frontend.Config) { c.PublicFiles = []string{secret + ".exe"} },
			"Config.PublicFiles entry 1",
		},
		{
			"public HTML file", func(c *frontend.Config) { c.PublicFiles = []string{secret + ".html"} },
			"Config.PublicFiles entry 1",
		},
		{
			"public dotfile", func(c *frontend.Config) { c.PublicFiles = []string{"." + secret + ".txt"} },
			"Config.PublicFiles entry 1",
		},
		{"theme colour", func(c *frontend.Config) { c.ThemeColor = secret }, "Config.ThemeColor"},
		{
			"language code", func(c *frontend.Config) { c.Language = language.Config{Languages: []string{"de", secret}} },
			"Config.Language",
		},
		{
			"language cookie", func(c *frontend.Config) { c.Language = language.Config{Cookie: secret + ";"} },
			"Config.Language",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(buildOf(routes), io.Discard)
			tt.change(&cfg)
			_, err := frontend.New(cfg)
			configtexttest.AssertHidden(t, err, secret, tt.want)
		})
	}
}

// TestNewRefusesAnUnusablePublicFileWithoutQuotingIt covers an entry that
// passes the name check but is no regular file of the build. Neither the
// error text nor any error under it, such as a *fs.PathError, may carry the
// name.
func TestNewRefusesAnUnusablePublicFileWithoutQuotingIt(t *testing.T) {
	const name = "hunter2-secret.png"
	tests := []struct {
		name     string
		change   func(fstest.MapFS)
		want     string
		notExist bool
	}{
		{"missing", func(fstest.MapFS) {}, "Config.PublicFiles entry 2: file does not exist", true},
		{"directory", func(b fstest.MapFS) {
			b[name+"/x.png"] = &fstest.MapFile{Data: []byte("x")}
		}, "Config.PublicFiles entry 2: not a regular file", false},
		{"symbolic link", func(b fstest.MapFS) {
			b[name] = &fstest.MapFile{Data: []byte(ogImage), Mode: fs.ModeSymlink}
		}, "Config.PublicFiles entry 2: not a regular file", false},
		{"named pipe", func(b fstest.MapFS) {
			b[name] = &fstest.MapFile{Mode: fs.ModeNamedPipe}
		}, "Config.PublicFiles entry 2: not a regular file", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			build := buildOf(routes)
			tt.change(build)
			cfg := testConfig(build, io.Discard)
			cfg.PublicFiles = []string{ogImage, name}
			_, err := frontend.New(cfg)
			configtexttest.AssertHidden(t, err, "hunter2", tt.want)
			if errors.Is(err, fs.ErrNotExist) != tt.notExist {
				t.Errorf("errors.Is(err, fs.ErrNotExist) = %v, want %v", !tt.notExist, tt.notExist)
			}
		})
	}
}

func TestNewRefusesAPublicFileServedAlreadyWithoutQuotingIt(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		value string
		want  string
	}{
		{"favicon", []string{ogImage, favicon.SVGFile}, favicon.SVGFile, "Config.PublicFiles entry 2 names a file that is served already"},
		{"sitemap", []string{"sitemap.xml"}, "sitemap.xml", "Config.PublicFiles entry 1 names a file that is served already"},
		{"twice", []string{ogImage, "a.txt", ogImage}, ogImage, "Config.PublicFiles entry 3 repeats entry 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(buildOf(routes), io.Discard)
			cfg.PublicFiles = tt.files
			_, err := frontend.New(cfg)
			configtexttest.AssertHidden(t, err, tt.value, tt.want)
		})
	}
}

func TestNewRefusesAPolicyWithoutScriptSrc(t *testing.T) {
	security := middleware.DefaultSecurity()
	security.Policy = middleware.NewPolicy().Set("default-src", "'self'")
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Security = &security
	_, err := frontend.New(cfg)
	if err == nil || !strings.Contains(err.Error(), "sets no script-src") {
		t.Errorf("error = %v, want the missing script-src", err)
	}
}

func TestNewAcceptsANilPolicy(t *testing.T) {
	security := middleware.DefaultSecurity()
	security.Policy = nil
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Security = &security
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if csp := do(t, s.Handler(), "GET", "/de/", nil).Header.Get("Content-Security-Policy"); csp != "" {
		t.Errorf("a nil policy sent a CSP: %s", csp)
	}
}

func TestNewAcceptsABuildWithoutAssets(t *testing.T) {
	build := buildOf(routes)
	for name := range build {
		if strings.HasPrefix(name, "assets/") {
			delete(build, name)
		}
	}
	if _, err := frontend.New(testConfig(build, io.Discard)); err != nil {
		t.Errorf("New refused a build without assets: %v", err)
	}
}

func TestRunNeedsTheAdminAddress(t *testing.T) {
	err := newServer(t).Run(context.Background(), ":0", "")
	if err == nil || !strings.Contains(err.Error(), "admin address is required") {
		t.Errorf("Run error = %v, want the missing admin address", err)
	}
}
