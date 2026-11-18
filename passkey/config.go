// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ostendorf-cloud/govue/account"
)

// Defaults of the durations in [Config].
const (
	// DefaultChallengeTTL is how long a challenge stays usable, and the
	// timeout the browser is given for the ceremony.
	DefaultChallengeTTL = 5 * time.Minute

	// DefaultReauthWindow is how old the last login of a session may be for
	// it to register or delete a passkey.
	DefaultReauthWindow = 10 * time.Minute

	// DefaultMaxBodyBytes is the body limit when [Config.MaxBodyBytes] is
	// zero. An attestation with a certificate chain is a few KiB, and JSON
	// with base64url grows it by a third.
	DefaultMaxBodyBytes int64 = 64 << 10
)

// Bounds of [Config.ChallengeTTL].
const (
	MinChallengeTTL = 30 * time.Second
	MaxChallengeTTL = 10 * time.Minute
)

// maxDisplayNameBytes bounds [Config.RPDisplayName].
const maxDisplayNameBytes = 128

// Config configures [New]. RPID, RPDisplayName, Origins, Store, StartSession,
// CurrentAccount and OtherFactors are required.
type Config struct {
	// RPID is the relying party id: the host every passkey of this
	// application is bound to, such as "app.example.com". Required, and never
	// derived from a request's Host header, which the client controls.
	//
	// It cannot change later. A passkey registered for one RP id is refused
	// for every other, so changing it loses every passkey. Use the
	// application's own host, not a marketing parent domain the application
	// does not control.
	RPID string

	// RPDisplayName is the name an authenticator shows next to the passkey.
	// Required.
	RPDisplayName string

	// Origins are the exact origins a ceremony may come from, each a scheme
	// and host with an optional port and nothing else, such as
	// "https://app.example.com". Required. Each has to be https, except
	// http on localhost for development, and its host has to be RPID or a
	// subdomain of it.
	Origins []string

	// Store holds the credentials. Required.
	Store Store

	// Challenges holds the pending ceremonies. Nil is a
	// [MemoryChallengeStore] with the defaults, which is right for one
	// process.
	Challenges ChallengeStore

	// StartSession turns a verified [account.Identity] into the
	// application's session after a passkey login. It has the signature of
	// [account.HandlerConfig.StartSession], so an application passes the
	// same function to both and a passkey login starts the same session a
	// password and TOTP login does. Required.
	StartSession func(ctx context.Context, w http.ResponseWriter, r *http.Request, id account.Identity) error

	// CurrentAccount returns the account the request is logged in as and
	// when that login happened, and false for a request without a session.
	// An error means the session store could not answer. Registering,
	// listing and deleting passkeys need it. Required.
	//
	// The time is the login, not the last request: a session that stays busy
	// for a day is not a recent authentication. In example/ it is the
	// session's IssuedAt.
	CurrentAccount func(r *http.Request) (id account.Identity, authenticatedAt time.Time, ok bool, err error)

	// OtherFactors returns how many ways the account has to log in besides
	// its passkeys, for example 1 for a password with a confirmed TOTP
	// secret. Deleting a passkey is refused when it is the last one and this
	// returns zero. Required.
	OtherFactors func(ctx context.Context, id account.Identity) (int, error)

	// ChallengeTTL is how long a challenge stays usable. Zero is
	// [DefaultChallengeTTL]. It has to lie between [MinChallengeTTL] and
	// [MaxChallengeTTL].
	ChallengeTTL time.Duration

	// ReauthWindow is how old the session's login may be to register or
	// delete a passkey. Zero is [DefaultReauthWindow].
	ReauthWindow time.Duration

	// TrustedOrigins are the cross origin callers the handlers accept, as in
	// [account.HandlerConfig.TrustedOrigins]. Zero trusts this origin only.
	TrustedOrigins []string

	// AllowPlainHTTP drops the Secure attribute and the __Host- prefix of the
	// challenge cookie, for a development server without TLS. It mirrors
	// session.Config.AllowPlainHTTP.
	AllowPlainHTTP bool

	// MaxBodyBytes limits a request body. Zero is [DefaultMaxBodyBytes].
	MaxBodyBytes int64

	// Now is the clock. Nil is time.Now.
	Now func() time.Time
}

