// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxUserAgent = 500
	maxTitle     = 200
	maxPath      = 500
)

// excludedPaths are the health and metrics routes. They are never counted,
// whatever Options.Skip says.
var excludedPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
	"/livez":   true,
	"/metrics": true,
}

// botMarkers are lower case fragments of a user agent that names a robot, a
// preview fetcher or a script. The list is best effort. Umami filters bots
// again on its side.
var botMarkers = []string{
	"bot", "crawl", "spider", "slurp", "headless", "preview", "lighthouse",
	"curl/", "wget", "python-", "go-http-client", "libwww", "java/", "httpclient",
}

// sharedAddressSpace is carrier grade NAT and VPN overlays (RFC 6598), which
// net/netip does not count as private.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

var languageTag = regexp.MustCompile(`^[A-Za-z]{1,8}(-[A-Za-z0-9]{1,8}){0,3}$`)

// Middleware records a view for every GET that answers 200 with an HTML page
// and is not excluded, see the package documentation. It never changes the
// status, the headers or the body, never sets a cookie and never waits for
// Umami. A configuration that counts nothing returns next unchanged.
//
// Like middleware.AccessLog it reads the route pattern the mux sets on the
// request, so it wraps the mux, or sits on the handler inside it, with no
// middleware between that replaces the request. A view with no route pattern
// is not counted, because the path may hold an id or a token. One warning is
// logged per client. A product registers every page, its fallback too,
// through a ServeMux, or sets Options.URL for pages it knows.
//
// Known limits of the response wrapper: it has no ReadFrom, so a handler that
// copies a file loses the sendfile fast path on a counted GET, and a direct
// w.(http.Hijacker) assertion fails. http.ResponseController reaches Hijack
// and every other capability through Unwrap.
func (c *Client) Middleware(next http.Handler) http.Handler {
	return c.counting(next, func(status int, contentType string) bool {
		return status == http.StatusOK && isHTML(contentType)
	})
}

// NotModified records a view for every GET that answers 304, with the same
// rules as Middleware otherwise. A page served with an ETag is revalidated
// on a revisit, and the browser shows its cached copy after a 304, so the
// visit would otherwise go uncounted. A 304 carries no Content-Type, so this
// wraps the handler of an HTML page only, never an asset or a file, and
// Middleware still wraps the mux for the 200 answers. It reads the route
// pattern like Middleware, which the mux has set by the time it calls a
// registered handler. A configuration that counts nothing returns next
// unchanged.
func (c *Client) NotModified(next http.Handler) http.Handler {
	return c.counting(next, func(status int, _ string) bool {
		return status == http.StatusNotModified
	})
}

// counting is the body of Middleware and NotModified: count reports whether
// the answer is a view.
func (c *Client) counting(next http.Handler, count func(status int, contentType string) bool) http.Handler {
	if !c.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, ok := c.eligible(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		rec := &pageWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if !count(rec.status, rec.contentType) {
			return
		}
		route, ok := c.route(r)
		if !ok || excludedPaths[route] {
			return
		}
		v := View{
			URL:       route,
			Referrer:  referrer(r.Header.Get("Referer"), c.hostname),
			Language:  language(r.Header.Get("Accept-Language")),
			UserAgent: truncate(strings.TrimSpace(r.UserAgent()), maxUserAgent),
			IP:        ip,
		}
		if c.title != nil {
			v.Title = truncate(c.title(r), maxTitle)
		}
		c.Track(v)
	})
}

// eligible applies every rule that depends on the request alone and returns
// the client address to report.
func (c *Client) eligible(r *http.Request) (netip.Addr, bool) {
	if r.Method != http.MethodGet || excludedPaths[r.URL.Path] || (c.skip != nil && c.skip(r)) {
		return netip.Addr{}, false
	}
	if signalled(r.Header, "DNT") || signalled(r.Header, "Sec-GPC") {
		return netip.Addr{}, false
	}
	if prefetch(r.Header) || notDocument(r.Header) {
		return netip.Addr{}, false
	}
	ua := strings.TrimSpace(r.UserAgent())
	if ua == "" || isBot(ua) {
		return netip.Addr{}, false
	}
	ip := c.resolver.Resolve(r)
	if !ip.IsValid() || (!c.private && !public(ip)) {
		return netip.Addr{}, false
	}
	return ip, true
}

// signalled reports whether any line of the header, or any comma separated
// part of a line, is "1". A visitor who sent the signal anywhere is not
// counted. Other values, "0" or "yes", are no signal.
func signalled(h http.Header, name string) bool {
	for _, line := range h.Values(name) {
		for _, part := range strings.Split(line, ",") {
			if strings.TrimSpace(part) == "1" {
				return true
			}
		}
	}
	return false
}

