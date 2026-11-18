// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest_test

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
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
				// MemoryStore is atomic by its one mutex. These runs check
				// that, not the suite's power to catch a race, which the
				// broken stores and TestRun_defaults cover.
				Rounds:  100,
				Disable: disableMemory,
			})
		})

		t.Run("shared store, fold "+boolName(fold), func(t *testing.T) {
			t.Parallel()

			shared := accounttest.NewMemoryStore(cfg)
			accounttest.Run(t, accounttest.Harness{
				NewStore:  func(*testing.T) account.Store { return shared },
				FoldsCase: fold,
				// MemoryStore is atomic by its one mutex. These runs check
				// that, not the suite's power to catch a race, which the
				// broken stores and TestRun_defaults cover.
				Rounds:  100,
				Disable: disableMemory,
			})
		})
	}
}

// countingStore counts the Create calls of the signup race.
type countingStore struct {
	*accounttest.MemoryStore

	signups atomic.Int64
}

func (s *countingStore) Create(ctx context.Context, rec account.Record) (account.Record, error) {
	if strings.HasPrefix(rec.Login, "racing-signup-") {
		s.signups.Add(1)
	}

	return s.MemoryStore.Create(ctx, rec)
}

// TestRun_honoursRacersAndRounds checks that the knobs reach the races: the
// signup race makes exactly Racers times Rounds Create calls.
func TestRun_honoursRacersAndRounds(t *testing.T) {
	t.Parallel()

	store := &countingStore{MemoryStore: accounttest.NewMemoryStore(accounttest.MemoryConfig{})}

	accounttest.Run(t, accounttest.Harness{
		NewStore: func(*testing.T) account.Store { return store },
		Disable: func(t *testing.T, _ account.Store, id string) {
			t.Helper()

			if !store.Disable(id) {
				t.Fatalf("Disable(%q): no such account", id)
			}
		},
		Racers: 3,
		Rounds: 5,
	})

	if got := store.signups.Load(); got != 15 {
		t.Errorf("the signup race made %d Create calls, want 3 racers times 5 rounds, 15", got)
	}
}

// TestRun_defaults pins the measured defaults and checks that a harness
// without knobs races with them: the signup race makes DefaultRacers times
// DefaultRounds Create calls.
func TestRun_defaults(t *testing.T) {
	t.Parallel()

	if accounttest.DefaultRacers != 32 || accounttest.DefaultRounds != 1000 {
		t.Fatalf("defaults %d racers and %d rounds, want the measured 32 and 1000; measure again before changing them",
			accounttest.DefaultRacers, accounttest.DefaultRounds)
	}

	store := &countingStore{MemoryStore: accounttest.NewMemoryStore(accounttest.MemoryConfig{})}

	accounttest.Run(t, accounttest.Harness{
		NewStore: func(*testing.T) account.Store { return store },
		Disable: func(t *testing.T, _ account.Store, id string) {
			t.Helper()

			if !store.Disable(id) {
				t.Fatalf("Disable(%q): no such account", id)
			}
		},
	})

	if got, want := store.signups.Load(), int64(accounttest.DefaultRacers*accounttest.DefaultRounds); got != want {
		t.Errorf("the signup race made %d Create calls, want DefaultRacers times DefaultRounds, %d", got, want)
	}
}

// invalidEnv names the invalid harness a child process runs the suite with.
const invalidEnv = "ACCOUNTTEST_INVALID_HARNESS"

// TestRun_refusesInvalidKnobs runs the suite in a child process with values
// that cannot race, and expects it to refuse them before any check runs.
func TestRun_refusesInvalidKnobs(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"one racer", "negative racers", "negative rounds"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInvalidHarnessChild$", "-test.v", "-test.count=1")
			cmd.Env = append(os.Environ(), invalidEnv+"="+name)

			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "accounttest: Harness.Racers") {
				t.Fatalf("the suite accepted %s:\n%s", name, out)
			}
			if strings.Contains(string(out), "--- PASS: TestInvalidHarnessChild/") {
				t.Errorf("a check ran with %s:\n%s", name, out)
			}
		})
	}
}

