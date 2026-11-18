// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkeytest

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/passkey"
)

// MemoryStore is a [passkey.Store] in memory, for tests of code built on the
// passkey package. One mutex guards every call, so each conditional write
// compares and writes as one step. It passes [Run].
//
// It is a test fake, not a production store: it keeps nothing across a
// restart. It knows an account only after [MemoryStore.PutAccount], the way a
// SQL adapter joins its credential rows with the account table.
type MemoryStore struct {
	mu       sync.Mutex
	creds    []passkey.Credential
	accounts map[string]account.Identity
	disabled map[string]bool
}

var _ passkey.Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		accounts: map[string]account.Identity{},
		disabled: map[string]bool{},
	}
}

// PutAccount makes the account known, so FindByUserHandle can answer its
// Identity. A second call for the same id replaces the login.
func (s *MemoryStore) PutAccount(id account.Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.accounts[id.ID] = id
}

// Disable holds the account back from login: FindByUserHandle answers
// passkey.ErrNotFound for it from now on.
func (s *MemoryStore) Disable(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.disabled[accountID] = true
}

// AddCredential stores cred, refusing a credential id that is taken.
func (s *MemoryStore) AddCredential(_ context.Context, cred passkey.Credential) error {
	if len(cred.ID) == 0 || cred.AccountID == "" || len(cred.UserHandle) == 0 {
		return fmt.Errorf("passkeytest: credential id, account id and user handle are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, c := range s.creds {
		if bytes.Equal(c.ID, cred.ID) {
			return passkey.ErrConflict
		}
	}
	s.creds = append(s.creds, clone(cred))

	return nil
}

// Credentials returns the account's credentials, oldest first.
func (s *MemoryStore) Credentials(_ context.Context, accountID string) ([]passkey.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := []passkey.Credential{}
	for _, c := range s.creds {
		if c.AccountID == accountID {
			out = append(out, clone(c))
		}
	}

	return out, nil
}

// FindByUserHandle returns the account of handle and its credentials.
func (s *MemoryStore) FindByUserHandle(_ context.Context, handle []byte) (account.Identity, []passkey.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(handle) == 0 {
		return account.Identity{}, nil, passkey.ErrNotFound
	}

	var accountID string
	var out []passkey.Credential
	for _, c := range s.creds {
		if bytes.Equal(c.UserHandle, handle) {
			accountID = c.AccountID
			out = append(out, clone(c))
		}
	}
	id, known := s.accounts[accountID]
	if accountID == "" || !known || s.disabled[accountID] {
		return account.Identity{}, nil, passkey.ErrNotFound
	}

	return id, out, nil
}

// UpdateCredential is the compare and set on the sign count.
func (s *MemoryStore) UpdateCredential(_ context.Context, cred passkey.Credential, oldSignCount uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.creds {
		if bytes.Equal(c.ID, cred.ID) && c.AccountID == cred.AccountID && c.SignCount == oldSignCount {
			s.creds[i].SignCount = cred.SignCount
			s.creds[i].LastUsedAt = cred.LastUsedAt
			s.creds[i].Data = bytes.Clone(cred.Data)

			return nil
		}
	}

	return passkey.ErrNotFound
}

// DeleteCredential deletes one credential of the account, with keepOne
// refusing the last one.
func (s *MemoryStore) DeleteCredential(_ context.Context, accountID string, id []byte, keepOne bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	at, count := -1, 0
	for i, c := range s.creds {
		if c.AccountID != accountID {
			continue
		}
		count++
		if bytes.Equal(c.ID, id) {
			at = i
		}
	}
	if at < 0 || (keepOne && count < 2) {
		return passkey.ErrNotFound
	}
	s.creds = slices.Delete(s.creds, at, at+1)

	return nil
}

func clone(c passkey.Credential) passkey.Credential {
	c.ID = bytes.Clone(c.ID)
	c.UserHandle = bytes.Clone(c.UserHandle)
	c.Data = bytes.Clone(c.Data)

	return c
}
