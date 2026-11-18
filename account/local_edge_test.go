// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
)

// TestLogin_invalidLoginNeverReachesTheStore covers logins SignUp refuses. A
// store may answer such a value with an error of its own, a Postgres text
// column does for a NUL byte, which would turn a refusal into a store failure
// and skip the equal work. The service treats them as unknown instead.
func TestLogin_invalidLoginNeverReachesTheStore(t *testing.T) {
	t.Parallel()

	logins := map[string]string{
		"empty":                    "",
		"NUL byte":                 "alice\x00@example.com",
		"invalid UTF-8":            "alice\xff@example.com",
		"control character":        "alice\n@example.com",
		"colon":                    "alice:x@example.com",
		"one byte above the bound": strings.Repeat("a", account.MaxLoginLength+1),
	}

	for name, login := range logins {
		for _, confirm := range []bool{false, true} {
			call := "Login"
			if confirm {
				call = "ConfirmTOTP"
			}

			t.Run(call+" "+name, func(t *testing.T) {
				t.Parallel()

				f := newFixture(t)

				var code string
				if confirm {
					_, enrolment := f.signUp(t, fakeLogin)
					code = f.code(t, enrolment.Secret, 0)
				} else {
					_, secret := f.confirmed(t)
					code = f.code(t, secret, 0)
				}

				f.work.reset()
				finds := f.store.finds.Load()

				var err error
				if confirm {
					_, err = f.svc.ConfirmTOTP(t.Context(), login, fakePassword, code)
				} else {
					_, err = f.svc.Login(t.Context(), login, fakePassword, code)
				}

				wantRefused(t, err)
				wantEqualWork(t, f)

				if got := f.store.finds.Load() - finds; got != 0 {
					t.Errorf("the store was asked %d times for a login SignUp refuses, want 0", got)
				}
			})
		}
	}
}

// TestChangePassword_wrongCurrentEqualToNextIsRefused passes the same wrong
// password as current and next. It must be refused like any wrong current
// password, not taken as "nothing to change".
func TestChangePassword_wrongCurrentEqualToNextIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id, _ := f.confirmed(t)
	hash := f.record(t, fakeLogin).PasswordHash
	before := len(f.events.all())

	err := f.svc.ChangePassword(t.Context(), id, fakeWrong, fakeWrong)
	wantRefused(t, err)

	if after := f.events.all(); len(after) != before {
		t.Errorf("a refused change wrote or ended sessions: %v", after[before:])
	}
	if got := f.record(t, fakeLogin).PasswordHash; got != hash {
		t.Errorf("the stored hash changed")
	}
}

// TestChangePassword_ambiguousCommitStillEndsSessions covers a store whose
// write commits and then reports an error, as a driver can when the context
// ends during the commit. The password may have changed, so the sessions end
// anyway: ending them once too often is harmless, leaving them is not.
func TestChangePassword_ambiguousCommitStillEndsSessions(t *testing.T) {
	t.Parallel()

	errCommit := errors.New("fake: context canceled during commit")
	f := newFixtureWith(t, func(f *fixture) { f.store.setHashCommitErr = errCommit })
	id, secret := f.confirmed(t)

	err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword)
	if !errors.Is(err, errCommit) || errors.Is(err, account.ErrStale) || errors.Is(err, account.ErrSessionsNotEnded) {
		t.Fatalf("ChangePassword = %v, want the store error, not ErrStale or ErrSessionsNotEnded", err)
	}

	ended := 0
	for _, e := range f.events.all() {
		if strings.HasPrefix(e, "EndSessions") {
			ended++
		}
	}
	if ended != 1 {
		t.Errorf("EndSessions ran %d times after an ambiguous commit, want 1", ended)
	}

	// The write did commit here, so the new password is in force.
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakeNewPassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with the new password: %v", err)
	}
}

// TestChangePassword_ambiguousCommitAndEndSessionsFail reports both.
func TestChangePassword_ambiguousCommitAndEndSessionsFail(t *testing.T) {
	t.Parallel()

	errCommit := errors.New("fake: context canceled during commit")
	errEnd := errors.New("fake session store failure")
	f := newFixtureWith(t, func(f *fixture) {
		f.store.setHashCommitErr = errCommit
		f.endErr = errEnd
	})
	id, _ := f.confirmed(t)

	err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword)
	if !errors.Is(err, errCommit) || !errors.Is(err, account.ErrSessionsNotEnded) || !errors.Is(err, errEnd) {
		t.Fatalf("ChangePassword = %v, want the store error and ErrSessionsNotEnded with its cause", err)
	}
}

