// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// MaxLoginLength is the most bytes a login may have, the longest address RFC
// 5321 allows. [Service.SignUp] refuses a longer one, and [Service.Login]
// treats a longer one as unknown without asking the store.
const MaxLoginLength = maxTOTPAccountName

var (
	// ErrLoginInvalid is what [Service.SignUp] answers for a login that is
	// empty, longer than [MaxLoginLength], not valid UTF-8, not printable,
	// starts or ends with a space, or holds a colon, which the TOTP label
	// reserves. Whether a login must be an email address is the
	// application's check, before it calls SignUp.
	ErrLoginInvalid = errors.New("account: login is empty, too long, padded or not printable")

	// ErrSessionsNotEnded is what [Service.ChangePassword] answers, wrapping
	// the cause, when the new password is stored but the EndSessions
	// callback failed. The password change stands and is not rolled back.
	// Sessions opened with the old password may still be live, so the
	// caller retries ending them and must not tell the person the change
	// failed.
	ErrSessionsNotEnded = errors.New("account: password changed, but ending the sessions failed")

	// ErrStale is what [Service.ChangePassword] answers when the stored
	// password hash changed between reading it and writing the new one,
	// usually through a concurrent password change, or the account is gone.
	// Nothing was written and no session was ended. The caller asks the
	// person to try again. The current password may by then be another one.
	ErrStale = errors.New("account: the account changed during the call")
)

// ServiceConfig configures a [Service].
type ServiceConfig struct {
	// Store is the consumer's adapter. Required. It should pass the
	// accounttest suite.
	Store Store

	// Hasher hashes and verifies passwords. Required. Share one per process.
	Hasher *Hasher

	// Issuer is the application name an authenticator app groups the TOTP
	// entry under. Required, and a valid TOTP label: printable, no colon.
	Issuer string

	// EndSessions ends every session of the account with this id, in
	// whatever session store the application runs, for example with
	// session.Manager.EndAll and any device sessions as well.
	// [Service.ChangePassword] calls it after the new password is stored.
	// Required: a password change that leaves the old sessions alive does
	// not lock out whoever knew the old password.
	EndSessions func(ctx context.Context, accountID string) error

	// Logger receives operational events, never a password, a hash, a
	// secret or a code. Required.
	Logger *slog.Logger

	// Now is the clock TOTP codes are checked against. Nil is time.Now.
	Now func() time.Time
}

// Enrolment is what [Service.SignUp] hands back for the person to put into an
// authenticator app. It is returned once and never read back out of the
// store afterwards: a secret an API hands over again on request is not a
// second factor.
type Enrolment struct {
	// Secret is the base32 secret, for an app that cannot scan.
	Secret string

	// URI is the otpauth URI of the same secret, for a QR code.
	URI string
}

// Service is the local account logic: signup, TOTP confirmation, login and
// password change, on top of a [Store] and a [Hasher]. It holds no state of
// its own and is safe for concurrent use.
//
// A refused login or confirmation always answers [ErrInvalidCredentials],
// unwrapped, after the same work: one Argon2id derivation at today's
// parameters and one HMAC per step of the TOTP window. That holds for an
// unknown login, a disabled account, a wrong password, a wrong, missing or
// replayed code and an unconfirmed secret alike. Only an outcome nobody can
// reach without both factors differs: the conditional counter write runs
// after the password and the code matched, so its time tells nothing to
// whoever does not hold both already.
type Service struct {
	store       Store
	hasher      *Hasher
	issuer      string
	endSessions func(ctx context.Context, accountID string) error
	logger      *slog.Logger
	now         func() time.Time

	// mac is hmacSHA1. Tests replace it to count the TOTP work.
	mac macFunc
}

// NewService validates cfg and returns a Service.
func NewService(cfg ServiceConfig) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("account: service needs a store")
	case cfg.Hasher == nil:
		return nil, errors.New("account: service needs a hasher")
	case cfg.EndSessions == nil:
		return nil, errors.New("account: service needs an EndSessions callback")
	case cfg.Logger == nil:
		return nil, errors.New("account: service needs a logger")
	case !validLabel(cfg.Issuer, maxTOTPIssuer):
		return nil, fmt.Errorf("account: service issuer: %w", ErrTOTPLabelInvalid)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store:       cfg.Store,
		hasher:      cfg.Hasher,
		issuer:      cfg.Issuer,
		endSessions: cfg.EndSessions,
		logger:      cfg.Logger,
		now:         now,
		mac:         hmacSHA1,
	}, nil
}

