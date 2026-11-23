// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/ostendorf-cloud/govue/favicon"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// The fixed names of the build.
const (
	sitemapFile = "sitemap.xml"
	robotsFile  = "robots.txt"
	assetsDir   = "assets"
	// notFoundFile is the 404 page of one language, <code>/404.html.
	notFoundFile = "404.html"
)

// The legal pages a build must and may have, the paths of the Vue package's
// legalRouteDefs.
var (
	requiredLegal = []string{"imprint", "privacy"}
	optionalLegal = "terms"
)

// maxSitemapBytes is the 50 MiB limit of the sitemap protocol.
const maxSitemapBytes = 50 << 20

// sitemapNamespace is the namespace of a sitemap's urlset element.
const sitemapNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

// The Cache-Control values. A hashed name changes with its content, so it
// never goes stale. Everything else is revalidated with its ETag, except the
// favicon files, the sitemap and robots.txt, which keep the lifetimes the
// sites used before.
const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNoCache   = "no-cache"
	cacheDay       = "public, max-age=86400"
	cacheHour      = "public, max-age=3600"
)

// contentTypes is the closed table of the extensions served from assets/
// and the root. A file with another extension fails startup, so no file is
// ever served with a guessed type. HTML is not in it: a page is served only
// through the sitemap.
var contentTypes = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".woff":        "font/woff",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
	".pdf":         "application/pdf",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
	".mp3":         "audio/mpeg",
}

// fileSegment is one segment of a served file name: letters, digits, '.',
// '_' and '-', not starting with a dot. It is the shape Vite writes.
var fileSegment = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// hashChars is the alphabet of a Vite content hash.
var hashChars = regexp.MustCompile(`^[A-Za-z0-9_-]{8}$`)

// file is one response body, read and hashed at startup.
type file struct {
	body         []byte
	contentType  string
	cacheControl string
	etag         string
}

func newFile(body []byte, contentType, cacheControl string) *file {
	sum := sha256.Sum256(body)
	return &file{
		body: body, contentType: contentType, cacheControl: cacheControl,
		etag: `"` + hex.EncodeToString(sum[:]) + `"`,
	}
}

// page is one loc of the sitemap.
type page struct {
	// pattern is the ServeMux pattern of the exact path.
	pattern string
	// slashPattern is the pattern of the other slash form, redirected to
	// location.
	slashPattern string
	// location is the canonical path as the sitemap writes it, escaped.
	location string
	// path is the decoded path.
	path string
	file *file
	// origin is the scheme and host of the loc.
	origin string
}

// site is the whole build, in memory.
type site struct {
	languages []string
	fallback  string
	pages     []page
	notFound  map[string]*file
	// assets and public are keyed by the decoded URL path.
	assets  map[string]*file
	public  map[string]*file
	sitemap *file
	robots  *file
	terms   bool
}

// checkPublicFiles checks the names of Config.PublicFiles. A refused name is
// never quoted, the error gives its position.
func checkPublicFiles(names []string) ([]string, error) {
	fixed := []string{favicon.SVGFile, favicon.PNGFile, favicon.TouchFile, sitemapFile, robotsFile}
	var problems []error
	seen := map[string]int{}
	for i, name := range names {
		_, typed := contentTypes[strings.ToLower(path.Ext(name))]
		switch {
		case !fileSegment.MatchString(name) || !typed:
			problems = append(problems, fmt.Errorf("frontend: Config.PublicFiles entry %d must be a file name of "+
				"letters, digits, '.', '_' and '-', not starting with a dot, with a known extension that is not "+
				"HTML (the value is not shown)", i+1))
		case slices.Contains(fixed, name):
			problems = append(problems, fmt.Errorf("frontend: Config.PublicFiles entry %d names a file that is "+
				"served already, a favicon file, the sitemap or robots.txt (the value is not shown)", i+1))
		case seen[name] > 0:
			problems = append(problems, fmt.Errorf("frontend: Config.PublicFiles entry %d repeats entry %d "+
				"(the value is not shown)", i+1, seen[name]))
		default:
			seen[name] = i + 1
		}
	}
	return names, errors.Join(problems...)
}

// loadSite reads the build. opts are the theme boot options with the
// favicon links. languages has the default first.
func loadSite(build fs.FS, languages []string, fallback string, opts []themeboot.Option, publicFiles []string) (*site, error) {
	s := &site{
		languages: languages,
		fallback:  fallback,
		notFound:  map[string]*file{},
		assets:    map[string]*file{},
		public:    map[string]*file{},
	}
	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	raw, origin, err := s.loadSitemap(build, opts)
	fail(err)
	if err == nil {
		s.sitemap = newFile(raw, "application/xml; charset=utf-8", cacheHour)
		s.robots = newFile([]byte("User-agent: *\nAllow: /\n\nSitemap: "+origin+"/"+sitemapFile+"\n"),
			"text/plain; charset=utf-8", cacheHour)
		fail(s.checkLegal())
	}
	for _, code := range languages {
		name := code + "/" + notFoundFile
		f, err := loadHTML(build, name, opts)
		if err != nil {
			fail(fmt.Errorf("frontend: the build has no usable 404 page %s: %w", name, err))
			continue
		}
		f.cacheControl = cacheNoCache
		s.notFound[code] = f
	}
	fail(s.loadAssets(build))
	for _, name := range append([]string{favicon.SVGFile, favicon.PNGFile, favicon.TouchFile}, publicFiles...) {
		body, err := readRegular(build, name)
		if err != nil {
			fail(fmt.Errorf("frontend: public file %s: %w", name, err))
			continue
		}
		s.public["/"+name] = newFile(body, contentTypes[strings.ToLower(path.Ext(name))], cacheDay)
	}

	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return s, nil
}

