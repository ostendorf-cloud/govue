// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// AppOrigins is the list of origins a native app sends its requests from,
// such as the bundled origin of a Capacitor webview. It is a type of its own,
// next to CORS and not a field of it, for one reason: an app origin must
// never be passed where a web origin goes, and a distinct type helps to keep
// the two apart. That is a convention helped by the type, not a guarantee:
// [AppOrigins.List] returns a plain []string, which the compiler would accept
// in [CORSConfig.Origins] or csrf.Protector.TrustOrigin. What is structural is
// the web CORS list: [NewAppCORS] takes the web [CORS] and refuses to build
// when the two lists overlap. The application passes [AppOrigins.List] to
// devicehttp's IssueTrustedOrigins and to csrf.Protector.TrustOriginOnRoute
// for the one issue route, and nowhere else.
//
// The zero value holds no origin and is refused by [NewAppCORS].
type AppOrigins struct {
	origins []string
}

// reservedAppSchemes can never be a custom scheme: https is always allowed on
// its own, http is refused for an app on purpose, and the rest are schemes a
// browser handles itself, which no app bundle origin is.
var reservedAppSchemes = []string{
	"http", "https", "file", "data", "javascript", "blob", "about", "ftp", "ws", "wss", "filesystem", "chrome",
	"chrome-extension", "moz-extension", "safari-web-extension", "view-source", "vbscript", "intent", "content",
}

// NewAppOrigins validates origins and returns the list. Each entry is exactly
// scheme://host or scheme://host:port, written the way a browser sends it:
// lower case, with no path, no trailing slash, no query, no fragment and no
// userinfo. The host is ASCII: a browser sends an internationalised host as
// punycode, so a Unicode host never matches and is an error. A port is
// numeric, from 1 to 65535, without a leading zero, and never the default port
// 443 of https, which a browser leaves out. An empty port is an error. The
// scheme is https, or one of customSchemes. A wildcard, the
// literal "null", http, an empty list and a duplicate are errors, and so is a
// custom scheme that is not a lower case URL scheme or that is one of the
// schemes a browser handles itself. Every error names the offending value.
//
// A custom scheme is an opt in of one scheme with exact origins, never a
// scheme wide rule: "capacitor" with "capacitor://localhost" lists that one
// origin and nothing else.
//
// No normalisation happens: a value that differs from what a browser sends is
// an error, so the list the operator reads is the list that matches.
func NewAppOrigins(origins []string, customSchemes ...string) (AppOrigins, error) {
	if len(origins) == 0 {
		return AppOrigins{}, errors.New("middleware: at least one app origin is required")
	}
	for _, scheme := range customSchemes {
		if !validCustomScheme(scheme) {
			return AppOrigins{}, fmt.Errorf("middleware: app scheme %q must be a lower case URL scheme "+
				"that a browser does not handle itself: %w", scheme, errInvalidOrigin)
		}
	}

	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		if err := checkAppOrigin(origin, customSchemes); err != nil {
			return AppOrigins{}, err
		}
		if slices.Contains(out, origin) {
			return AppOrigins{}, fmt.Errorf("middleware: app origin %q is listed twice: %w", origin, errInvalidOrigin)
		}
		out = append(out, origin)
	}

	return AppOrigins{origins: out}, nil
}

// checkAppOrigin applies the rules of NewAppOrigins to one entry.
func checkAppOrigin(origin string, customSchemes []string) error {
	bad := func(why string) error {
		return fmt.Errorf("middleware: app origin %q %s: %w", origin, why, errInvalidOrigin)
	}
	if strings.Contains(origin, "*") {
		return bad("holds a wildcard, list exact origins")
	}
	if origin == "null" {
		return bad("is the null origin, which any sandboxed page sends")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return bad("must be scheme://host")
	}
	// The one comparison that rejects a path, a trailing slash, a query, a
	// fragment, userinfo, an upper case letter and any other spelling a
	// browser would not send: the origin must be what it parses to.
	if origin != parsed.Scheme+"://"+parsed.Host || origin != strings.ToLower(origin) {
		return bad("must be lower case scheme://host with no path, query, fragment or userinfo")
	}
	if parsed.Scheme != "https" && !slices.Contains(customSchemes, parsed.Scheme) {
		return bad("has a scheme that is neither https nor a listed custom scheme")
	}
	for i := range len(origin) {
		if origin[i] >= utf8.RuneSelf {
			return bad("has a host that is not ASCII, a browser sends punycode")
		}
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return bad("has an empty port")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || port[0] == '0' {
			return bad("has a port a browser never sends, use 1 to 65535 without a leading zero")
		}
		if parsed.Scheme == "https" && n == 443 {
			return bad("names the default port 443, which a browser leaves out")
		}
	}

	return nil
}

// validCustomScheme reports whether s is a lower case URL scheme (RFC 3986)
// outside the reserved list.
func validCustomScheme(s string) bool {
	if s == "" || slices.Contains(reservedAppSchemes, s) {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}

	return true
}

// List returns a copy of the origins, in the order they were given. It is for
// devicehttp's IssueTrustedOrigins and csrf.Protector.TrustOriginOnRoute for the
// issue route, and for nothing else: never for csrf.Protector.TrustOrigin and
// never for [CORSConfig.Origins]. The compiler would accept it there, so this
// is a rule the caller keeps.
func (a AppOrigins) List() []string { return slices.Clone(a.origins) }