// SignUp creates an account for login with password and a fresh TOTP secret,
// and returns its identity with the enrolment to show once. The secret is
// unconfirmed: the account cannot log in until [Service.ConfirmTOTP] has seen
// a code from it, so a person who never scanned the secret is not locked out
// by a signup that looked finished. No session is started here.
//
// A login that is already taken answers [ErrConflict]. That answer is an
// enumeration oracle: anyone can learn whether an account exists by signing
// up with its login. The work is the same either way, one derivation before
// the store is asked, so only the answer tells. Hiding it needs a channel
// outside the response, for example a mail to the address that says an
// account exists, which this package does not have. An application that
// cannot accept the oracle answers ErrConflict like a success in its handler
// and tells the person through that channel. Put the same per client rate
// limit in front of signup as in front of login.
//
// Other refusals are [ErrLoginInvalid], [ErrPasswordTooShort] and
// [ErrPasswordTooLong], checked before any hashing. [ErrHashingBusy] means
// nothing was created.
func (s *Service) SignUp(ctx context.Context, login, password string) (Identity, Enrolment, error) {
	if !validLabel(login, MaxLoginLength) || strings.TrimSpace(login) != login {
		return Identity{}, Enrolment{}, ErrLoginInvalid
	}
	if err := CheckPassword(password); err != nil {
		return Identity{}, Enrolment{}, err
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		return Identity{}, Enrolment{}, err
	}

	// Built before the account exists, so nothing is left behind when it
	// fails. The login passed validLabel above, so a failure is a bug.
	uri, err := TOTPProvisioningURI(s.issuer, login, secret)
	if err != nil {
		return Identity{}, Enrolment{}, fmt.Errorf("account: build the totp provisioning uri: %w", err)
	}

	hash, err := s.hasher.HashPassword(ctx, password)
	if err != nil {
		return Identity{}, Enrolment{}, err
	}

	rec, err := s.store.Create(ctx, Record{Login: login, PasswordHash: hash, TOTPSecret: secret})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Identity{}, Enrolment{}, ErrConflict
		}

		return Identity{}, Enrolment{}, fmt.Errorf("account: create account: %w", err)
	}

	return Identity{ID: rec.ID, Login: rec.Login}, Enrolment{Secret: secret, URI: uri}, nil
}

// ConfirmTOTP confirms the unconfirmed TOTP secret of the account behind
// login with a code from it, and returns the account's identity. No session
// exists before the first login, so the password gates it, as it gates a
// login. The code is consumed: [Store.ConfirmTOTP] stores its counter, so it
// cannot log in afterwards. The caller may start a session with the returned
// identity, since the call proved both factors.
//
// Every refusal answers [ErrInvalidCredentials] after the same work as a
// refused login, including an account whose secret is already confirmed. Of
// two confirmations racing with one code, the store lets exactly one pass.
func (s *Service) ConfirmTOTP(ctx context.Context, login, password, code string) (Identity, error) {
	a, err := s.authenticate(ctx, login, password, code)
	if err != nil {
		return Identity{}, err
	}

	if !a.ok || a.rec.TOTPCounter != 0 {
		return Identity{}, ErrInvalidCredentials
	}

	if err := s.store.ConfirmTOTP(ctx, a.rec.ID, a.rec.TOTPSecret, a.counter); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Identity{}, ErrInvalidCredentials
		}

		return Identity{}, fmt.Errorf("account: confirm totp secret: %w", err)
	}

	return Identity{ID: a.rec.ID, Login: a.rec.Login}, nil
}

// Login checks login, password and a TOTP code together and returns the
// account's identity. There is no half logged in state between a password
// step and a code step.
//
// Every refusal answers [ErrInvalidCredentials], unwrapped, after the same
// work; see [Service]. A login that could not be decided is not a refusal:
// [ErrHashingBusy] when no derivation slot came free, and a wrapped store
// error when the store failed. Neither consumes the code.
//
// The code is consumed by [Store.AdvanceTOTPCounter], the only replay guard
// here. The service never compares the code's counter with one it read: of
// two logins racing with one code, the store's conditional write lets exactly
// one pass, and the other is refused.
//
// When the stored hash is outdated, a successful login rewrites it at today's
// parameters; see [PasswordCheck.NeedsRehash]. That is best effort: a failure
// is logged and the login still succeeds.
func (s *Service) Login(ctx context.Context, login, password, code string) (Identity, error) {
	a, err := s.authenticate(ctx, login, password, code)
	if err != nil {
		return Identity{}, err
	}

	// A secret is confirmed exactly when its counter is above zero. The
	// store refuses to advance an unconfirmed one as well, and this check
	// keeps the refusal here if an adapter misses it.
	if !a.ok || a.rec.TOTPCounter <= 0 {
		return Identity{}, ErrInvalidCredentials
	}

	if err := s.store.AdvanceTOTPCounter(ctx, a.rec.ID, a.counter); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Identity{}, ErrInvalidCredentials
		}

		return Identity{}, fmt.Errorf("account: consume totp code: %w", err)
	}

	if a.needsRehash {
		s.rehash(ctx, a.rec, password)
	}

	return Identity{ID: a.rec.ID, Login: a.rec.Login}, nil
}

