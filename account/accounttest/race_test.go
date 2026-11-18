// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
)

// TestRace_startBarrier holds the last racer of every round before it
// arrives at the start barrier, and counts the calls made meanwhile. With
// the barrier no racer calls until all have arrived, so the count is zero.
// Without it, or with a barrier that waits for fewer racers, the others call
// while the last one is held.
func TestRace_startBarrier(t *testing.T) {
	const racers = 8

	var calls, callsWhileHeld atomic.Int64
	callsWhileHeld.Store(-1)

	h := Harness{Racers: racers, Rounds: 3}
	h.beforeArrive = func(i int) {
		if i != racers-1 {
			return
		}

		// Long enough for every other racer to call, if nothing holds it.
		time.Sleep(50 * time.Millisecond)
		callsWhileHeld.Store(calls.Load())
	}

	race(t, h,
		func(int) struct{} { return struct{}{} },
		func(context.Context, struct{}, int) error {
			calls.Add(1)
			return nil
		},
		func(round int, _ struct{}, errs []error) {
			if got := callsWhileHeld.Load(); got != 0 {
				t.Errorf("round %d: %d racers called before the last one arrived, want 0", round, got)
			}
			if got := calls.Load(); got != int64(len(errs)) {
				t.Errorf("round %d: %d calls, want %d", round, got, len(errs))
			}

			calls.Store(0)
			callsWhileHeld.Store(-1)
		})
}

// TestRace_raisesProcessors starts the race helper with GOMAXPROCS 1 and
// checks that the racers run with at least minRaceProcs, and that the value
// is restored afterwards. It changes a process wide setting, so it does not
// run in parallel.
func TestRace_raisesProcessors(t *testing.T) {
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)

	var seen atomic.Int64

	race(t, Harness{Racers: 4, Rounds: 2},
		func(int) struct{} { return struct{}{} },
		func(context.Context, struct{}, int) error {
			seen.Store(int64(runtime.GOMAXPROCS(0)))
			return nil
		},
		func(int, struct{}, []error) {})

	// Four is the documented minimum, the value the one processor catch
	// rates were measured with.
	if got := seen.Load(); got < 4 {
		t.Errorf("the racers ran with GOMAXPROCS %d, want at least the documented 4", got)
	}
	if got := runtime.GOMAXPROCS(0); got != 1 {
		t.Errorf("GOMAXPROCS after the race = %d, want the 1 it had before", got)
	}
}

// procChange is one call that changed GOMAXPROCS.
type procChange struct{ to int }