// Contains reports whether origin, byte for byte as a client sent it, is on
// the list. A different letter case does not match: a browser sends lower case,
// and failing to match fails safe.
func (a AppOrigins) Contains(origin string) bool { return slices.Contains(a.origins, origin) }

// RejectOverlap returns an error that names the first app origin that is also
// on another list. [NewAppCORS] already refuses an overlap with the web
// [CORS] it is given. What stays a call of the application is the list this
// package cannot see: call it once at startup with the origins passed to
// csrf.Protector.TrustOrigin, and refuse to start on an error. An app origin on
// either list would let a page of that origin ride a cookie, because both of
// those lists carry credentials. The webCORS argument is for a list that does
// not go through a [CORS] value.
//
// The other lists are compared the way their consumers read them: surrounding
// space and trailing slashes dropped and letters lower cased, as NewCORS
// normalises. Pass every list a web origin can enter through; this function
// cannot see a list it is not given.
func (a AppOrigins) RejectOverlap(webCORS, csrfTrusted []string) error {
	for _, named := range []struct {
		name    string
		origins []string
	}{{"web CORS list", webCORS}, {"CSRF trusted origins", csrfTrusted}} {
		for _, other := range named.origins {
			normal := strings.ToLower(strings.TrimRight(strings.TrimSpace(other), "/"))
			if a.Contains(normal) {
				return fmt.Errorf("middleware: app origin %q is also on the %s, an app origin "+
					"must never be a web or trusted origin", normal, named.name)
			}
		}
	}

	return nil
}

// Methods, headers and cache time of the app preflight. The methods are the
// ones the device token routes use: POST to issue and rotate, GET to list,
// DELETE to sign out and revoke. Authorization carries the bearer token and
// Content-Type the JSON body.
const (
	appCORSMethods = "GET, POST, DELETE"
	appCORSHeaders = "Authorization, Content-Type"
	// appCORSMaxAge is ten minutes, in seconds. It is a bound set here, so a
	// changed policy does not outlive its change for long, and not left to
	// the browser's own cap.
	appCORSMaxAge = "600"
)

// AppCORS is the one CORS handler of a deployment with native apps. It holds
// the app policy and the web [CORS] policy and routes by the Origin header: a
// request whose Origin is on the app list is answered by the app policy and
// goes to the next handler directly, never through the web policy. Any other
// request goes through the web policy, or straight on when there is none. It
// replaces [CORS.Middleware] in the chain and does not sit in front of it, so
// no origin gets the answer of both. [NewAppCORS] refuses a web policy whose
// origin list overlaps the app list, so the web policy could not answer an
// app origin even if the routing were bypassed.
//
// It never sends Access-Control-Allow-Credentials for an app origin: an app
// authenticates with a bearer token, which is not a cookie, so credentials
// mode stays off and a page of an app origin cannot ride a cookie even if it
// tried. It sends no Access-Control header of its own for an origin that is not
// listed, and never echoes one.
//
// Not sent: Access-Control-Expose-Headers. The app policy is fixed and has no
// configuration, and [CORSConfig.ExposeHeaders] belongs to the web policy and
// reaches web origins only, so a page of an app origin reads the CORS
// safelisted response headers only and cannot read Retry-After. Adding a field
// here would change the signature of [NewAppCORS], a breaking change, so it
// waits for an app that needs it. Access-Control-Allow-
// Private-Network is not sent either: a preflight with
// Access-Control-Request-Private-Network gets the answer without it, and a
// browser that enforces Private Network Access, such as Chrome, then fails the
// request when the API is on a private network. That is not decided here.
type AppCORS struct {
	origins AppOrigins
	web     *CORS
}

// NewAppCORS returns the CORS handler for origins and the web policy web, which
// may be nil when the deployment has no web origin. It returns an error for the
// zero AppOrigins and for an app origin that is also an origin of web, naming
// the origin. The comparison is the one of [AppOrigins.RejectOverlap].
func NewAppCORS(origins AppOrigins, web *CORS) (*AppCORS, error) {
	if len(origins.origins) == 0 {
		return nil, errors.New("middleware: the app CORS handler needs at least one app origin")
	}
	if web != nil {
		if err := origins.RejectOverlap(web.origins, nil); err != nil {
			return nil, err
		}
	}

	return &AppCORS{origins: origins, web: web}, nil
}

// Middleware answers a preflight of an app origin and adds the CORS headers to
// the real response of an app origin, with one Vary: Origin. Any other request
// is answered by the web policy.
func (c *AppCORS) Middleware(next http.Handler) http.Handler {
	var other http.Handler
	if c.web != nil {
		// The web policy adds its own Vary: Origin.
		other = c.web.Middleware(next)
	} else {
		other = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Always, even without a match: a cache must never hand the answer
			// computed for an app origin to another origin.
			w.Header().Add("Vary", "Origin")
			next.ServeHTTP(w, r)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !c.origins.Contains(origin) {
			other.ServeHTTP(w, r)

			return
		}

		h := w.Header()
		h.Add("Vary", "Origin")
		// The matched Origin, byte for byte: it equals a listed value.
		h.Set("Access-Control-Allow-Origin", origin)

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			// Answered here, before the rate limit, the CSRF protector and any
			// session read, like the web policy does.
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", appCORSMethods)
			h.Set("Access-Control-Allow-Headers", appCORSHeaders)
			h.Set("Access-Control-Max-Age", appCORSMaxAge)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		next.ServeHTTP(w, r)
	})
}