// ChangePassword replaces the password of the account behind id, after
// checking current, and ends every session of the account.
//
// The order is fixed: the new hash is committed with [Store.SetPasswordHash]
// first, and only then EndSessions runs. Ending the sessions first would
// leave a window in which a session started with the old password, which
// still verifies, survives the change. EndSessions runs with ctx detached
// from its cancellation, so a client that hangs up after the commit does not
// keep its old sessions.
//
// When EndSessions fails, the password stays changed and the error wraps
// [ErrSessionsNotEnded] and the cause. The caller retries ending the
// sessions. When the store write fails, nothing changed and no session is
// ended.
//
// The write is a compare and set on the hash the current password was
// verified against. Of two changes racing from the same password, exactly
// one wins. The other answers [ErrStale], writes nothing and ends no
// session, since the winner already ended them.
//
// A new password that breaks the policy answers [ErrPasswordTooShort] or
// [ErrPasswordTooLong] before any hashing. A wrong current password, an
// account that is gone or disabled, or a login that now names another
// account answers [ErrInvalidCredentials]. The application starts a fresh
// session for the caller afterwards, if it wants one.
func (s *Service) ChangePassword(ctx context.Context, id Identity, current, next string) error {
	if err := CheckPassword(next); err != nil {
		return err
	}

	rec, found, err := s.lookup(ctx, id.Login)
	if err != nil {
		return err
	}
	if !found || rec.ID != id.ID {
		return ErrInvalidCredentials
	}

	check, err := s.hasher.VerifyPassword(ctx, rec.PasswordHash, current)
	if errors.Is(err, ErrHashingBusy) {
		return err
	}
	if !check.Match {
		return ErrInvalidCredentials
	}

	hash, err := s.hasher.HashPassword(ctx, next)
	if err != nil {
		return err
	}

	// Written only over the hash the current password was verified against.
	// A concurrent change got there first when that fails, and it stands.
	if err := s.store.SetPasswordHash(ctx, rec.ID, rec.PasswordHash, hash); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrStale
		}

		return fmt.Errorf("account: store the new password hash: %w", err)
	}

	if err := s.endSessions(context.WithoutCancel(ctx), rec.ID); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionsNotEnded, err)
	}

	return nil
}

// attempt is the outcome of authenticate.
type attempt struct {
	rec         Record
	counter     int64
	ok          bool
	needsRehash bool
}

// authenticate looks login up and checks password and code against it. The
// work is the same whatever it finds: one derivation, against an empty hash
// for no account, and one HMAC per step of the window, against an empty
// secret for no account. ok reports that an account was found and both
// factors matched. It says nothing about the account's TOTP state, which the
// caller checks. An error means nothing was decided.
func (s *Service) authenticate(ctx context.Context, login, password, code string) (attempt, error) {
	rec, found, err := s.lookup(ctx, login)
	if err != nil {
		return attempt{}, err
	}

	// rec is the zero Record when nothing was found, so both checks run
	// against empty values and cost what a real check costs.
	check, err := s.hasher.VerifyPassword(ctx, rec.PasswordHash, password)
	switch {
	case errors.Is(err, ErrHashingBusy):
		return attempt{}, err
	case errors.Is(err, ErrPasswordHashInvalid):
		s.logger.LogAttrs(ctx, slog.LevelWarn, "account: stored password hash is not a valid argon2id hash",
			slog.String("account_id", rec.ID))
	}

	counter, codeOK := verifyTOTPCode(s.mac, rec.TOTPSecret, code, s.now())

	return attempt{
		rec:         rec,
		counter:     counter,
		ok:          found && check.Match && codeOK,
		needsRehash: check.NeedsRehash,
	}, nil
}

// lookup finds the account behind login. No account is not an error. A login
// that is empty or longer than MaxLoginLength cannot exist and is not sent to
// the store.
func (s *Service) lookup(ctx context.Context, login string) (Record, bool, error) {
	if login == "" || len(login) > MaxLoginLength {
		return Record{}, false, nil
	}

	rec, err := s.store.FindByLogin(ctx, login)
	switch {
	case err == nil:
		return rec, true, nil
	case errors.Is(err, ErrNotFound):
		return Record{}, false, nil
	default:
		return Record{}, false, fmt.Errorf("account: look up account: %w", err)
	}
}

// rehash rewrites the outdated hash of rec with password at today's
// parameters. It is best effort and logs a failure without any secret.
//
// The write is a compare and set on the hash the login just verified. A
// password change landing between the login's lookup and this write moves
// the hash, the write is refused, and the change stands. The rehash is then
// dropped, logged at debug level only, since nothing went wrong.
func (s *Service) rehash(ctx context.Context, rec Record, password string) {
	hash, err := s.hasher.HashPassword(ctx, password)
	if err != nil {
		// A password below today's policy, kept by an adopted account,
		// lands here too. The old hash stays and still verifies.
		s.logger.LogAttrs(ctx, slog.LevelWarn, "account: rehash password: hashing failed, kept the old hash",
			slog.String("account_id", rec.ID), slog.Any("error", err))

		return
	}

	err = s.store.SetPasswordHash(ctx, rec.ID, rec.PasswordHash, hash)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		s.logger.LogAttrs(ctx, slog.LevelDebug, "account: rehash password: the hash changed meanwhile, dropped",
			slog.String("account_id", rec.ID))
	default:
		s.logger.LogAttrs(ctx, slog.LevelWarn, "account: rehash password: store write failed, kept the old hash",
			slog.String("account_id", rec.ID), slog.Any("error", err))
	}
}