// TestInvalidHarnessChild is the child process of TestRun_refusesInvalidKnobs.
// It is skipped in an ordinary run.
func TestInvalidHarnessChild(t *testing.T) {
	h := accounttest.Harness{
		NewStore: func(*testing.T) account.Store { return accounttest.NewMemoryStore(accounttest.MemoryConfig{}) },
		Disable:  disableMemory,
	}

	switch os.Getenv(invalidEnv) {
	case "":
		t.Skip("runs only as the child of TestRun_refusesInvalidKnobs")
	case "one racer":
		h.Racers = 1
	case "negative racers":
		h.Racers = -1
	case "negative rounds":
		h.Rounds = -1
	}

	accounttest.Run(t, h)
}

func boolName(b bool) string {
	if b {
		return "on"
	}

	return "off"
}

// brokenEnv names the broken store a child process runs the suite against.
// roundsEnv, when set, replaces the child's rounds, for measuring the catch
// rate of the defaults by hand.
const (
	brokenEnv = "ACCOUNTTEST_BROKEN_STORE"
	roundsEnv = "ACCOUNTTEST_CHILD_ROUNDS"
)

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

			args := []string{"-test.run=" + childPattern(b.mustFail), "-test.v", "-test.count=1"}
			if b.oneProcessor {
				args = append(args, "-test.cpu=1")
			}

			cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
			cmd.Env = append(os.Environ(), brokenEnv+"="+b.name)

			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the suite passed the broken store %q:\n%s", b.name, out)
			}

			for _, check := range b.mustFail {
				failed := regexp.MustCompile(`--- FAIL: TestBrokenStoreChild(-\d+)?/` + regexp.QuoteMeta(check) + ` `)
				if !failed.Match(out) {
					t.Errorf("check %s did not fail for the broken store %q:\n%s", check, b.name, out)
				}
			}
			if strings.Contains(string(out), "DATA RACE") {
				t.Errorf("the broken store %q raced in memory, so the failure proves nothing:\n%s", b.name, out)
			}
		})
	}
}

// childPattern is a -test.run pattern that runs only the named checks, and
// possibly a few others that share a group or a name, in the child. The
// child does not run the whole suite: 32 children racing every check at
// once would load the machine until a narrow race stops showing.
func childPattern(checks []string) string {
	var groups, names []string
	for _, check := range checks {
		group, name, _ := strings.Cut(check, "/")
		groups = append(groups, regexp.QuoteMeta(group))
		names = append(names, regexp.QuoteMeta(name))
	}

	return "^TestBrokenStoreChild$/^(" + strings.Join(groups, "|") + ")$/^(" + strings.Join(names, "|") + ")$"
}

// TestBrokenStoreChild is the child process of TestRun_catchesBrokenStores.
// It is skipped in an ordinary run.
func TestBrokenStoreChild(t *testing.T) {
	name := os.Getenv(brokenEnv)
	if name == "" {
		t.Skip("runs only as the child of TestRun_catchesBrokenStores")
	}

	// The measurement of the defaults sets the rounds of a child run here.
	rounds := 10 * accounttest.DefaultRounds
	if v := os.Getenv(roundsEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("%s=%q: %v", roundsEnv, v, err)
		}
		rounds = n
	}

	for _, b := range brokenStores() {
		if b.name != name {
			continue
		}

		accounttest.Run(t, accounttest.Harness{
			NewStore:  func(*testing.T) account.Store { return newBrokenStore(b.flaws) },
			FoldsCase: b.foldsCase,
			// Ten times the default. The read then write stores here have a
			// window of a few instructions. Alone, the defaults caught each
			// of them in 200 of 200 runs. Next to the account package's
			// Argon2 tests, which go test ./... runs at the same time, one
			// of them escaped in one of ten runs of this test. A broken
			// store fails at its first caught round, so the extra rounds
			// cost little. TestRun_defaults pins the defaults themselves.
			Rounds: rounds,
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
