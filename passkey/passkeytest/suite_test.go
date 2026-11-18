// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkeytest_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
)

var accounts atomic.Uint64

func memoryHarness() passkeytest.Harness {
	return passkeytest.Harness{
		NewStore: func(*testing.T) passkey.Store { return passkeytest.NewMemoryStore() },
		NewAccount: func(t *testing.T, store passkey.Store) account.Identity {
			t.Helper()
			n := accounts.Add(1)
			id := account.Identity{ID: fmt.Sprintf("memory-%d", n), Login: fmt.Sprintf("person-%d@example.test", n)}
			memory(t, store).PutAccount(id)
			return id
		},
		Disable: func(t *testing.T, store passkey.Store, accountID string) {
			t.Helper()
			memory(t, store).Disable(accountID)
		},
	}
}

func memory(t *testing.T, store passkey.Store) *passkeytest.MemoryStore {
	t.Helper()
	m, ok := store.(*passkeytest.MemoryStore)
	if !ok {
		t.Fatalf("store is %T, want *passkeytest.MemoryStore", store)
	}
	return m
}

func TestMemoryStorePassesSuite(t *testing.T) {
	passkeytest.Run(t, memoryHarness())
}