// TestRun_parallelRunsRestoreProcessorsOnce runs two Run calls in parallel,
// starting from GOMAXPROCS 1, 2 and 3. The shorter one finishes while the
// longer one still races. The race checks must run with at least
// minRaceProcs all the time, and the original value must come back only when
// the last user of the raise is done, never while another run still races.
// Each race check of one Run raises and restores on its own, so "once"
// holds per stretch of overlapping checks, not per Run. It changes a process wide setting, so it does
// not run in parallel itself.
func TestRun_parallelRunsRestoreProcessorsOnce(t *testing.T) {
	if n := runtime.NumCPU(); n < 2 {
		t.Skipf("runtime.NumCPU() = %d: Run refuses to pass on fewer than two CPUs", n)
	}

	for _, start := range []int{1, 2, 3} {
		t.Run("from "+strconv.Itoa(start), func(t *testing.T) {
			outer := runtime.GOMAXPROCS(start)
			t.Cleanup(func() { runtime.GOMAXPROCS(outer) })

			var (
				mu      sync.Mutex
				changes []procChange
				overlap []procChange // changes up to the end of the short run
			)

			realSet := setProcs
			setProcs = func(n int) int {
				prev := realSet(n)
				if n > 0 {
					mu.Lock()
					changes = append(changes, procChange{n})
					mu.Unlock()
				}

				return prev
			}
			t.Cleanup(func() { setProcs = realSet })

			var (
				minSeen   atomic.Int64
				longReady = make(chan struct{})
				shortDone = make(chan struct{})
				holdOnce  sync.Once
				waitOnce  sync.Once
			)
			minSeen.Store(1 << 30)

			observe := func() {
				got := int64(runtime.GOMAXPROCS(0))
				for {
					cur := minSeen.Load()
					if got >= cur || minSeen.CompareAndSwap(cur, got) {
						return
					}
				}
			}
			wait := func(c <-chan struct{}, what string) {
				select {
				case <-c:
				case <-time.After(time.Minute):
					t.Errorf("timed out waiting for %s", what)
				}
			}

			// The long run holds one racer until the short run is over, so
			// the short run is certain to finish while it still races.
			long := Harness{Racers: 4, Rounds: 3}
			long.beforeArrive = func(int) {
				holdOnce.Do(func() {
					close(longReady)
					wait(shortDone, "the short run")
				})
				observe()
			}
			// The short run starts after the long one raised the value.
			short := Harness{Racers: 2, Rounds: 1}
			short.beforeArrive = func(int) {
				waitOnce.Do(func() { wait(longReady, "the long run") })
				observe()
			}

			for _, h := range []*Harness{&long, &short} {
				h.NewStore = func(*testing.T) account.Store { return NewMemoryStore(MemoryConfig{}) }
				h.Disable = func(t *testing.T, s account.Store, id string) {
					t.Helper()

					if m, ok := s.(*MemoryStore); !ok || !m.Disable(id) {
						t.Fatalf("Disable(%q): no such account", id)
					}
				}
			}

			t.Run("runs", func(t *testing.T) {
				t.Run("long", func(t *testing.T) {
					t.Parallel()
					Run(t, long)
				})
				t.Run("short", func(t *testing.T) {
					t.Parallel()
					defer close(shortDone)
					defer func() {
						mu.Lock()
						overlap = slices.Clone(changes)
						mu.Unlock()
					}()
					Run(t, short)
				})
			})

			if got := minSeen.Load(); got < minRaceProcs {
				t.Errorf("GOMAXPROCS fell to %d during the race checks, want at least %d", got, minRaceProcs)
			}
			if got := runtime.GOMAXPROCS(0); got != start {
				t.Errorf("GOMAXPROCS after both runs = %d, want the %d it started with", got, start)
			}

			mu.Lock()
			defer mu.Unlock()

			// While both runs raced, the first one raised once and nobody
			// restored: the restore comes from the last user only.
			if len(overlap) != 1 || overlap[0].to != minRaceProcs {
				t.Errorf("GOMAXPROCS changes while both runs raced = %v, want one raise to %d and no restore", overlap, minRaceProcs)
			}

			// Afterwards the checks of the long run raise and restore one
			// after the other. Every raise is restored exactly once, always
			// to the original value, never to a value another run set.
			raises, restores := 0, 0
			for _, c := range changes {
				switch c.to {
				case minRaceProcs:
					raises++
				case start:
					restores++
				default:
					t.Errorf("GOMAXPROCS set to %d, want %d or %d", c.to, minRaceProcs, start)
				}
			}
			if raises != restores || changes[len(changes)-1].to != start {
				t.Errorf("GOMAXPROCS changes = %v, want every raise restored once to %d, the last change a restore", changes, start)
			}
		})
	}
}

// oneCPUEnv makes TestRaceOneCPUChild run.
const oneCPUEnv = "ACCOUNTTEST_ONE_CPU_CHILD"

// TestRace_refusesOneCPU runs the race helper in a child process that fakes
// a machine with one CPU. The race checks cannot see a narrow race there, so
// the helper must fail with a clear message instead of reporting success. A
// child process is the only way to let it fail without failing this test.
func TestRace_refusesOneCPU(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRaceOneCPUChild$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), oneCPUEnv+"=1")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the race helper reported success on one CPU:\n%s", out)
	}
	if !strings.Contains(string(out), "accounttest: this machine has one CPU") {
		t.Errorf("the failure does not name the reason:\n%s", out)
	}
	if strings.Contains(string(out), "a call ran") {
		t.Errorf("a race call ran on one CPU:\n%s", out)
	}
}

// TestRaceOneCPUChild is the child process of TestRace_refusesOneCPU. It is
// skipped in an ordinary run.
func TestRaceOneCPUChild(t *testing.T) {
	if os.Getenv(oneCPUEnv) == "" {
		t.Skip("runs only as the child of TestRace_refusesOneCPU")
	}

	numCPU = func() int { return 1 }

	race(t, Harness{Racers: 2, Rounds: 1},
		func(int) struct{} { return struct{}{} },
		func(context.Context, struct{}, int) error {
			t.Log("a call ran")
			return nil
		},
		func(int, struct{}, []error) {})
}
