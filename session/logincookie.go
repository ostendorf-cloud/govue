// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"fmt"
	"net/http"
	"time"
)

// Defaults for the login cookie. Each is the value a left zero [CodecConfig]
// field takes.
const (
	// DefaultLoginCookieName carries the __Host- prefix for the same reason
	// [DefaultCookieName] does: a browser only accepts it over https, for this
	// exact host, with Path=/ and no Domain, so a sibling host cannot plant the
	// state of a login this application is about to finish.
	DefaultLoginCookieName = "__Host-login"

	// DefaultLoginLifetime is how long a login has to come back from the
	// identity provider. It is long enough for somebody to read a consent
	// screen, type a password and answer a second factor, and short enough that
	// a signed value nothing can revoke is not worth stealing for later.
	DefaultLoginLifetime = 10 * time.Minute
)

// LoginCookieName is the name the login cookie is sent under, after the
// __Host- prefix has been dropped for a plain HTTP run. It mirrors
// [Manager.CookieName].
func (c *Codec) LoginCookieName() string { return c.loginCookieName }

// SetLoginCookie signs value and sets the login cookie, for the handler that
// is about to redirect a browser to the identity provider. value is whatever
// the callback needs to finish the login, an oidc.Login in practice.
//
// The lifetime is [CodecConfig.LoginLifetime], so no caller has to pick one,
// and it is inside the signed value rather than only on the cookie: a browser
// that keeps the cookie past its Max-Age, or sends it back after the clock
// moved, still gets a value this Codec refuses.
func (c *Codec) SetLoginCookie(w http.ResponseWriter, value any) error {
	encoded, err := c.Encode(value, c.loginLifetime)
	if err != nil {
		return err
	}
	cookie := c.loginCookie()
	cookie.Value = encoded
	http.SetCookie(w, cookie)
	return nil
}

// ReadLoginCookie reads the login cookie, decodes it into into, and clears the
// cookie whatever happens: on a successful read, on a value that does not
// verify and on a request that carries no cookie at all.
//
// That is the point of this method. A login in flight is good for exactly one
// callback, so single use is the default rather than something an application
// has to remember to do, and the clear happens before anything can go wrong
// further down the handler. Replaying a callback URL then finds no login to
// finish, and a login that failed leaves the browser with nothing to send
// again.
//
// Every failure is [ErrInvalid], including a missing and an empty cookie. A
// caller cannot tell the two apart on purpose: there is nothing it could do
// differently, and one code path is one place to get the refusal right.
func (c *Codec) ReadLoginCookie(w http.ResponseWriter, r *http.Request, into any) error {
	c.ClearLoginCookie(w)

	cookie, err := r.Cookie(c.loginCookieName)
	if err != nil || cookie.Value == "" {
		return fmt.Errorf("session: login cookie: %w", ErrInvalid)
	}
	if err := c.Decode(cookie.Value, into); err != nil {
		return fmt.Errorf("session: login cookie: %w", err)
	}
	return nil
}

// ClearLoginCookie deletes the login cookie. [Codec.ReadLoginCookie] already
// does it, so this is for the paths that never get that far: a callback refused
// before the cookie is looked at, or a login abandoned halfway through being
// started.
func (c *Codec) ClearLoginCookie(w http.ResponseWriter) {
	cookie := c.loginCookie()
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
}

// loginCookie is the login cookie with no value yet. The attributes come from
// the same place the session cookie's do, so the two cannot drift apart, with
// two deliberate differences.
//
// SameSite is always Lax and is not configurable. The identity provider
// redirects the browser back to the callback, which is a cross site top level
// navigation, and Strict withholds a cookie from exactly that. A Strict login
// cookie would therefore break every single login rather than harden it, which
// is why [Config.SameSiteStrict] has no counterpart here.
//
// There is no Max-Age, whatever [Config.Persistent] says about the session
// cookie. The lifetime that matters is the short one signed into the value, and
// a login in flight surviving the browser being closed buys nothing: the person
// is not coming back to a provider redirect from last week.
func (c *Codec) loginCookie() *http.Cookie {
	return cookie(c.loginCookieName, c.allowPlainHTTP, http.SameSiteLaxMode)
}
