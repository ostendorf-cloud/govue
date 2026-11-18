// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"
)

// disableMemory disables an account of a MemoryStore.
func disableMemory(t *testing.T, store account.Store, id string) {
	t.Helper()

	memory, ok := store.(*accounttest.MemoryStore)
	if !ok || !memory.Disable(id) {
		t.Fatalf("Disable(%q): no such account", id)
	}
}

// TestRun_memoryStore runs the suite against the fake both ways it handles
// letter case, one store per check and one store shared by every check.
func TestRun_memoryStore(t *testing.T) {
	t.Parallel()

	for _, fold := range []bool{false, true} {
		cfg := accounttest.MemoryConfig{FoldCase: fold}

		t.Run("fresh store, fold "+boolName(fold), func(t *testing.T) {
			t.Parallel()

			accounttest.Run(t, accounttest.Harness{
				NewStore:  func(*testing.T) account.Store { return accounttest.NewMemoryStore(cfg) },
				FoldsCase: fold,
				Disable:   disableMemory,
			})
		})

		t.Run("shared store, fold "+boolName(fold), func(t *testing.T) {
			t.Parallel()

			shared := accounttest.NewMemoryStore(cfg)
			accounttest.Run(t, accounttest.Harness{
				NewStore:  func(*testing.T) account.Store { return shared },
				FoldsCase: fold,
				Disable:   disableMemory,
			})
		})
	}
}

func boolName(b bool) string {
	if b {
		return "on"
	}

	return "off"
}

// brokenEnv names the broken store a child process runs the suite against.
const brokenEnv = "ACCOUNTTEST_BROKEN_STORE"

// TestRun_catchesBrokenStores runs the suite in a child process against
// stores that each break one guard, and expects the named check to fail.
// The suite has to fail them: a conformance suite that passes a broken store
// proves nothing. A child process is the only way to let the suite fail
// without failing this test. It runs the same test binary, with no network.
func TestRun_catchesBrokenStores(t *testing.T) {
	t.Parallel()

	for _, b := range brokenStores() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBrokenStoreChild$", "-test.v", "-test.count=1")
			cmd.Env = append(os.Environ(), brokenEnv+"="+b.name)

			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the suite passed the broken store %q:\n%s", b.name, out)
			}

			for _, check := range b.mustFail {
				if !strings.Contains(string(out), "--- FAIL: TestBrokenStoreChild/"+check+" ") {
					t.Errorf("check %s did not fail for the broken store %q:\n%s", check, b.name, out)
				}
			}
			if strings.Contains(string(out), "DATA RACE") {
				t.Errorf("the broken store %q raced in memory, so the failure proves nothing:\n%s", b.name, out)
			}
		})
	}
}

// TestBrokenStoreChild is the child process of TestRun_catchesBrokenStores.
// It is skipped in an ordinary run.
func TestBrokenStoreChild(t *testing.T) {
	name := os.Getenv(brokenEnv)
	if name == "" {
		t.Skip("runs only as the child of TestRun_catchesBrokenStores")
	}

	for _, b := range brokenStores() {
		if b.name != name {
			continue
		}

		accounttest.Run(t, accounttest.Harness{
			NewStore:  func(*testing.T) account.Store { return newBrokenStore(b.flaws) },
			FoldsCase: b.foldsCase,
			Disable: func(t *testing.T, store account.Store, id string) {
				t.Helper()

				b, ok := store.(*brokenStore)
				if !ok {
					t.Fatalf("store is %T, want *brokenStore", store)
				}
				b.disable(id)
			},
		})

		return
	}

	t.Fatalf("no broken store named %q", name)
}