// validate checks cfg and returns it with the defaults filled in. Like the
// other constructors of this module it names every problem at once.
func (cfg Config) validate() (Config, error) {
	var problems []error

	if err := checkRPID(cfg.RPID); err != nil {
		problems = append(problems, err)
	}
	if err := checkDisplayName(cfg.RPDisplayName); err != nil {
		problems = append(problems, err)
	}
	if len(cfg.Origins) == 0 {
		problems = append(problems, errors.New("origins are required"))
	}
	for _, origin := range cfg.Origins {
		if err := checkOrigin(origin, cfg.RPID); err != nil {
			problems = append(problems, err)
		}
	}
	if cfg.Store == nil {
		problems = append(problems, errors.New("store is required"))
	}
	if cfg.StartSession == nil {
		problems = append(problems, errors.New("start session is required"))
	}
	if cfg.CurrentAccount == nil {
		problems = append(problems, errors.New("current account is required"))
	}
	if cfg.OtherFactors == nil {
		problems = append(problems, errors.New("other factors is required"))
	}

	if cfg.ChallengeTTL == 0 {
		cfg.ChallengeTTL = DefaultChallengeTTL
	}
	if cfg.ChallengeTTL < MinChallengeTTL || cfg.ChallengeTTL > MaxChallengeTTL {
		problems = append(problems, fmt.Errorf("challenge ttl %s is outside %s to %s",
			cfg.ChallengeTTL, MinChallengeTTL, MaxChallengeTTL))
	}
	if cfg.ReauthWindow == 0 {
		cfg.ReauthWindow = DefaultReauthWindow
	}
	if cfg.ReauthWindow < 0 {
		problems = append(problems, fmt.Errorf("reauth window %s is negative", cfg.ReauthWindow))
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if cfg.MaxBodyBytes < 0 {
		problems = append(problems, fmt.Errorf("max body bytes %d is negative", cfg.MaxBodyBytes))
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if err := errors.Join(problems...); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// checkRPID refuses what a browser would refuse as an RP id, and what would
// bind every passkey to the wrong host: anything but a lower case DNS name of
// at least two labels, or localhost. It cannot tell a public suffix such as
// "co.uk" from a registrable domain, since that needs the public suffix list,
// and a browser refuses such an RP id at the ceremony.
func checkRPID(rpID string) error {
	switch {
	case rpID == "":
		return errors.New("rp id is required")
	case rpID == "localhost":
		return nil
	case len(rpID) > 253:
		return fmt.Errorf("rp id is longer than 253 bytes")
	case net.ParseIP(rpID) != nil:
		return fmt.Errorf("rp id %q is an IP address, a passkey needs a host name", rpID)
	}

	labels := strings.Split(rpID, ".")
	if len(labels) < 2 {
		return fmt.Errorf("rp id %q is a single label, use the application's full host name", rpID)
	}
	for _, label := range labels {
		if !validLabel(label) {
			return fmt.Errorf("rp id %q is not a lower case host name without scheme, port or path", rpID)
		}
	}

	return nil
}

// validLabel reports whether label is a lower case DNS label: 1 to 63 bytes
// of a to z, digits and inner hyphens.
func validLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range []byte(label) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

func checkDisplayName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("rp display name is required")
	}
	if len(name) > maxDisplayNameBytes {
		return fmt.Errorf("rp display name is longer than %d bytes", maxDisplayNameBytes)
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return errors.New("rp display name holds a character that is not printable")
		}
	}

	return nil
}

// checkOrigin accepts an origin only in the serialised form a browser puts
// into the client data, and refuses everything else at startup instead of
// normalising it: a lower case scheme, https or http on localhost, a lower
// case host of DNS labels that is the RP id or below it, and an optional port
// of 1 to 65535 without leading zeros that is not the scheme's default.
//
// The library's own comparison folds the case of the scheme and the host and
// normalises the port, so a sloppy entry might still match. Refusing it
// keeps the configured list equal to what browsers send, which is what a
// reviewer reads and what the RP id suffix rule is checked against.
func checkOrigin(origin, rpID string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("origin %q is not a URL: %w", origin, err)
	}
	host := u.Hostname()
	// url.Parse folds the scheme to lower case, so the raw text is checked.
	scheme, _, _ := strings.Cut(origin, ":")
	switch {
	case scheme != strings.ToLower(scheme):
		return fmt.Errorf("origin %q needs a lower case scheme", origin)
	case scheme == "https":
	case scheme == "http" && host == "localhost":
	case scheme == "http" && strings.ToLower(host) == "localhost":
		return fmt.Errorf("origin %q needs a lower case host", origin)
	default:
		return fmt.Errorf("origin %q is not https, plain http is allowed on localhost only", origin)
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		strings.HasSuffix(origin, "?") || strings.HasSuffix(origin, "#") {
		return fmt.Errorf("origin %q holds more than a scheme, a host and a port", origin)
	}
	if host == "" {
		return fmt.Errorf("origin %q has no host", origin)
	}
	if host != "localhost" {
		for _, label := range strings.Split(host, ".") {
			if !validLabel(label) {
				return fmt.Errorf("origin %q needs a lower case host of DNS labels, no wildcard", origin)
			}
		}
	}
	if err := checkPort(origin, u); err != nil {
		return err
	}
	if rpID != "" && host != rpID && !strings.HasSuffix(host, "."+rpID) {
		return fmt.Errorf("origin %q is not rp id %q or a subdomain of it", origin, rpID)
	}

	return nil
}

// checkPort refuses a port a browser never serialises: an empty one after a
// colon, one with a leading zero, one outside 1 to 65535 and the default port
// of the scheme, which a browser leaves out.
func checkPort(origin string, u *url.URL) error {
	port := u.Port()
	if port == "" {
		if strings.HasSuffix(u.Host, ":") {
			return fmt.Errorf("origin %q has a colon without a port", origin)
		}
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || port[0] == '0' {
		return fmt.Errorf("origin %q needs a port of 1 to 65535 without leading zeros", origin)
	}
	if n == 443 && u.Scheme == "https" || n == 80 && u.Scheme == "http" {
		return fmt.Errorf("origin %q names the default port, a browser sends it without", origin)
	}

	return nil
}
