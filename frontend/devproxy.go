// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/clientip"
)

// The bounds of the development API proxy.
const (
	// DevAPIProxyMaxBody is the default of Config.DevAPIMaxBody: the largest
	// request body the development proxy forwards. A larger one answers 413.
	DevAPIProxyMaxBody = 1 << 20
	// DevAPIProxyMaxBodyLimit is the largest value Config.DevAPIMaxBody
	// accepts. It keeps a typo from turning the cap into no cap.
	DevAPIProxyMaxBodyLimit = 256 << 20
	// devAPIDialTimeout bounds the connection to the API.
	devAPIDialTimeout = 5 * time.Second
	// devAPIHeaderTimeout bounds the wait for the API's response header.
	devAPIHeaderTimeout = 30 * time.Second
)

// devAPIPrefix is the one path prefix the development proxy forwards.
const devAPIPrefix = "/api/"

// singleLabel is a host name of one label, such as a compose service name.
var singleLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// frontendSecurityHeaders are the headers the frontend's own security
// middleware sets. The API's copies are dropped from a proxied response, so
// the frontend's apply alone and none is sent twice.
var frontendSecurityHeaders = []string{
	"Content-Security-Policy", "Content-Security-Policy-Report-Only", "Strict-Transport-Security",
	"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Permissions-Policy",
	"Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy", "X-Robots-Tag", "X-Request-Id",
}

// checkDevAPIProxy validates Config.DevAPIProxy and returns the target. An
// empty value is no proxy. A refused value is never quoted and no error of
// url.Parse, which quotes it, is wrapped.
func checkDevAPIProxy(raw string, development bool) (*url.URL, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // no proxy configured is not an error
	}
	if !development {
		return nil, errors.New("frontend: Config.DevAPIProxy is for development only and needs " +
			"Config.Development (the value is not shown)")
	}
	const form = "an http or https URL of a loopback address, localhost or a single label host " +
		"such as a compose service name, with an optional port and path prefix, and no userinfo, " +
		"query or fragment (the value is not shown)"
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("frontend: Config.DevAPIProxy must be " + form)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https", u.Opaque != "", u.User != nil,
		u.RawQuery != "", u.ForceQuery, u.Fragment != "", u.RawFragment != "":
		return nil, errors.New("frontend: Config.DevAPIProxy must be " + form)
	case !devHostOK(u.Hostname()):
		return nil, errors.New("frontend: Config.DevAPIProxy names a host that is neither loopback nor a " +
			"single label, it must be " + form)
	case !devPrefixOK(u.EscapedPath()):
		return nil, errors.New("frontend: Config.DevAPIProxy has a path that is not a clean prefix, it must be " + form)
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("frontend: Config.DevAPIProxy has an empty port, it must be " + form)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, errors.New("frontend: Config.DevAPIProxy has a port outside 1 to 65535, it must be " + form)
		}
	}
	return u, nil
}

// checkDevAPIMaxBody validates Config.DevAPIMaxBody and returns the limit in
// bytes. Zero is the default, DevAPIProxyMaxBody.
func checkDevAPIMaxBody(n int64) (int64, error) {
	switch {
	case n == 0:
		return DevAPIProxyMaxBody, nil
	case n < 0 || n > DevAPIProxyMaxBodyLimit:
		return 0, fmt.Errorf("frontend: Config.DevAPIMaxBody must be between 1 and %d bytes, or 0 for the default",
			DevAPIProxyMaxBodyLimit)
	}
	return n, nil
}

// devHostOK reports whether host is loopback, localhost or one label.
func devHostOK(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().IsLoopback() && addr.Zone() == ""
	}
	return singleLabel.MatchString(host) && !numericLabel(host)
}

// numericLabel reports whether a label is a number some resolvers read as an
// IPv4 address: all decimal digits, which covers octal with a leading 0, or
// 0x followed by hex digits only. The macOS resolver turns 167772161 and
// 0x0a000001 into 10.0.0.1 and 2130706433 into 127.0.0.1. Dotted forms such
// as 0x7f.1 hold a dot and fail the single label rule already.
func numericLabel(label string) bool {
	isDigit := func(r rune) bool { return r >= '0' && r <= '9' }
	isHex := func(r rune) bool { return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') }
	if strings.IndexFunc(label, func(r rune) bool { return !isDigit(r) }) < 0 {
		return true
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(label), "0x"); ok {
		return strings.IndexFunc(rest, func(r rune) bool { return !isHex(r) }) < 0
	}
	return false
}

// devPrefixOK reports whether an escaped path is empty, / or plain segments
// with no trailing slash and no escape at all.
func devPrefixOK(p string) bool {
	if p == "" || p == "/" {
		return true
	}
	if !strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "%") {
		return false
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if !plainSegment(seg) {
			return false
		}
	}
	return true
}

// newDevAPIProxy forwards /api/ to target. It strips the inbound forwarding
// headers and sets X-Forwarded-For from the trusted client IP resolver
// alone, bounds the body and the timeouts, drops the API's copies of the
// frontend's security headers and answers a failure with one opaque 502.
func newDevAPIProxy(target *url.URL, transport http.RoundTripper, resolver *clientip.Resolver,
	logger *slog.Logger, maxBody int64,
) http.Handler {
	if transport == nil {
		dialer := &net.Dialer{Timeout: devAPIDialTimeout}
		transport = &http.Transport{
			// No proxy from the environment: the request goes to the target.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   devAPIDialTimeout,
			ResponseHeaderTimeout: devAPIHeaderTimeout,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          16,
		}
	}
	proxy := &httputil.ReverseProxy{
		// Rewrite, unlike Director, removes the inbound Forwarded-for headers
		// X-Forwarded-For, -Host and -Proto before it runs, and the hop by
		// hop headers are removed by ReverseProxy itself.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			h := pr.Out.Header
			h.Del("Forwarded")
			h.Del("X-Real-Ip")
			h.Del("Via")
			if addr := resolver.Resolve(pr.In); addr.IsValid() {
				h.Set("X-Forwarded-For", addr.String())
			}
			h.Set("X-Forwarded-Host", pr.In.Host)
			proto := "http"
			if pr.In.TLS != nil {
				proto = "https"
			}
			h.Set("X-Forwarded-Proto", proto)
		},
		Transport: transport,
		ModifyResponse: func(res *http.Response) error {
			for _, name := range frontendSecurityHeaders {
				res.Header.Del(name)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			// The cause stays here: it may name the target or carry what the
			// API said. The log line holds its type only.
			if !errors.Is(err, context.Canceled) {
				logger.WarnContext(r.Context(), "dev api proxy failed", "error_type", fmt.Sprintf("%T", err))
			}
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		proxy.ServeHTTP(w, r)
	})
}

// withDevAPIProxy sends a request under /api/ to api, on a mux of its own so
// the access log and the metrics see the pattern /api/, and every other
// request to site, the counted mux. A proxied request never reaches the
// page view counter.
func withDevAPIProxy(site, api http.Handler, route func(http.Handler) http.Handler) http.Handler {
	apiMux := http.NewServeMux()
	apiMux.Handle(devAPIPrefix, route(api))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, devAPIPrefix) {
			apiMux.ServeHTTP(w, r)
			return
		}
		site.ServeHTTP(w, r)
	})
}
