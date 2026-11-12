package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORS answers cross-origin requests for an API whose frontend is served
// from another origin. A same-origin deployment needs none of this and
// must not use it: every origin this middleware is told to allow gets a
// credentialed read of whatever it protects.
//
// Every allowed origin is listed explicitly; a wildcard is never accepted,
// even without credentials, matching current Fetch spec and OWASP CORS
// guidance: a reflected "allow everyone" policy is exactly the mistake this
// package exists to make structurally impossible; the application lists
// its real origins instead of reaching for "*" when something does not
// work. The allowed origin is echoed back verbatim once matched, never a
// wildcard and never the raw Origin header reflected unchecked, so a
// response for one origin can never silently become valid for another.
type CORS struct {
	origins     []string
	methods     string
	headers     string
	credentials bool
	maxAge      string
}

// CORSConfig is what NewCORS validates into a CORS.
type CORSConfig struct {
	// Origins is the exact allow-list: scheme and host, no path, no query,
	// no userinfo, and never a wildcard. The scheme and host are compared
	// case-insensitively, matching how browsers themselves normalize an
	// origin, but the value this middleware echoes back is always the
	// incoming Origin header, byte for byte, so a real browser's own
	// same-origin comparison of the response never sees anything other
	// than the exact value it sent.
	Origins []string
	// Methods defaults to the safe methods plus the write methods a JSON
	// API commonly uses.
	Methods []string
	// Headers defaults to Content-Type only. Add csrf.HeaderName here when
	// the application also uses this module's csrf package, so a
	// cross-origin preflight allows the header that package's protector
	// requires.
	Headers []string
	// Credentials allows the browser to send cookies and HTTP
	// authentication with the request. Per the Fetch spec this can never
	// be combined with a wildcard origin; NewCORS refuses a wildcard
	// regardless, so that combination cannot occur here even by mistake.
	Credentials bool
	// MaxAge caches a preflight answer for this long. Zero sends no
	// Access-Control-Max-Age, so the browser falls back to its own default.
	MaxAge time.Duration
}

// defaultCORSMethods is the method list used when CORSConfig.Methods is
// empty: the safe methods plus the write methods a JSON API commonly uses.
var defaultCORSMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost,
	http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// NewCORS validates cfg and returns the middleware. A wildcard origin is
// refused unconditionally, not only when Credentials is set: without
// credentials a wildcard still hands every site on the internet a read of
// whatever this API answers, and a codebase that accepts it once tends to
// keep it once a cookie is added later. An origin that does not parse as a
// bare scheme://host, including one carrying a path, a query, userinfo, or
// the literal string "null" that a sandboxed or file: context sends, is
// refused for the same reason: it can never be a value this middleware
// would also produce when echoing a match, and a config entry that could
// never match is a mistake worth catching at startup rather than silently
// never firing.
func NewCORS(cfg CORSConfig) (*CORS, error) {
	if len(cfg.Origins) == 0 {
		return nil, errors.New("middleware: cors: at least one origin is required")
	}
	origins := make([]string, 0, len(cfg.Origins))
	for _, raw := range cfg.Origins {
		origin := strings.TrimRight(strings.TrimSpace(raw), "/")
		if origin == "*" {
			return nil, errors.New("middleware: cors: a wildcard origin is never allowed, list the origins")
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.User != nil {
			return nil, fmt.Errorf("middleware: cors: origin %q must be scheme://host: %w", raw, errInvalidOrigin)
		}
		// Stored and compared case-insensitively: browsers normalize the
		// scheme and host of an Origin header to lowercase before sending
		// it, so a configured origin that differs from the request only in
		// letter case would otherwise fail to match despite naming the
		// same origin. That is a false negative, which fails safe rather
		// than open, but it is a cheap footgun to close.
		origins = append(origins, strings.ToLower(parsed.Scheme+"://"+parsed.Host))
	}

	methods := cfg.Methods
	if len(methods) == 0 {
		methods = defaultCORSMethods
	}
	headers := cfg.Headers
	if len(headers) == 0 {
		headers = []string{"Content-Type"}
	}

	c := &CORS{
		origins:     origins,
		methods:     strings.Join(methods, ", "),
		headers:     strings.Join(headers, ", "),
		credentials: cfg.Credentials,
	}
	if cfg.MaxAge > 0 {
		c.maxAge = strconv.Itoa(int(cfg.MaxAge.Seconds()))
	}
	return c, nil
}

// errInvalidOrigin is wrapped into NewCORS' error so a caller can match on
// it with errors.Is if it ever needs to; the message already names the
// offending value.
var errInvalidOrigin = errors.New("invalid origin")

// Middleware answers the preflight and adds the CORS headers to the real
// response.
func (c *CORS) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always, even without a match: a cache must never hand the answer
		// computed for one origin to another.
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
		allowed := origin != "" && c.allows(origin)
		if allowed {
			h := w.Header()
			// The request's own Origin header is echoed, byte for byte,
			// never the request's value left unchecked and never a
			// wildcard: it is only ever sent after it matched the
			// allow-list above.
			h.Set("Access-Control-Allow-Origin", origin)
			if c.credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
		}

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if !allowed {
				// A preflight from an origin that is not on the list gets
				// no headers and no body; the browser then refuses the
				// request it was about to send.
				w.WriteHeader(http.StatusForbidden)
				return
			}
			h := w.Header()
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", c.methods)
			h.Set("Access-Control-Allow-Headers", c.headers)
			if c.maxAge != "" {
				h.Set("Access-Control-Max-Age", c.maxAge)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// allows reports whether origin, exactly as a client sent it, names one of
// the configured origins. The comparison lowercases origin first, matching
// how the allow-list itself was normalized in NewCORS; the literal value
// "null" can never appear in that list, because NewCORS only ever accepts
// an entry that parses as scheme://host, so it can never match here either.
func (c *CORS) allows(origin string) bool {
	return slices.Contains(c.origins, strings.ToLower(origin))
}