// readRegular reads name, a regular file of build. A symbolic link, a
// directory and anything else fail: the build tools write none of them.
func readRegular(build fs.FS, name string) ([]byte, error) {
	info, err := fs.Lstat(build, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return fs.ReadFile(build, name)
}

// loadHTML reads one page and injects the theme boot script and the icon
// links. A page without a head element, or one that carries icon links of
// its own, fails: it would serve no script, or the icons twice.
func loadHTML(build fs.FS, name string, opts []themeboot.Option) (*file, error) {
	body, err := readRegular(build, name)
	if err != nil {
		return nil, err
	}
	if favicon.Present(body) {
		return nil, errors.New("the page already carries icon links or a theme-color tag")
	}
	injected := themeboot.InjectStatic(body, opts...)
	if len(injected) == len(body) {
		return nil, errors.New("the page has no head element for the theme boot script")
	}
	return newFile(injected, "text/html; charset=utf-8", cacheNoCache), nil
}

// loadSitemap reads the sitemap and every page it lists, and returns the
// raw sitemap and the origin of its locs.
func (s *site) loadSitemap(build fs.FS, opts []themeboot.Option) (raw []byte, origin string, err error) {
	f, err := build.Open(sitemapFile)
	if err != nil {
		return nil, "", fmt.Errorf("frontend: the build has no %s: %w", sitemapFile, err)
	}
	defer func() { _ = f.Close() }()
	raw, err = io.ReadAll(io.LimitReader(f, maxSitemapBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("frontend: read %s: %w", sitemapFile, err)
	}
	if len(raw) > maxSitemapBytes {
		return nil, "", fmt.Errorf("frontend: %s is larger than %d bytes", sitemapFile, maxSitemapBytes)
	}

	var doc struct {
		XMLName xml.Name
		URLs    []struct {
			Locs []string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, "", fmt.Errorf("frontend: parse %s: %w", sitemapFile, err)
	}
	if doc.XMLName != (xml.Name{Space: sitemapNamespace, Local: "urlset"}) {
		return nil, "", fmt.Errorf("frontend: %s must be a urlset in the sitemap namespace", sitemapFile)
	}
	if len(doc.URLs) == 0 {
		return nil, "", fmt.Errorf("frontend: %s lists no url", sitemapFile)
	}

	var problems []error
	seen := map[string]bool{}
	for _, u := range doc.URLs {
		if len(u.Locs) != 1 {
			problems = append(problems, fmt.Errorf("frontend: a url of %s has %d locs, want one", sitemapFile, len(u.Locs)))
			continue
		}
		loc := strings.TrimSpace(u.Locs[0])
		p, err := s.loadPage(build, loc, origin, opts)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		origin = p.origin
		if seen[p.path] {
			problems = append(problems, fmt.Errorf("frontend: loc %q is listed twice", loc))
			continue
		}
		seen[p.path] = true
		s.pages = append(s.pages, p)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, "", err
	}
	return raw, origin, nil
}

// loadPage turns one loc into its page. wantOrigin is the origin of the
// first loc, empty for the first.
func (s *site) loadPage(build fs.FS, loc, wantOrigin string, opts []themeboot.Option) (page, error) {
	bad := func(reason string) (page, error) {
		return page{}, fmt.Errorf("frontend: loc %q of %s %s", loc, sitemapFile, reason)
	}
	u, err := url.Parse(loc)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return bad("is not an absolute http or https URL")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil || u.Opaque != "" {
		return bad("has a query, a fragment or userinfo")
	}
	origin := u.Scheme + "://" + u.Host
	if wantOrigin != "" && origin != wantOrigin {
		return bad("has another origin than the first loc, " + wantOrigin)
	}
	escaped := u.EscapedPath()
	// Decoding %2f or %5c would change the segments, and %2e and %25 would
	// give a dot or a percent the request check refuses.
	if !encodingAllowed(escaped) {
		return bad("has an encoded slash, backslash, dot, percent or NUL")
	}
	p := u.Path
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if !strings.HasPrefix(p, "/") || !slices.Contains(s.languages, segments[0]) {
		return bad("is not under a configured language prefix")
	}
	isRoot := len(segments) == 2 && segments[1] == ""
	if isRoot {
		segments = segments[:1]
	}
	for _, seg := range segments {
		if !plainSegment(seg) {
			return bad("has a segment that is not a plain path segment")
		}
	}
	if len(segments) == 1 && !isRoot {
		return bad("is a language prefix without its trailing slash")
	}

	name := strings.Join(segments, "/") + "/index.html"
	f, err := loadHTML(build, name, opts)
	if err != nil {
		return page{}, fmt.Errorf("frontend: loc %q: page %s: %w", loc, name, err)
	}
	pg := page{path: p, location: escaped, file: f, origin: origin}
	if isRoot {
		pg.pattern = "GET /" + segments[0] + "/{$}"
		pg.slashPattern = "GET /" + segments[0]
	} else {
		pg.pattern = "GET " + p
		pg.slashPattern = "GET " + p + "/{$}"
	}
	return pg, nil
}

// plainSegment reports whether seg is a path segment the mux reads as
// itself: not empty, not starting with a dot, and free of backslash,
// percent, wildcard braces, white space and control characters.
func plainSegment(seg string) bool {
	if seg == "" || seg[0] == '.' || strings.ContainsAny(seg, "\\{}%") {
		return false
	}
	return strings.IndexFunc(seg, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || r == unicode.ReplacementChar
	}) < 0
}

// checkLegal requires the imprint and the privacy page in every language
// and the terms page in every language or in none.
func (s *site) checkLegal() error {
	has := map[string]bool{}
	for _, p := range s.pages {
		has[p.path] = true
	}
	var problems []error
	for _, code := range s.languages {
		for _, name := range requiredLegal {
			if !has["/"+code+"/"+name] {
				problems = append(problems, fmt.Errorf("frontend: the build has no legal page /%s/%s in %s", code, name, sitemapFile))
			}
		}
	}
	terms := 0
	for _, code := range s.languages {
		if has["/"+code+"/"+optionalLegal] {
			terms++
		}
	}
	switch terms {
	case 0:
	case len(s.languages):
		s.terms = true
	default:
		problems = append(problems, fmt.Errorf("frontend: the build has the terms page in %d of %d languages, "+
			"want every language or none", terms, len(s.languages)))
	}
	return errors.Join(problems...)
}

// loadAssets reads every file under assets/. A directory or file whose name
// starts with a dot is skipped and never served. A name of another shape, a
// symbolic link, anything but a regular file and an extension outside
// contentTypes fail. A build without assets/ serves none.
func (s *site) loadAssets(build fs.FS) error {
	info, err := fs.Lstat(build, assetsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return fmt.Errorf("frontend: %s of the build is not a directory", assetsDir)
	}
	var problems []error
	err = fs.WalkDir(build, assetsDir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == assetsDir {
			return nil
		}
		base := d.Name()
		if strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !fileSegment.MatchString(base) {
			problems = append(problems, fmt.Errorf("frontend: asset %s: the name is not letters, digits, '.', '_' and '-'", name))
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			problems = append(problems, fmt.Errorf("frontend: asset %s is not a regular file", name))
			return nil
		}
		contentType, ok := contentTypes[strings.ToLower(path.Ext(base))]
		if !ok {
			problems = append(problems, fmt.Errorf("frontend: asset %s has an extension that is not served", name))
			return nil
		}
		body, err := fs.ReadFile(build, name)
		if err != nil {
			return err
		}
		cache := cacheNoCache
		if hashedName(base) {
			cache = cacheImmutable
		}
		s.assets["/"+name] = newFile(body, contentType, cache)
		return nil
	})
	if err != nil {
		problems = append(problems, fmt.Errorf("frontend: read %s: %w", assetsDir, err))
	}
	return errors.Join(problems...)
}

// hashedName reports whether base carries a Vite content hash: the name
// without its extension ends in '-' or '.' and eight hash characters, at
// least one of them a digit or a capital letter, as in index-BXa3k2_9.js.
// A name that fails is revalidated instead of cached for good, which costs a
// request and never serves a stale file.
func hashedName(base string) bool {
	stem := strings.TrimSuffix(base, path.Ext(base))
	if len(stem) < 10 {
		return false
	}
	hash := stem[len(stem)-8:]
	if sep := stem[len(stem)-9]; sep != '-' && sep != '.' {
		return false
	}
	return hashChars.MatchString(hash) && strings.ContainsFunc(hash, func(r rune) bool {
		return unicode.IsDigit(r) || unicode.IsUpper(r)
	})
}

// encodingAllowed reports whether an escaped path is free of the encodings
// whose decoded form would change the segments or give a dot, a percent, a
// backslash or a NUL: %2f, %5c, %2e, %25 and %00, in either case.
func encodingAllowed(escaped string) bool {
	lower := strings.ToLower(escaped)
	for _, enc := range []string{"%2f", "%5c", "%2e", "%25", "%00"} {
		if strings.Contains(lower, enc) {
			return false
		}
	}
	return true
}
