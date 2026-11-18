// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"context"
	"errors"
	"time"

	"github.com/ostendorf-cloud/govue/account"
)

// Errors a [Store] or a [ChallengeStore] returns, wrapped or not. Any other
// error is a failure of the store.
var (
	// ErrNotFound is a credential, a user handle or a challenge the store does
	// not hold, and a conditional write that changed nothing.
	ErrNotFound = errors.New("passkey: not found")

	// ErrConflict is a credential id that is already stored, for any account.
	ErrConflict = errors.New("passkey: conflict")
)

// UserHandleBytes is the length of a user handle this package generates.
// WebAuthn allows at most 64 bytes. 32 random bytes are far from that limit
// and too many to guess.
const UserHandleBytes = 32

// Credential is one passkey as the [Store] holds it.
//
// The user handle is random, 32 bytes, generated once per account when the
// first passkey is registered and copied onto every further credential of the
// account. It is not the account id. The authenticator keeps the handle and
// hands it back on every login, and a synced passkey copies it to every
// device of the person, so it must not carry anything about the account:
// an id built from an email address or a database sequence would leak it.
// A consumer id can also be longer than the 64 bytes WebAuthn allows. A
// random handle has neither problem, and costs one column.
type Credential struct {
	// ID is the credential id the authenticator chose, as raw bytes. It is
	// unique across every account.
	ID []byte

	// AccountID is the consumer's account id, as in [account.Identity.ID].
	AccountID string

	// UserHandle is the random handle of the account, as described above.
	UserHandle []byte

	// Name is the label the person gave the passkey. It may be empty.
	Name string

	// SignCount is the signature counter the authenticator reported last.
	// Zero means the authenticator keeps no counter, which most synced
	// passkeys do.
	SignCount uint32

	// Data is the rest of the credential, public key included, in a form only
	// this package reads. The store keeps it as given and never interprets
	// it. It holds no secret: a passkey's private key never leaves the
	// authenticator.
	Data []byte

	// CreatedAt is when the passkey was registered.
	CreatedAt time.Time

	// LastUsedAt is the last login with the passkey, zero before the first.
	LastUsedAt time.Time
}

// Store is what this package needs from a consumer's storage: the five calls
// its logic makes for registration, login, listing and deletion, and no more.
// The interface is defined here, at the consumer of the data, and a consumer
// satisfies it with an adapter over its own tables. This package owns no
// schema.
//
// What is absent is the point, as for [account.Store]. There is no method for
// a provider identity, a connected account, a tenant, a role or a list of
// accounts. A passkey login finds its account through the user handle the
// authenticator returns and nothing else, so it can never be matched against
// an account somebody connected for another purpose. Scoping, row level
// security or a tenant, is the adapter's job and is read from ctx there. An
// adapter that expects a scope and finds none fails closed.
//
// An implementation is safe for concurrent use. It returns [ErrNotFound],
// wrapped or not, for what it does not hold and for a conditional write that
// changed nothing.
type Store interface {
	// AddCredential stores a new credential. A credential id that is already
	// stored, for this or any other account, returns ErrConflict and changes
	// nothing.
	//
	// An account has exactly one user handle. When the account already holds
	// a credential, cred.UserHandle has to equal its handle, or the call
	// returns ErrConflict and changes nothing. The check and the insert are
	// one step: two first registrations racing from two browsers each bring
	// a new random handle, and exactly one of them may win. In SQL that is a
	// lock on the account's credential rows, or the handle kept in an account
	// column and written once with a conditional UPDATE. Without it an
	// account ends up with two handles. An adapter that answers
	// FindByUserHandle from an account column then never finds the second
	// passkey, while a delete still counts it as a live one.
	AddCredential(ctx context.Context, cred Credential) error

	// Credentials returns every credential of the account, oldest first. An
	// account without credentials returns an empty slice and no error.
	Credentials(ctx context.Context, accountID string) ([]Credential, error)

	// FindByUserHandle returns the account the handle belongs to and its
	// credentials, or ErrNotFound. The match is exact on the bytes. The
	// Identity carries the account's id and login as the account package
	// would return them, so a passkey login and a password login yield the
	// same Identity. An account that must not log in, because it is disabled
	// or held back, answers ErrNotFound, as [account.Store.FindByLogin] does.
	//
	// The handle belongs to one account and the account has no other, which
	// AddCredential guarantees. The answer holds every credential of that
	// account, so the handle can be kept on each credential row or once in
	// an account column, and both answer the same.
	FindByUserHandle(ctx context.Context, handle []byte) (account.Identity, []Credential, error)

	// UpdateCredential writes SignCount, LastUsedAt and Data of the credential
	// cred.ID of the account cred.AccountID after a login. It is a compare and
	// set: it writes only when the stored SignCount equals oldSignCount, in
	// one statement, and returns ErrNotFound when no row changed. Of two
	// logins racing with one counter value, exactly one wins, so a cloned
	// authenticator cannot use the same counter twice.
	UpdateCredential(ctx context.Context, cred Credential, oldSignCount uint32) error

	// DeleteCredential deletes the credential id of the account accountID. A
	// credential of another account is not touched and answers ErrNotFound.
	// With keepOne set it deletes only while the account holds another
	// credential, decided in the same step as the delete, and returns
	// ErrNotFound otherwise. That is what keeps two racing deletes from
	// removing the last two passkeys of an account without a second factor.
	// In SQL the adapter locks the account's credential rows, for example
	// with SELECT ... FOR UPDATE, or runs the statement serialisable: a
	// DELETE with an EXISTS in its WHERE clause alone lets two concurrent
	// deletes each see the other row.
	DeleteCredential(ctx context.Context, accountID string, id []byte, keepOne bool) error
}