// TestChangePassword_endSessionsHasADeadline lets EndSessions hang. The
// configured timeout ends it, and the change answers ErrSessionsNotEnded.
func TestChangePassword_endSessionsHasADeadline(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) {
		f.endBlocks = true
		f.endTimeout = 50 * time.Millisecond
	})
	id, _ := f.confirmed(t)

	done := make(chan error, 1)
	go func() { done <- f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword) }()

	select {
	case err := <-done:
		if !errors.Is(err, account.ErrSessionsNotEnded) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ChangePassword = %v, want ErrSessionsNotEnded wrapping the deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ChangePassword still waits for a hung EndSessions after 5 s")
	}
}

// TestChangePassword_endSessionsDefaultDeadline checks the default timeout.
func TestChangePassword_endSessionsDefaultDeadline(t *testing.T) {
	t.Parallel()

	if account.DefaultEndSessionsTimeout != 30*time.Second {
		t.Fatalf("DefaultEndSessionsTimeout = %v, want the documented 30 s", account.DefaultEndSessionsTimeout)
	}

	f := newFixture(t)
	id, _ := f.confirmed(t)

	start := time.Now()
	if err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	deadline, ok := endContext(t, f).Deadline()
	if !ok {
		t.Fatal("EndSessions ran without a deadline")
	}
	if left := deadline.Sub(start); left <= account.DefaultEndSessionsTimeout-5*time.Second || left > account.DefaultEndSessionsTimeout+time.Second {
		t.Errorf("EndSessions deadline %v after the call, want about %v", left, account.DefaultEndSessionsTimeout)
	}
}

type requestKey struct{}

// endContext returns the context of the last EndSessions call that has
// already happened, and fails t when there was none, instead of waiting.
func endContext(t *testing.T, f *fixture) context.Context {
	t.Helper()

	select {
	case ctx := <-f.endCtxs:
		return ctx
	default:
		t.Fatal("EndSessions did not run")

		return nil
	}
}

// TestChangePassword_endSessionsKeepsContextValues checks that EndSessions
// sees the request's values, for example a tenant an adapter scopes by.
func TestChangePassword_endSessionsKeepsContextValues(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id, _ := f.confirmed(t)

	ctx := context.WithValue(t.Context(), requestKey{}, "fake-tenant")
	if err := f.svc.ChangePassword(ctx, id, fakePassword, fakeNewPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if got := endContext(t, f).Value(requestKey{}); got != "fake-tenant" {
		t.Errorf("EndSessions context value = %v, want the request's", got)
	}
}

// TestLogin_malformedStoredHashLoggedQuietly refuses a login against a
// corrupted hash. Anyone can trigger that path without credentials, so it
// logs at debug level, never as a warning that would let them flood the log,
// and never with the stored value.
func TestLogin_malformedStoredHashLoggedQuietly(t *testing.T) {
	t.Parallel()

	const marker = "fake-not-a-hash-marker-value"

	f := newFixture(t)
	id, secret := f.confirmed(t)
	setStoredHash(t, f, marker)

	_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0))
	wantRefused(t, err)

	logs := f.logs.String()
	if strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "level=DEBUG") || !strings.Contains(logs, id.ID) {
		t.Errorf("the corrupted hash was not logged at debug level with the account id:\n%s", logs)
	}

	wantNoSecretsLogged(t, f, marker, fakePassword, secret)
}

// TestChangePassword_staleAfterARehash lets a login with the same password
// rehash an outdated hash between the change's verify and its write. The
// password did not change, but the hash did, so the change answers ErrStale
// and the person tries again.
func TestChangePassword_staleAfterARehash(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id, secret := confirmedWithOutdatedHash(t, f)

	f.store.finds.Store(0)
	hook := func(call int) {
		if call == 1 {
			// Inside ChangePassword's lookup: a login with a rehash
			// completes first.
			if _, err := f.svc.Login(context.WithoutCancel(t.Context()), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
				t.Errorf("Login: %v", err)
			}
		}
	}
	f.store.findHook.Store(&hook)

	err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword)
	f.store.findHook.Store(nil)

	if !errors.Is(err, account.ErrStale) {
		t.Fatalf("ChangePassword = %v, want account.ErrStale", err)
	}
}

// TestLogin_replayTriggersNoRehash replays a code with the right password
// against an outdated hash. The replay is refused before any rehash write.
func TestLogin_replayTriggersNoRehash(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, secret := confirmedWithOutdatedHash(t, f)

	code := f.code(t, secret, 0)
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, code); err != nil {
		t.Fatalf("first Login: %v", err)
	}

	// Put the outdated hash back, so a rehash would be due again.
	setStoredHash(t, f, outdatedHash)
	before := len(f.events.all())

	_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
	wantRefused(t, err)

	for _, e := range f.events.all()[before:] {
		if strings.HasPrefix(e, "SetPasswordHash") {
			t.Errorf("a replayed login rewrote the hash: %v", f.events.all()[before:])
		}
	}
	if got := f.record(t, fakeLogin).PasswordHash; got != outdatedHash {
		t.Errorf("stored hash changed after a replay")
	}
}
