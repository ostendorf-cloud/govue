// Package session keeps a login server side: the cookie carries nothing but an
// opaque random id, and the state lives behind a [Store].
//
// That is what makes a session endable. A self contained signed cookie needs no
// store and keeps every replica stateless, but nothing can take it back: a
// logout, a disabled account and a leaked cookie are all unenforceable until it
// expires on its own. Here the id is one lookup away from being gone.
//
// Two timeouts apply. The idle one ends a session that is not used for a while,
// so a forgotten tab does not stay logged in. The absolute one ends it a fixed
// time after the login however active it is, so no session lives forever.
//
// A [Manager] owns the cookie and issues a new id on every login, and [Manager.Rotate]
// issues one on a privilege change inside a login. Both are the same defence:
// an id an attacker planted before the change is not the id that comes out of it.
//
// [Codec] is separate and store free, for the one thing a store is wrong for:
// the state, nonce and PKCE verifier of a login that has not happened yet, which
// have to survive the redirect to the identity provider before any session
// exists to hold them.
//
// A session id is a plain string, so a CSRF package can bind a token to it
// without either package importing the other:
//
//	token := csrf.Token(key, s.ID)
//
// Nothing here binds a session to a client IP or a user agent. Both change mid
// session on a mobile network, behind a carrier NAT and across a VPN, so the
// check logs out real users at a rate the attacks it catches do not justify. An
// application that wants it anyway has [Session.Data] to keep the value in.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Defaults an application starts from. Each is the value a left zero [Config]
// field takes.
const (
	// DefaultCookieName carries the __Host- prefix, which a browser only
	// accepts over https, for this exact host, with Path=/ and no Domain. That
	// is what stops a sibling host, or a host that merely resolved to this one
	// for a moment, from setting a session cookie for this application. The
	// name says nothing about the framework behind it on purpose.
	DefaultCookieName = "__Host-session"
	// DefaultIdle ends a session unused for this long.
	DefaultIdle = 2 * time.Hour
	// DefaultLifetime ends a session this long after the login.
	DefaultLifetime = 12 * time.Hour
	// DefaultTouchEvery keeps the idle timeout from writing to the store on
	// every single request.
	DefaultTouchEvery = time.Minute
	// hostPrefix is required to come with Secure, so it is dropped together
	// with it.
	hostPrefix = "__Host-"
	// idBytes is 256 bits of entropy, well past the 64 bits a session id needs
	// to be out of brute force range, and cheap enough not to economise on.
	idBytes = 32
)

// Session is one login.
//
// Subject is the identity provider's stable id for the account and is what
// [Manager.EndAll] matches on, so it has to be set for a password change or a
// disabled account to be enforceable. Data is the application's own and is kept
// small: it travels with every read of the session.
//
// The three timestamps are the manager's, not the caller's. Whatever a caller
// sets them to is overwritten on [Manager.Start].
type Session struct {
	ID       string
	Subject  string
	Username string
	Name     string
	Email    string
	Groups   []string
	Data     map[string]string

	// IssuedAt is when the login happened. It does not move on a rotation.
	IssuedAt time.Time
	// LastSeen is the last use the store was told about, which is at most
	// Config.TouchEvery behind the real one.
	LastSeen time.Time
	// ExpiresAt is the absolute end of the session. A rotation does not
	// extend it.
	ExpiresAt time.Time
}

// In reports whether the account is in the group.
func (s Session) In(group string) bool { return slices.Contains(s.Groups, group) }

// Config configures a [Manager]. Store is the only required field; every other
// one is secure when left zero.
type Config struct {
	// Store keeps the session state. Required.
	Store Store

	// CookieName defaults to [DefaultCookieName].
	CookieName string

	// AllowPlainHTTP drops the Secure attribute, and with it the __Host-
	// prefix the attribute is a precondition of, so a session works on a plain
	// HTTP development run. It sends the session id in the clear. Never set it
	// on anything reachable from outside the machine it runs on.
	AllowPlainHTTP bool

	// SameSiteStrict sends the cookie on same site requests only.
	//
	// The default is Lax, which withholds the cookie from a cross site form
	// post but keeps it on a top level navigation, so following a link into
	// the application does not land on a logged out page. Strict is the
	// stricter of the two values worth having and costs exactly that
	// navigation, which an application whose entry points are all inside
	// itself does not need.
	SameSiteStrict bool

	// Persistent gives the cookie a Max-Age, so it survives the browser being
	// closed, bounded by Lifetime.
	//
	// The default is a cookie with neither Max-Age nor Expires, which the
	// browser drops when it closes. The server side absolute lifetime is
	// authoritative either way, so the attribute buys an application nothing
	// but a session id sitting on disk for longer.
	Persistent bool

	// Idle ends a session that is not used for this long. Zero is
	// [DefaultIdle].
	Idle time.Duration

	// Lifetime ends a session this long after the login, however active it is.
	// Zero is [DefaultLifetime].
	Lifetime time.Duration

	// TouchEvery is how often an active session's last use is written back to
	// the store, which is what keeps a read from being a write. It has to be
	// shorter than Idle: a session whose last use is written back less often
	// than it is allowed to sit idle would be touched for the first time after
	// it had already expired. Zero is [DefaultTouchEvery].
	TouchEvery time.Duration

	// Now is the clock. Zero is [time.Now]. Tests replace it.
	Now func() time.Time
}

