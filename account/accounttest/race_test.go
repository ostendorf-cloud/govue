// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
