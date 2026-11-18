// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// AppOrigins is the list of origins a native app sends its requests from,
// such as the bundled origin of a Capacitor webview. It is a type of its own,
// next to CORS and not a field of it, for one reason: an app origin must
// never be passed where a web origin goes, and a distinct type makes that a
// compile error instead of a convention. Nothing here converts it to a plain
// list for csrf.Protector.TrustOrigin or for [CORSConfig.Origins]. The
// application passes [AppOrigins.List] to devicehttp's IssueTrustedOrigins and
// to [NewAppCORS], and nowhere else.
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
}

// NewAppOrigins validates origins and returns the list. Each entry is exactly
// scheme://host or scheme://host:port, written the way a browser sends it:
// lower case, with no path, no trailing slash, no query, no fragment and no
// userinfo. The scheme is https, or one of customSchemes. A wildcard, the
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
// devicehttp's IssueTrustedOrigins and for nothing else: never for
// csrf.Protector.TrustOrigin and never for [CORSConfig.Origins].
func (a AppOrigins) List() []string { return slices.Clone(a.origins) }

// Contains reports whether origin, byte for byte as a client sent it, is on
// the list. A different letter case does not match: a browser sends lower case,
// and failing to match fails safe.
func (a AppOrigins) Contains(origin string) bool { return slices.Contains(a.origins, origin) }

// RejectOverlap returns an error that names the first app origin that is also
// on another list. Call it once at startup, with the list of the web CORS
// policy and the origins passed to csrf.Protector.TrustOrigin, and refuse to
// start on an error. An app origin on either list would let a page of that
// origin ride a cookie, because both of those lists carry credentials.
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

// AppCORS answers the CORS protocol for the origins of a native app. It is
// the app half of a two policy setup and never replaces the web policy: a
// request whose Origin is on the app list is answered here and goes no
// further than this handler's own headers, any other request passes to the
// next handler untouched. Put it in front of [CORS.Middleware]. The lists
// are disjoint, see [AppOrigins.RejectOverlap], so the web policy never sees
// an app origin it would answer differently.
//
// It never sends Access-Control-Allow-Credentials: an app authenticates with
// a bearer token, which is not a cookie, so credentials mode stays off and a
// page of an app origin cannot ride a cookie even if it tried. It sends no
// Access-Control header for an origin that is not listed, and never echoes one.
type AppCORS struct {
	origins AppOrigins
}

// NewAppCORS returns the app CORS handler for origins. It returns an error for
// the zero AppOrigins.
func NewAppCORS(origins AppOrigins) (*AppCORS, error) {
	if len(origins.origins) == 0 {
		return nil, errors.New("middleware: the app CORS handler needs at least one app origin")
	}

	return &AppCORS{origins: origins}, nil
}

// Middleware answers a preflight of an app origin and adds the CORS headers to
// the real response of an app origin. Any other request is passed on with
// Vary: Origin and no other header from this handler.
func (c *AppCORS) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Always, even without a match: a cache must never hand the answer
		// computed for an app origin to another origin. This is the only
		// header an unlisted origin gets from this handler.
		h.Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
		if origin == "" || !c.origins.Contains(origin) {
			next.ServeHTTP(w, r)

			return
		}

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
