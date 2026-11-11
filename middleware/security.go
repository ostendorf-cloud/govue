package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultHSTS is one year, the minimum a browser preload list submission
// requires.
const DefaultHSTS = 365 * 24 * time.Hour

// DefaultPermissionsPolicy denies every browser feature a consuming
// application has no general use for. One that is actually needed is
// switched on by that application, through its own Security.
const DefaultPermissionsPolicy = "accelerometer=(), browsing-topics=(), camera=(), display-capture=(), " +
	"geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), payment=(), usb=()"

type nonceCtxKey struct{}

// Nonce returns the Content-Security-Policy nonce generated for the
// response being written, or an empty string when the policy carries none.
// A handler puts this on every inline script and style tag it renders;
// nothing else makes those run under DefaultPolicy.
func Nonce(ctx context.Context) string {
	nonce, _ := ctx.Value(nonceCtxKey{}).(string)
	return nonce
}

// Policy is a Content-Security-Policy, ordinarily built once at startup and
// left unchanged afterwards. Build it from DefaultPolicy and widen only the
// directives an application genuinely needs; a directive that is not set at
// all is absent from the header, which for most directives is the same as
// 'none' but written nowhere, so prefer Set over leaving one out by
// omission when the intent is to deny it explicitly.
type Policy struct {
	order  []string
	values map[string][]string
	nonced map[string]bool
}

// NewPolicy returns an empty policy that denies nothing. Prefer
// DefaultPolicy, which denies everything and then allows what this
// framework's own responses need.
func NewPolicy() *Policy {
	return &Policy{values: map[string][]string{}, nonced: map[string]bool{}}
}

// DefaultPolicy denies by default and allows only same-origin scripts,
// styles, images (plus data: URIs), fonts and requests. Inline script and
// style run only with the response's own nonce, so a tag an attacker
// injected does not.
func DefaultPolicy() *Policy {
	return NewPolicy().
		Set("default-src", "'none'").
		Set("script-src", "'self'").
		Set("style-src", "'self'").
		Set("img-src", "'self'", "data:").
		Set("font-src", "'self'").
		Set("connect-src", "'self'").
		Set("form-action", "'self'").
		Set("frame-ancestors", "'none'").
		Set("base-uri", "'none'").
		Set("object-src", "'none'").
		NonceIn("script-src", "style-src")
}

// Set replaces a directive's sources. A directive with no sources, such as
// upgrade-insecure-requests, is written as its name alone.
func (p *Policy) Set(directive string, sources ...string) *Policy {
	if _, ok := p.values[directive]; !ok {
		p.order = append(p.order, directive)
	}
	p.values[directive] = slices.Clone(sources)
	return p
}

// Add appends sources to a directive, adding the directive if it is new.
func (p *Policy) Add(directive string, sources ...string) *Policy {
	if _, ok := p.values[directive]; !ok {
		return p.Set(directive, sources...)
	}
	p.values[directive] = append(p.values[directive], sources...)
	return p
}

// NonceIn marks the directives that carry the per-response nonce.
func (p *Policy) NonceIn(directives ...string) *Policy {
	for _, directive := range directives {
		p.nonced[directive] = true
	}
	return p
}

// NeedsNonce reports whether a nonce has to be generated for a response
// carrying this policy: true once at least one directive is both set and
// marked with NonceIn.
func (p *Policy) NeedsNonce() bool {
	for directive := range p.nonced {
		if _, ok := p.values[directive]; ok {
			return true
		}
	}
	return false
}

// Header renders the policy as the Content-Security-Policy header value.
// The nonce is appended to the directives NonceIn marked; an empty nonce
// renders the policy without one.
func (p *Policy) Header(nonce string) string {
	var b strings.Builder
	for _, directive := range p.order {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(directive)
		for _, source := range p.values[directive] {
			b.WriteString(" ")
			b.WriteString(source)
		}
		if nonce != "" && p.nonced[directive] {
			b.WriteString(" 'nonce-")
			b.WriteString(nonce)
			b.WriteString("'")
		}
	}
	return b.String()
}

// String renders the policy without a nonce.
func (p *Policy) String() string { return p.Header("") }