// Manager issues, reads and ends sessions, and owns the cookie. It is safe for
// concurrent use.
type Manager struct {
	cfg Config
}

// New validates the configuration and fills in the defaults.
func New(cfg Config) (*Manager, error) {
	if cfg.CookieName == "" {
		cfg.CookieName = DefaultCookieName
	}
	if cfg.Idle == 0 {
		cfg.Idle = DefaultIdle
	}
	if cfg.Lifetime == 0 {
		cfg.Lifetime = DefaultLifetime
	}
	if cfg.TouchEvery == 0 {
		cfg.TouchEvery = DefaultTouchEvery
	}

	var problems []error
	if cfg.Store == nil {
		problems = append(problems, errors.New("store is required"))
	}
	if cfg.Idle < 0 {
		problems = append(problems, fmt.Errorf("idle timeout %s is negative", cfg.Idle))
	}
	if cfg.Lifetime < 0 {
		problems = append(problems, fmt.Errorf("lifetime %s is negative", cfg.Lifetime))
	}
	if cfg.TouchEvery < 0 {
		problems = append(problems, fmt.Errorf("touch interval %s is negative", cfg.TouchEvery))
	}
	if cfg.TouchEvery > 0 && cfg.Idle > 0 && cfg.TouchEvery >= cfg.Idle {
		problems = append(problems, fmt.Errorf(
			"touch interval %s is not shorter than the idle timeout %s, so a session would expire before its first touch",
			cfg.TouchEvery, cfg.Idle))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	if cfg.AllowPlainHTTP {
		// A browser refuses a __Host- cookie that does not come with Secure,
		// so keeping the prefix on a plain HTTP run would mean no session at
		// all rather than a less protected one.
		cfg.CookieName = strings.TrimPrefix(cfg.CookieName, hostPrefix)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Manager{cfg: cfg}, nil
}

// CookieName is the name the session cookie is sent under, after the __Host-
// prefix has been dropped for a plain HTTP run.
func (m *Manager) CookieName() string { return m.cfg.CookieName }

// Start creates a session for an account that has just authenticated and sets
// the cookie. The id is new, which is the renewal a login needs: a session an
// attacker fixed beforehand is not the one the login produces.
//
// The id, the issue time, the last use and the expiry on s are overwritten. The
// session as stored is returned.
func (m *Manager) Start(ctx context.Context, w http.ResponseWriter, s Session) (Session, error) {
	id, err := NewID()
	if err != nil {
		return Session{}, err
	}

	now := m.cfg.Now().UTC()
	s.ID = id
	s.IssuedAt = now
	s.LastSeen = now
	s.ExpiresAt = now.Add(m.cfg.Lifetime)

	if err := m.cfg.Store.Save(ctx, s); err != nil {
		return Session{}, fmt.Errorf("session: save: %w", err)
	}
	m.setCookie(w, s)
	return s, nil
}

// Rotate issues a new id for a session that already exists and drops the old
// one, for a privilege change inside a login: a second factor, a step up, an
// account switch.
//
// The absolute expiry and the issue time do not move, so rotating is not a way
// to extend a session past its lifetime.
//
// The session has to still exist: the store is asked for the current id first,
// and a rotation of an id it no longer holds is refused with an error wrapping
// [ErrNotFound]. Without that, a caller holding a [Session] value read before a
// [Manager.EndAll] would write it back and undo the end, which is the one thing
// a disabled account must not survive. [Manager.Start] is deliberately exempt:
// a fresh login writes whatever the authentication just established.
func (m *Manager) Rotate(ctx context.Context, w http.ResponseWriter, s Session) (Session, error) {
	previous := s.ID
	if previous != "" {
		if _, err := m.cfg.Store.Get(ctx, previous); err != nil {
			return Session{}, fmt.Errorf("session: rotate: %w", err)
		}
	}

	id, err := NewID()
	if err != nil {
		return Session{}, err
	}

	s.ID = id
	s.LastSeen = m.cfg.Now().UTC()

	// The new session is saved first, so a failed write leaves the old one
	// intact rather than destroying the login. The old id is dropped next, and
	// only then is the new one promised to the client: a cookie set in between
	// would mean handing out a second valid credential before knowing the first
	// one is gone, and it buys nothing, because http.SetCookie only writes a
	// header and nothing reaches the client until the handler returns.
	if err := m.cfg.Store.Save(ctx, s); err != nil {
		return Session{}, fmt.Errorf("session: save: %w", err)
	}
	if previous != "" && previous != s.ID {
		if err := m.cfg.Store.Delete(ctx, previous); err != nil {
			return s, fmt.Errorf("session: delete the previous id: %w", err)
		}
	}
	m.setCookie(w, s)
	return s, nil
}

// Read returns the session the request carries, reports whether there is one,
// and reports separately whether the store could answer at all.
//
// The bool is the whole authorisation answer and is false for a request without
// the cookie, an id the store does not hold, a session past either timeout, and
// a store that could not be reached. A caller that ignores the error is exactly
// as fail closed as one that reads it: the error is never accompanied by a
// session, and true never comes with an error.
//
// The error says only that the store could not answer. Without it an outage is
// indistinguishable from an ordinary logged out request, which sends every user
// to the identity provider at the moment the infrastructure is already failing
// and leaves nothing in the application's log to see it by. A caller that cares
// answers 503 instead of a login redirect.
//
// An expired session is deleted, and a failed delete is not reported: the
// session is over either way. An active one has its last use written back at
// most every [Config.TouchEvery], so an ordinary read does not write, and a
// failed write back is not reported either, because the session is valid and
// the request is not worth failing over it.
func (m *Manager) Read(ctx context.Context, r *http.Request) (Session, bool, error) {
	cookie, err := r.Cookie(m.cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return Session{}, false, nil
	}

	found, err := m.cfg.Store.Get(ctx, cookie.Value)
	if err != nil {
		// An id the store does not hold is the ordinary logged out case, not
		// something a caller should answer differently.
		if errors.Is(err, ErrNotFound) {
			return Session{}, false, nil
		}
		return Session{}, false, fmt.Errorf("session: read: %w", err)
	}

	// Both bounds are exclusive: a session whose expiry is exactly now is
	// over, not valid for one last request.
	now := m.cfg.Now().UTC()
	if !now.Before(found.ExpiresAt) || now.Sub(found.LastSeen) >= m.cfg.Idle {
		// Deleting it here is housekeeping, not what makes the session
		// invalid: the checks above already decided that, and they run on
		// every read whatever the store still holds.
		_ = m.cfg.Store.Delete(ctx, found.ID)
		return Session{}, false, nil
	}

	if now.Sub(found.LastSeen) >= m.cfg.TouchEvery {
		touched := found
		touched.LastSeen = now
		if err := m.cfg.Store.Save(ctx, touched); err != nil {
			// The session is valid. Failing the request because its last use
			// could not be written would be worse than the idle timeout being
			// measured from the previous touch.
			return found, true, nil
		}
		return touched, true, nil
	}
	return found, true, nil
}

// End logs out the session the request carries: gone from the store, cookie
// cleared. A copy of the cookie taken somewhere else stops working with it.
//
// A request without a session is not an error. The cookie is cleared either
// way, so a logout always leaves the client with nothing.
func (m *Manager) End(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	m.clearCookie(w)

	cookie, err := r.Cookie(m.cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	if err := m.cfg.Store.Delete(ctx, cookie.Value); err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}
	return nil
}

// EndAll logs out every session of one account at once, which is what a
// password change, a disabled account and a log out everywhere all need. It
// takes no response writer: the sessions it ends are mostly not the one making
// the request, and there is no cookie of theirs to clear.
func (m *Manager) EndAll(ctx context.Context, subject string) error {
	if subject == "" {
		return errors.New("session: end all: subject is empty")
	}
	if err := m.cfg.Store.DeleteSubject(ctx, subject); err != nil {
		return fmt.Errorf("session: delete the account's sessions: %w", err)
	}
	return nil
}

func (m *Manager) setCookie(w http.ResponseWriter, s Session) {
	cookie := m.cookie()
	cookie.Value = s.ID
	if m.cfg.Persistent {
		cookie.MaxAge = int(m.cfg.Lifetime.Seconds())
	}
	http.SetCookie(w, cookie)
}

func (m *Manager) clearCookie(w http.ResponseWriter) {
	cookie := m.cookie()
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
}

// cookie is the one place the attributes are decided, so setting and clearing
// cannot drift apart. A browser matches a deletion against the attributes of
// what it stored.
func (m *Manager) cookie() *http.Cookie {
	sameSite := http.SameSiteLaxMode
	if m.cfg.SameSiteStrict {
		sameSite = http.SameSiteStrictMode
	}
	return &http.Cookie{
		Name: m.cfg.CookieName,
		Path: "/",
		// No Domain: the cookie belongs to this host alone, which is both what
		// the __Host- prefix requires and what keeps a subdomain out of it.
		HttpOnly: true,
		Secure:   !m.cfg.AllowPlainHTTP,
		SameSite: sameSite,
	}
}

// NewID is an opaque session id: 32 random bytes, base64url encoded. It cannot
// be guessed and says nothing about the account behind it.
func NewID() (string, error) {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("session: read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
