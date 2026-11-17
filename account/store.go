// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import "context"

// Record is one local account as the [Store] holds it: the fields the login
// logic reads, and nothing a user table usually carries besides.
//
// An empty PasswordHash or TOTPSecret means the account has none, for example
// an account a consumer created through OIDC alone. Such an account cannot log
// in through this package, whatever password somebody guesses for it.
type Record struct {
	// ID is the consumer's account id, opaque here. [Store.Create] fills it.
	ID string

	// Login is the unique name the person logs in with, usually an email
	// address. The store decides uniqueness, and a store that folds case
	// folds it the same way in FindByLogin and Create.
	Login string

	// PasswordHash is the encoded password hash, for example an Argon2id PHC
	// string. The store keeps it as given and never interprets it.
	PasswordHash string

	// TOTPSecret is the base32 TOTP secret. A store may encrypt it at rest,
	// as long as FindByLogin hands back the plain value.
	TOTPSecret string

	// TOTPConfirmed reports whether a code from TOTPSecret has been confirmed.
	// An unconfirmed secret never satisfies a login.
	TOTPConfirmed bool

	// TOTPCounter is the RFC 6238 counter of the last code this account
	// consumed, zero when none was. Counter zero is the first thirty seconds
	// of 1970, so no live code ever has it.
	TOTPCounter int64
}

// Store is what this package needs from a consumer's storage: the five calls
// the login logic makes, and no more. It is defined here, at the consumer of
// the data, and a consumer satisfies it with an adapter over its own tables.
// This package owns no schema.
//
// What is deliberately absent is the point. There is no method for a provider
// identity, a connected account, a tenant, a role or a list of accounts. Code
// in this package cannot reach what the interface does not offer, so a login
// can never be matched against a provider account somebody connected for
// another purpose, and never act outside the account it was asked about. That
// guarantee is structural, not a check somebody could forget. Adding a method
// here, whatever the intended use, removes it for every consumer at once.
//
// Scoping is the adapter's job. A consumer with row level security or tenants
// reads its scope from ctx inside the adapter. Nothing here carries it.
//
// An implementation is safe for concurrent use. It returns [ErrNotFound],
// wrapped or not, for a login or an id it does not hold, and for a conditional
// write that changed nothing. Any other error is a failure of the store.
type Store interface {
	// FindByLogin returns the account with this login, or ErrNotFound. It runs
	// before any account scope exists, so an adapter with row level security
	// reaches the row through a narrow lookup, not a widened policy.
	FindByLogin(ctx context.Context, login string) (Record, error)

	// Create stores a new account and returns it with ID filled. A login that
	// is already taken returns ErrConflict. The other fields are stored as
	// given, so a new account starts with an unconfirmed TOTP secret.
	Create(ctx context.Context, rec Record) (Record, error)

	// AdvanceTOTPCounter sets the account's TOTPCounter to counter only when
	// the stored value is lower, and returns ErrNotFound when no row changed.
	// The comparison and the write are one statement, a conditional UPDATE in
	// SQL. That is what refuses a replayed code: of two logins racing with
	// the same code, exactly one advances the counter. Reading the counter
	// and then writing the whole record cannot do that.
	AdvanceTOTPCounter(ctx context.Context, id string, counter int64) error

	// ConfirmTOTP marks the account's TOTP secret as confirmed and sets
	// TOTPCounter to counter, the counter of the code that confirmed it, so
	// that code cannot log in afterwards. It writes only when the account has
	// a secret, is not yet confirmed and counter is above the stored value,
	// and returns ErrNotFound when no row changed.
	ConfirmTOTP(ctx context.Context, id string, counter int64) error

	// SetPasswordHash replaces the account's password hash and nothing else,
	// or returns ErrNotFound.
	SetPasswordHash(ctx context.Context, id, hash string) error
}