// Security sets the response headers every answer carries. Build it with
// DefaultSecurity and change only the fields a consuming application has a
// specific, documented reason to change: the defaults are this package's
// product, and weakening one is that application's decision through a
// field, never a change to the default made here.
type Security struct {
	// Policy is the Content-Security-Policy. Nil sends no CSP at all, which
	// is only the right choice for an endpoint that serves no documents,
	// for example a pure JSON API behind its own origin.
	Policy *Policy
	// HSTS is the Strict-Transport-Security max-age. Zero omits the header
	// entirely, which is only right for an application reached over plain
	// HTTP, such as in local development.
	HSTS                  time.Duration
	HSTSIncludeSubDomains bool
	HSTSPreload           bool
	// ReferrerPolicy is the Referrer-Policy header value. Empty omits it.
	ReferrerPolicy string
	// PermissionsPolicy is the Permissions-Policy header value. Empty omits
	// it.
	PermissionsPolicy string
	// FrameOptions is the X-Frame-Options header value, the legacy twin of
	// the policy's frame-ancestors directive for browsers that predate it.
	// Empty omits it.
	FrameOptions string
	// COOP is the Cross-Origin-Opener-Policy header value. Empty omits it.
	COOP string
	// CORP is the Cross-Origin-Resource-Policy header value. Empty omits
	// it.
	CORP string
	// RobotsTag is the X-Robots-Tag header value, sent only when set. It is
	// empty by default; set it for an application whose URLs must never be
	// indexed.
	RobotsTag string
}

// DefaultSecurity is the house baseline: HSTS for a year including
// subdomains and preload, a strict default-deny Content-Security-Policy
// with a per-response nonce for inline script and style, no MIME sniffing,
// no framing, a referrer sent only within the same origin, both
// cross-origin isolation headers set to same-origin, and every browser
// feature this framework has no use for denied. A zero value on a field,
// such as HSTS = 0, Policy = nil or FrameOptions = "", is how a consuming
// application opts out of that one header; nothing here changes what a
// fresh call to DefaultSecurity produces.
func DefaultSecurity() Security {
	return Security{
		Policy:                DefaultPolicy(),
		HSTS:                  DefaultHSTS,
		HSTSIncludeSubDomains: true,
		HSTSPreload:           true,
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		PermissionsPolicy:     DefaultPermissionsPolicy,
		FrameOptions:          "DENY",
		COOP:                  "same-origin",
		CORP:                  "same-origin",
	}
}

// Middleware returns the header middleware. It belongs outside the router,
// so an error answer carries the same headers as a page.
func (s Security) Middleware(next http.Handler) http.Handler {
	static := s.staticHeaders()
	nonceNeeded := s.Policy != nil && s.Policy.NeedsNonce()
	policyWithoutNonce := ""
	if s.Policy != nil && !nonceNeeded {
		policyWithoutNonce = s.Policy.String()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for name, value := range static {
			h.Set(name, value)
		}
		switch {
		case nonceNeeded:
			nonce := newNonce()
			h.Set("Content-Security-Policy", s.Policy.Header(nonce))
			r = r.WithContext(context.WithValue(r.Context(), nonceCtxKey{}, nonce))
		case policyWithoutNonce != "":
			h.Set("Content-Security-Policy", policyWithoutNonce)
		}
		next.ServeHTTP(w, r)
	})
}

// staticHeaders is everything that does not change per response.
func (s Security) staticHeaders() map[string]string {
	headers := map[string]string{"X-Content-Type-Options": "nosniff"}
	if s.HSTS > 0 {
		value := "max-age=" + strconv.Itoa(int(s.HSTS.Seconds()))
		if s.HSTSIncludeSubDomains {
			value += "; includeSubDomains"
		}
		if s.HSTSPreload {
			value += "; preload"
		}
		headers["Strict-Transport-Security"] = value
	}
	for name, value := range map[string]string{
		"Referrer-Policy":              s.ReferrerPolicy,
		"Permissions-Policy":           s.PermissionsPolicy,
		"X-Frame-Options":              s.FrameOptions,
		"Cross-Origin-Opener-Policy":   s.COOP,
		"Cross-Origin-Resource-Policy": s.CORP,
		"X-Robots-Tag":                 s.RobotsTag,
	} {
		if value != "" {
			headers[name] = value
		}
	}
	return headers
}

// newNonce is 16 random bytes, fresh per response. Reusing one, or caching a
// response that carries one, defeats the directive entirely.
func newNonce() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Without randomness no nonce may be sent: a predictable one is
		// worse than none, and the inline tags then simply do not run.
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(buf[:])
}