// route is the URL reported: the pattern of the route that answered. Without
// a pattern the path is never sent. Options.URL may name the URL then, and
// when it does not, or is not set, the view is not counted and one warning
// says so.
func (c *Client) route(r *http.Request) (string, bool) {
	if r.Pattern != "" {
		return patternPath(r.Pattern), true
	}
	if c.urlFn != nil {
		u := cleanURL(c.urlFn(r))
		return u, u != ""
	}
	c.noPattern.Do(func() {
		c.logger.Warn("no route pattern, view not counted")
	})
	return "", false
}

// cleanURL keeps the path of what Options.URL returned: a query or a fragment
// is cut off, and anything that is not an absolute path is refused.
func cleanURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	// "//host/x" and "/\host/x" are no path: a reader may take them for a
	// host.
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") || strings.HasPrefix(u, `/\`) {
		return ""
	}
	return truncate(u, maxPath)
}

// patternPath turns a ServeMux pattern such as "GET example.test/notes/{id}"
// into the path part "/notes/{id}". The method and the host are dropped and a
// closing {$} is removed. The wildcard names stay, which is the point: the
// value never does.
func patternPath(p string) string {
	p = strings.TrimSpace(p)
	if method, rest, ok := strings.Cut(p, " "); ok && !strings.Contains(method, "/") {
		p = strings.TrimSpace(rest)
	}
	if !strings.HasPrefix(p, "/") {
		i := strings.Index(p, "/")
		if i < 0 {
			return "/"
		}
		p = p[i:]
	}
	p = strings.TrimSuffix(p, "{$}")
	if p == "" {
		return "/"
	}
	return truncate(p, maxPath)
}

// isHTML reports whether a Content-Type header names an HTML page.
func isHTML(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && media == "text/html"
}

// public reports whether a is a visitor on the internet. A private, loopback,
// link local or shared address is a probe, the cluster or a VPN.
func public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !sharedAddressSpace.Contains(a)
}

// prefetch reports a request the browser made ahead of a click that may never
// come.
func prefetch(h http.Header) bool {
	for _, name := range []string{"Sec-Purpose", "Purpose", "X-Purpose", "X-Moz"} {
		if strings.Contains(strings.ToLower(h.Get(name)), "prefetch") {
			return true
		}
	}
	return false
}

// notDocument reports a request whose browser says it is no navigation, such
// as a fetch or an iframe. A browser without the header is not excluded.
func notDocument(h http.Header) bool {
	dest := strings.TrimSpace(h.Get("Sec-Fetch-Dest"))
	return dest != "" && !strings.EqualFold(dest, "document")
}

func isBot(ua string) bool {
	ua = strings.ToLower(ua)
	for _, marker := range botMarkers {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}

// referrer keeps the origin of another site and nothing of its path: a path
// on another site can hold a token. The site's own pages, with or without
// www, are no referrer.
func referrer(raw, ownHost string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	own := strings.ToLower(ownHost)
	if host == own || host == strings.TrimPrefix(own, "www.") || host == "www."+own {
		return ""
	}
	return truncate(u.Scheme+"://"+strings.ToLower(u.Host)+"/", maxPath)
}

// language is the first tag of Accept-Language, or empty when it is no tag.
func language(header string) string {
	first, _, _ := strings.Cut(header, ",")
	first, _, _ = strings.Cut(first, ";")
	first = strings.TrimSpace(first)
	if !languageTag.MatchString(first) {
		return ""
	}
	return first
}

// truncate cuts s to at most n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// pageWriter records the status and the content type of the response. It
// passes everything through unchanged.
type pageWriter struct {
	http.ResponseWriter
	status      int
	contentType string
	sniffed     bool
}

// WriteHeader records the first final status. An informational status such as
// 103 is passed through and does not count as the answer.
func (w *pageWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
		w.contentType = w.Header().Get("Content-Type")
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write records the implicit 200 and, as net/http does, takes the content
// type from the first bytes when the handler set none.
func (w *pageWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	// net/http sniffs at the first byte it sends, so an empty write decides
	// nothing.
	if w.contentType == "" && !w.sniffed && len(b) > 0 {
		w.sniffed = true
		w.contentType = http.DetectContentType(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush passes through to the real writer, which is what a handler that
// asserts http.Flusher expects. A flush sends the header, so it records the
// implicit 200 first. net/http sends no sniffed Content-Type for a flush
// before any body, so a later write does not set one here either.
func (w *pageWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.sniffed = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap exposes the real http.ResponseWriter so http.ResponseController can
// reach Hijack or SetWriteDeadline straight through this wrapper.
func (w *pageWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
