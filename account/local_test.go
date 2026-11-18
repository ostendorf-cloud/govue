// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"
)

// Fake values only. None of them is anybody's password, secret or login.
const (
	fakeLogin       = "alice@example.com"
	fakePassword    = "fake-password-not-a-secret"
	fakeNewPassword = "fake-new-password-not-a-secret"
	fakeWrong       = "fake-wrong-password-not-a-secret"
	fakeIssuer      = "Example"

	// fakeOtherSecret is a well formed secret no account holds.
	fakeOtherSecret = "FAKEOTHERFAKEOTHERFAKEOTHERFAKEO"
)

// testStart is the clock of every fixture until a test moves it.
var testStart = time.Unix(1_700_000_010, 0)

// derivation is one recorded Argon2id call.
type derivation struct {
	password  string
	time      uint32
	memoryKiB uint32
	threads   uint8
	keyLen    uint32
}

// macCall is one recorded TOTP HMAC.
type macCall struct {
	keyLen  int
	counter uint64
}

// work records the Argon2id and HMAC work of the service, so a test asserts
// the work of a call instead of its time.
type work struct {
	mu          sync.Mutex
	derivations []derivation
	macs        []macCall
}

func (w *work) derive(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte {
	w.mu.Lock()
	w.derivations = append(w.derivations, derivation{
		password: string(password), time: iterations, memoryKiB: memoryKiB, threads: threads, keyLen: keyLen,
	})
	w.mu.Unlock()

	return account.FakeDerive(password, salt, iterations, memoryKiB, threads, keyLen)
}

func (w *work) mac(key []byte, counter uint64) []byte {
	w.mu.Lock()
	w.macs = append(w.macs, macCall{keyLen: len(key), counter: counter})
	w.mu.Unlock()

	return account.HMACSHA1(key, counter)
}

func (w *work) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.derivations = nil
	w.macs = nil
}

func (w *work) snapshot() ([]derivation, []macCall) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]derivation(nil), w.derivations...), append([]macCall(nil), w.macs...)
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a logger.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// events is an ordered log of store writes and callbacks.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(event string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.list = append(e.list, event)
}

func (e *events) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]string(nil), e.list...)
}

// recordingStore wraps a store, logs every write into events and fails a
// call where the test asks it to.
type recordingStore struct {
	account.Store

	events *events

	findErr     error
	findHook    atomic.Pointer[func(call int)]
	findBarrier atomic.Pointer[barrier]
	finds       atomic.Int64
	advanceErr  error
	advanceOK   bool // answer AdvanceTOTPCounter with nil without asking the store
	confirmErr  error
	setHashErr  error
	setHashHook func()

	// setHashCommitErr is returned after a successful write: an ambiguous
	// commit, the write happened but the caller is told it failed.
	setHashCommitErr error
}

func (s *recordingStore) FindByLogin(ctx context.Context, login string) (account.Record, error) {
	call := int(s.finds.Add(1))
	if s.findErr != nil {
		return account.Record{}, s.findErr
	}

	rec, err := s.Store.FindByLogin(ctx, login)
	if hook := s.findHook.Load(); hook != nil {
		(*hook)(call)
	}
	if b := s.findBarrier.Load(); b != nil {
		b.wait()
	}

	return rec, err
}

// barrier holds every caller until n have arrived, so racing calls all read
// the same state before any of them writes. It gives up after a while rather
// than hang a test that started fewer callers.
type barrier struct {
	mu      sync.Mutex
	n       int
	arrived int
	release chan struct{}
}

func newBarrier(n int) *barrier {
	return &barrier{n: n, release: make(chan struct{})}
}

func (b *barrier) wait() {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.release)
	}
	b.mu.Unlock()

	select {
	case <-b.release:
	case <-time.After(5 * time.Second):
	}
}

func (s *recordingStore) AdvanceTOTPCounter(ctx context.Context, id string, counter int64) error {
	s.events.add("AdvanceTOTPCounter " + id)
	if s.advanceErr != nil {
		return s.advanceErr
	}
	if s.advanceOK {
		return nil
	}

	return s.Store.AdvanceTOTPCounter(ctx, id, counter)
}

func (s *recordingStore) ConfirmTOTP(ctx context.Context, id, secret string, counter int64) error {
	s.events.add("ConfirmTOTP " + id)
	if s.confirmErr != nil {
		return s.confirmErr
	}

	return s.Store.ConfirmTOTP(ctx, id, secret, counter)
}

func (s *recordingStore) SetPasswordHash(ctx context.Context, id, oldHash, newHash string) error {
	s.events.add("SetPasswordHash " + id)
	if s.setHashErr != nil {
		return s.setHashErr
	}

	err := s.Store.SetPasswordHash(ctx, id, oldHash, newHash)
	if s.setHashHook != nil {
		s.setHashHook()
	}
	if err == nil && s.setHashCommitErr != nil {
		return s.setHashCommitErr
	}

	return err
}

// fixture is a Service over a MemoryStore with every dependency observable.
type fixture struct {
	svc    *account.Service
	memory *accounttest.MemoryStore
	store  *recordingStore
	work   *work
	events *events
	logs   *syncBuffer
	clock  atomic.Int64

	endErr     error
	endCtxs    chan context.Context
	endCtxErrs chan error    // ctx.Err() as EndSessions saw it when called
	endBlocks  bool          // EndSessions waits until its context ends
	endTimeout time.Duration // ServiceConfig.EndSessionsTimeout
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	return newFixtureWith(t, nil)
}

// newFixtureWith builds a fixture whose store is configured by configure
// before the service is built.
func newFixtureWith(t *testing.T, configure func(*fixture)) *fixture {
	t.Helper()

	f := &fixture{
		memory:     accounttest.NewMemoryStore(accounttest.MemoryConfig{}),
		work:       &work{},
		events:     &events{},
		logs:       &syncBuffer{},
		endCtxs:    make(chan context.Context, 64),
		endCtxErrs: make(chan error, 64),
	}
	f.store = &recordingStore{Store: f.memory, events: f.events}
	f.clock.Store(testStart.Unix())

	if configure != nil {
		configure(f)
	}

	hasher, err := account.NewHasher(account.HasherConfig{MaxConcurrentHashes: 4})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	account.SetDerive(hasher, f.work.derive)

	svc, err := account.NewService(account.ServiceConfig{
		Store:  f.store,
		Hasher: hasher,
		Issuer: fakeIssuer,
		EndSessions: func(ctx context.Context, id string) error {
			f.events.add("EndSessions " + id)
			f.endCtxs <- ctx
			f.endCtxErrs <- ctx.Err()

			if f.endBlocks {
				<-ctx.Done()

				return ctx.Err()
			}

			return f.endErr
		},
		EndSessionsTimeout: f.endTimeout,
		Logger:             slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:                f.now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	account.SetMAC(svc, f.work.mac)

	f.svc = svc

	return f
}

func (f *fixture) now() time.Time { return time.Unix(f.clock.Load(), 0) }

// step moves the clock by n TOTP steps.
func (f *fixture) step(n int64) { f.clock.Add(30 * n) }

// code is the code of secret for the step offset steps from now.
func (f *fixture) code(t *testing.T, secret string, offset int64) string {
	t.Helper()

	code, err := account.TOTPCode(secret, f.now().Add(time.Duration(offset)*30*time.Second))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}

	return code
}

// signUp creates an unconfirmed account for login with fakePassword.
func (f *fixture) signUp(t *testing.T, login string) (account.Identity, account.Enrolment) {
	t.Helper()

	id, enrolment, err := f.svc.SignUp(t.Context(), login, fakePassword)
	if err != nil {
		t.Fatalf("SignUp(%q): %v", login, err)
	}

	return id, enrolment
}

// confirmed creates an account for fakeLogin and confirms its secret with the
// code of the previous step, so the code of the current step logs in.
func (f *fixture) confirmed(t *testing.T) (account.Identity, string) {
	t.Helper()

	id, enrolment := f.signUp(t, fakeLogin)
	if _, err := f.svc.ConfirmTOTP(t.Context(), fakeLogin, fakePassword, f.code(t, enrolment.Secret, -1)); err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}

	return id, enrolment.Secret
}

// record reads the stored record of login.
func (f *fixture) record(t *testing.T, login string) account.Record {
	t.Helper()

	rec, err := f.memory.FindByLogin(t.Context(), login)
	if err != nil {
		t.Fatalf("FindByLogin(%q): %v", login, err)
	}

	return rec
}

// setStoredHash replaces the stored password hash of fakeLogin with hash,
// directly in the memory store.
func setStoredHash(t *testing.T, f *fixture, hash string) {
	t.Helper()

	rec := f.record(t, fakeLogin)
	if err := f.memory.SetPasswordHash(t.Context(), rec.ID, rec.PasswordHash, hash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
}

// wantRefused fails t unless err is ErrInvalidCredentials itself, not
// wrapped, so its text is the same for every refusal.
func wantRefused(t *testing.T, err error) {
	t.Helper()

	if err != account.ErrInvalidCredentials { //nolint:errorlint // identity, not errors.Is: the error must not be wrapped
		t.Fatalf("error = %v, want account.ErrInvalidCredentials unwrapped", err)
	}
}

// wantEqualWork fails t unless the recorded work is one Argon2id derivation
// at today's parameters and one HMAC per step of the window around now,
// each keyed with at least 128 bits. That is the work of a wrong password
// for an existing account, so every refusal costs the same.
func wantEqualWork(t *testing.T, f *fixture) {
	t.Helper()

	derivations, macs := f.work.snapshot()

	if len(derivations) != 1 {
		t.Fatalf("%d Argon2id derivations ran, want exactly 1", len(derivations))
	}
	d := derivations[0]
	if d.memoryKiB != 64*1024 || d.time != 3 || d.threads != 4 || d.keyLen != 32 {
		t.Errorf("derivation at m=%d,t=%d,p=%d,len=%d, want today's m=65536,t=3,p=4,len=32", d.memoryKiB, d.time, d.threads, d.keyLen)
	}

	if len(macs) != 3 {
		t.Fatalf("%d TOTP HMACs ran, want 3", len(macs))
	}
	now := uint64(f.now().Unix() / 30)
	for i, m := range macs {
		if want := now - 1 + uint64(i); m.counter != want {
			t.Errorf("HMAC %d over counter %d, want %d", i, m.counter, want)
		}
		if m.keyLen < 16 {
			t.Errorf("HMAC %d keyed with %d bytes, want at least 16", i, m.keyLen)
		}
	}
}

// wantNoSecretsLogged fails t if the log holds any of the values.
func wantNoSecretsLogged(t *testing.T, f *fixture, values ...string) {
	t.Helper()

	logs := f.logs.String()
	for _, v := range values {
		if v != "" && strings.Contains(logs, v) {
			t.Errorf("the log holds %q:\n%s", v, logs)
		}
	}
}

func TestNewService_validates(t *testing.T) {
	t.Parallel()

	hasher, err := account.NewHasher(account.HasherConfig{})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	valid := account.ServiceConfig{
		Store:       accounttest.NewMemoryStore(accounttest.MemoryConfig{}),
		Hasher:      hasher,
		Issuer:      fakeIssuer,
		EndSessions: func(context.Context, string) error { return nil },
		Logger:      slog.New(slog.DiscardHandler),
	}

	if _, err := account.NewService(valid); err != nil {
		t.Fatalf("NewService with a valid config: %v", err)
	}

	// The shortest accepted timeout is one second, and a long one is fine.
	for _, d := range []time.Duration{time.Second, 24 * time.Hour, time.Duration(math.MaxInt64)} {
		cfg := valid
		cfg.EndSessionsTimeout = d
		if _, err := account.NewService(cfg); err != nil {
			t.Errorf("NewService with an end sessions timeout of %v: %v", d, err)
		}
	}

	tests := []struct {
		name   string
		change func(*account.ServiceConfig)
	}{
		{"no store", func(c *account.ServiceConfig) { c.Store = nil }},
		{"no hasher", func(c *account.ServiceConfig) { c.Hasher = nil }},
		{"no end sessions callback", func(c *account.ServiceConfig) { c.EndSessions = nil }},
		{"no logger", func(c *account.ServiceConfig) { c.Logger = nil }},
		{"negative end sessions timeout", func(c *account.ServiceConfig) { c.EndSessionsTimeout = -time.Second }},
		{"end sessions timeout of one nanosecond", func(c *account.ServiceConfig) { c.EndSessionsTimeout = time.Nanosecond }},
		{"end sessions timeout just below a second", func(c *account.ServiceConfig) { c.EndSessionsTimeout = time.Second - time.Nanosecond }},
		{"empty issuer", func(c *account.ServiceConfig) { c.Issuer = "" }},
		{"issuer with a colon", func(c *account.ServiceConfig) { c.Issuer = "Example:App" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := valid
			tt.change(&cfg)

			if _, err := account.NewService(cfg); err == nil {
				t.Fatal("NewService accepted an invalid config")
			}
		})
	}
}

func TestSignUp(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	id, enrolment := f.signUp(t, fakeLogin)

	rec := f.record(t, fakeLogin)
	if id.ID == "" || id.ID != rec.ID || id.Login != fakeLogin {
		t.Errorf("SignUp identity = %+v, want the stored id %q and the login", id, rec.ID)
	}
	if rec.TOTPSecret != enrolment.Secret || rec.TOTPCounter != 0 {
		t.Errorf("stored secret and counter = %q, %d, want the enrolled secret, unconfirmed", rec.TOTPSecret, rec.TOTPCounter)
	}
	if !strings.HasPrefix(rec.PasswordHash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("stored hash = %q, want an Argon2id PHC string", rec.PasswordHash)
	}

	wantURI, err := account.TOTPProvisioningURI(fakeIssuer, fakeLogin, enrolment.Secret)
	if err != nil {
		t.Fatalf("TOTPProvisioningURI: %v", err)
	}
	if enrolment.URI != wantURI {
		t.Errorf("enrolment URI = %q, want %q", enrolment.URI, wantURI)
	}
	if _, err := account.TOTPCode(enrolment.Secret, f.now()); err != nil {
		t.Errorf("the enrolled secret does not produce codes: %v", err)
	}
}

func TestSignUp_takenLogin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	first, _ := f.signUp(t, fakeLogin)
	before := f.record(t, fakeLogin)

	f.work.reset()

	_, _, err := f.svc.SignUp(t.Context(), fakeLogin, fakeNewPassword)
	if !errors.Is(err, account.ErrConflict) {
		t.Fatalf("SignUp of a taken login = %v, want account.ErrConflict", err)
	}

	// The work is that of a signup that succeeds, one derivation, so the
	// time does not add to what the error already says.
	if derivations, _ := f.work.snapshot(); len(derivations) != 1 {
		t.Errorf("%d derivations for a taken login, want 1, the same as a new one", len(derivations))
	}

	if after := f.record(t, fakeLogin); after != before || after.ID != first.ID {
		t.Errorf("the existing account changed: %+v, want %+v", after, before)
	}
}

func TestSignUp_concurrentSameLoginCreatesOne(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	errs := make([]error, racers)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			_, _, errs[i] = f.svc.SignUp(t.Context(), fakeLogin, fakePassword)
		})
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, account.ErrConflict):
		default:
			t.Errorf("SignUp: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d of %d concurrent signups succeeded, want exactly 1", ok, racers)
	}
}

func TestSignUp_refusesBeforeAnyWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		login    string
		password string
		want     error
	}{
		{"empty login", "", fakePassword, account.ErrLoginInvalid},
		{"leading space", " " + fakeLogin, fakePassword, account.ErrLoginInvalid},
		{"trailing space", fakeLogin + " ", fakePassword, account.ErrLoginInvalid},
		{"colon", "alice:x@example.com", fakePassword, account.ErrLoginInvalid},
		{"control character", "alice\n@example.com", fakePassword, account.ErrLoginInvalid},
		{"invalid UTF-8", "alice\xff@example.com", fakePassword, account.ErrLoginInvalid},
		{"login too long", strings.Repeat("a", account.MaxLoginLength-len("@example.com")+1) + "@example.com", fakePassword, account.ErrLoginInvalid},
		{"password too short", fakeLogin, "fake-short", account.ErrPasswordTooShort},
		{"password too long", fakeLogin, strings.Repeat("x", account.MaxPasswordLength+1), account.ErrPasswordTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			_, _, err := f.svc.SignUp(t.Context(), tt.login, tt.password)
			if !errors.Is(err, tt.want) {
				t.Fatalf("SignUp = %v, want %v", err, tt.want)
			}

			if derivations, _ := f.work.snapshot(); len(derivations) != 0 {
				t.Errorf("%d derivations ran for a refused signup, want 0", len(derivations))
			}
			if _, err := f.memory.FindByLogin(t.Context(), tt.login); !errors.Is(err, account.ErrNotFound) {
				t.Errorf("an account was created: %v", err)
			}
		})
	}
}

func TestSignUp_longestLogin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	login := strings.Repeat("a", account.MaxLoginLength-len("@example.com")) + "@example.com"

	_, enrolment := f.signUp(t, login)

	// The longest login also confirms and logs in: the bound is inclusive
	// on every path.
	if _, err := f.svc.ConfirmTOTP(t.Context(), login, fakePassword, f.code(t, enrolment.Secret, -1)); err != nil {
		t.Fatalf("ConfirmTOTP with the longest login: %v", err)
	}
	if _, err := f.svc.Login(t.Context(), login, fakePassword, f.code(t, enrolment.Secret, 0)); err != nil {
		t.Fatalf("Login with the longest login: %v", err)
	}
}

func TestSignUp_hashingBusy(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := f.svc.SignUp(ctx, fakeLogin, fakePassword)
	if !errors.Is(err, account.ErrHashingBusy) {
		t.Fatalf("SignUp with an ended context = %v, want account.ErrHashingBusy", err)
	}
	if _, err := f.memory.FindByLogin(t.Context(), fakeLogin); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("an account was created without a hash: %v", err)
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	want, secret := f.confirmed(t)

	code := f.code(t, secret, 0)
	got, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got != want {
		t.Errorf("Login identity = %+v, want %+v, the signup's", got, want)
	}

	if rec := f.record(t, fakeLogin); rec.TOTPCounter != f.now().Unix()/30 {
		t.Errorf("stored counter = %d, want %d, the counter of the code", rec.TOTPCounter, f.now().Unix()/30)
	}

	// The same code again is a replay.
	_, err = f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
	wantRefused(t, err)

	// The next step's code logs in.
	f.step(1)
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with the next code: %v", err)
	}
}

// TestLogin_everyRefusalIsTheSame is the enumeration guard: every reason a
// login fails answers the one unwrapped error, after exactly the same
// Argon2id and HMAC work.
func TestLogin_everyRefusalIsTheSame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string

		// setup prepares the store and returns the login, password and
		// code of the attempt.
		setup func(t *testing.T, f *fixture) (login, password, code string)
	}{
		{"unknown login", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return "bob@example.com", fakePassword, f.code(t, secret, 0)
		}},
		{"empty login", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return "", fakePassword, f.code(t, secret, 0)
		}},
		{"login above the length bound", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return strings.Repeat("a", account.MaxLoginLength+1), fakePassword, f.code(t, secret, 0)
		}},
		{"wrong password", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return fakeLogin, fakeWrong, f.code(t, secret, 0)
		}},
		{"password above the length bound", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return fakeLogin, strings.Repeat("x", account.MaxPasswordLength+1), f.code(t, secret, 0)
		}},
		{"wrong code", func(t *testing.T, f *fixture) (string, string, string) {
			f.confirmed(t)
			return fakeLogin, fakePassword, f.code(t, fakeOtherSecret, 0)
		}},
		{"code outside the window", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return fakeLogin, fakePassword, f.code(t, secret, 3)
		}},
		{"missing code", func(t *testing.T, f *fixture) (string, string, string) {
			f.confirmed(t)
			return fakeLogin, fakePassword, ""
		}},
		{"malformed code", func(t *testing.T, f *fixture) (string, string, string) {
			f.confirmed(t)
			return fakeLogin, fakePassword, "12345a"
		}},
		{"unconfirmed secret with the right code", func(t *testing.T, f *fixture) (string, string, string) {
			_, enrolment := f.signUp(t, fakeLogin)
			return fakeLogin, fakePassword, f.code(t, enrolment.Secret, 0)
		}},
		{"replayed code", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			code := f.code(t, secret, 0)
			if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, code); err != nil {
				t.Fatalf("first Login: %v", err)
			}
			return fakeLogin, fakePassword, code
		}},
		{"code that confirmed the secret", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return fakeLogin, fakePassword, f.code(t, secret, -1)
		}},
		{"disabled account", func(t *testing.T, f *fixture) (string, string, string) {
			id, secret := f.confirmed(t)
			f.memory.Disable(id.ID)
			return fakeLogin, fakePassword, f.code(t, secret, 0)
		}},
		{"account without a password, right code", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			setStoredHash(t, f, "")
			return fakeLogin, fakePassword, f.code(t, secret, 0)
		}},
		{"corrupted password hash", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			setStoredHash(t, f, "fake-not-a-hash")
			return fakeLogin, fakePassword, f.code(t, secret, 0)
		}},
		{"account without a secret", func(t *testing.T, f *fixture) (string, string, string) {
			f.signUp(t, "carol@example.com")
			hash := f.record(t, "carol@example.com").PasswordHash
			if _, err := f.memory.Create(t.Context(), account.Record{Login: fakeLogin, PasswordHash: hash}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			return fakeLogin, fakePassword, f.code(t, fakeOtherSecret, 0)
		}},
	}

	var texts sync.Map

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			login, password, code := tt.setup(t, f)

			f.work.reset()

			id, err := f.svc.Login(t.Context(), login, password, code)
			wantRefused(t, err)
			if id != (account.Identity{}) {
				t.Errorf("a refused login returned the identity %+v", id)
			}
			texts.Store(err.Error(), true)

			wantEqualWork(t, f)
			wantNoSecretsLogged(t, f, fakePassword, password, code)
		})
	}

	t.Cleanup(func() {
		n := 0
		texts.Range(func(any, any) bool { n++; return true })
		if n > 1 {
			t.Errorf("refusals answered with %d different texts, want 1", n)
		}
	})
}

// TestLogin_racingReplaySucceedsOnce starts many logins with one code at
// once. The conditional counter write lets exactly one of them through.
func TestLogin_racingReplaySucceedsOnce(t *testing.T) {
	t.Parallel()

	for round := range 10 {
		f := newFixture(t)
		_, secret := f.confirmed(t)

		if ok := racingLogins(t, f, f.code(t, secret, 0)); ok != 1 {
			t.Fatalf("round %d: %d of the racing logins with one code succeeded, want exactly 1", round, ok)
		}
	}
}

// TestLogin_racingReplayNeedsTheConditionalWrite runs the same race over a
// store that reads the counter and then writes. More than one login gets
// through, which shows that the race above is real and that the service
// relies on the store's conditional write, as the accounttest suite demands.
func TestLogin_racingReplayNeedsTheConditionalWrite(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) {
		f.store.Store = &readThenWriteStore{MemoryStore: f.memory, gate: newBarrier(racers)}
	})
	_, secret := f.confirmed(t)

	if ok := racingLogins(t, f, f.code(t, secret, 0)); ok < 2 {
		t.Fatalf("%d racing logins succeeded over a read then write store, want more than 1", ok)
	}
}

// racers is how many calls a race test starts at once.
const racers = 8

// racingLogins starts racers logins with code at once and counts the
// successes. Every failure must be the refusal. A barrier lets every login
// read the account before any of them writes, so the service's own checks
// all pass and only the store's conditional write can refuse.
func racingLogins(t *testing.T, f *fixture, code string) int {
	t.Helper()

	f.store.findBarrier.Store(newBarrier(racers))
	defer f.store.findBarrier.Store(nil)

	errs := make([]error, racers)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			_, errs[i] = f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
		})
	}
	close(start)
	wg.Wait()

	return countRefusals(t, errs)
}

// countRefusals counts the nil errors and fails t for any error that is not
// the unwrapped refusal.
func countRefusals(t *testing.T, errs []error) int {
	t.Helper()

	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		if err != account.ErrInvalidCredentials { //nolint:errorlint // identity: the refusal is never wrapped
			t.Errorf("error = %v, want nil or account.ErrInvalidCredentials", err)
		}
	}

	return ok
}

// readThenWriteStore breaks the replay guard: it reads the counter, compares
// in Go, waits at gate until every racing call has read, and then writes,
// ignoring that the write found the counter moved. It is what the
// accounttest suite exists to refuse.
type readThenWriteStore struct {
	*accounttest.MemoryStore

	gate *barrier

	mu     sync.Mutex
	logins map[string]string
}

func (s *readThenWriteStore) Create(ctx context.Context, rec account.Record) (account.Record, error) {
	rec, err := s.MemoryStore.Create(ctx, rec)
	if err == nil {
		s.mu.Lock()
		if s.logins == nil {
			s.logins = map[string]string{}
		}
		s.logins[rec.ID] = rec.Login
		s.mu.Unlock()
	}

	return rec, err
}

func (s *readThenWriteStore) AdvanceTOTPCounter(ctx context.Context, id string, counter int64) error {
	s.mu.Lock()
	login := s.logins[id]
	s.mu.Unlock()

	rec, err := s.FindByLogin(ctx, login)
	s.gate.wait()

	if err != nil || rec.TOTPCounter <= 0 || counter <= rec.TOTPCounter {
		return account.ErrNotFound
	}

	_ = s.MemoryStore.AdvanceTOTPCounter(ctx, id, counter)

	return nil
}

// TestLogin_unconfirmedRefusedByTheService checks that the service refuses an
// unconfirmed secret itself, over a store that would let the counter advance.
func TestLogin_unconfirmedRefusedByTheService(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) { f.store.advanceOK = true })
	_, enrolment := f.signUp(t, fakeLogin)

	_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, enrolment.Secret, 0))
	wantRefused(t, err)

	for _, e := range f.events.all() {
		if strings.HasPrefix(e, "AdvanceTOTPCounter") {
			t.Errorf("the service asked the store to advance an unconfirmed secret: %v", f.events.all())
		}
	}
}

// TestLogin_disabledRefusedByTheService checks that an account without a
// password hash cannot log in with a right code, over a store that would let
// the counter advance.
func TestLogin_disabledRefusedByTheService(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) { f.store.advanceOK = true })
	_, secret := f.confirmed(t)
	setStoredHash(t, f, "")

	for _, password := range []string{"", fakePassword} {
		_, err := f.svc.Login(t.Context(), fakeLogin, password, f.code(t, secret, 0))
		wantRefused(t, err)
	}
}

// TestLogin_wrongCodeRefusedByTheService checks that the service refuses a
// wrong or missing code itself, over a store that would let any counter
// advance. Without it, only the store's refusal of counter zero would stand
// between a password alone and a login.
func TestLogin_wrongCodeRefusedByTheService(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) { f.store.advanceOK = true })
	f.confirmed(t)

	for _, code := range []string{"", "12345a", f.code(t, fakeOtherSecret, 0)} {
		_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
		wantRefused(t, err)
	}
}

func TestLogin_undecided(t *testing.T) {
	t.Parallel()

	errStore := errors.New("fake store failure")

	t.Run("hashing busy", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, secret := f.confirmed(t)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := f.svc.Login(ctx, fakeLogin, fakePassword, f.code(t, secret, 0))
		if !errors.Is(err, account.ErrHashingBusy) || errors.Is(err, account.ErrInvalidCredentials) {
			t.Fatalf("Login with an ended context = %v, want account.ErrHashingBusy", err)
		}
		if rec := f.record(t, fakeLogin); rec.TOTPCounter != f.now().Unix()/30-1 {
			t.Errorf("the code was consumed by an undecided login")
		}
	})

	t.Run("lookup fails", func(t *testing.T) {
		t.Parallel()

		f := newFixtureWith(t, func(f *fixture) { f.store.findErr = errStore })

		_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, "123456")
		if !errors.Is(err, errStore) || errors.Is(err, account.ErrInvalidCredentials) {
			t.Fatalf("Login = %v, want the store failure", err)
		}
	})

	t.Run("counter write fails", func(t *testing.T) {
		t.Parallel()

		f := newFixtureWith(t, func(f *fixture) { f.store.advanceErr = errStore })
		_, secret := f.confirmed(t)

		_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0))
		if !errors.Is(err, errStore) || errors.Is(err, account.ErrInvalidCredentials) {
			t.Fatalf("Login = %v, want the store failure", err)
		}
	})
}

// outdatedHash is the FakeDerive hash of fakePassword under weaker parameters
// than today's, so a login with it reports NeedsRehash.
var outdatedHash = account.FakeHashOf(fakePassword, 19456, 2, 1)

// confirmedWithOutdatedHash creates a confirmed account whose stored hash is
// outdatedHash.
func confirmedWithOutdatedHash(t *testing.T, f *fixture) (account.Identity, string) {
	t.Helper()

	id, secret := f.confirmed(t)
	setStoredHash(t, f, outdatedHash)

	return id, secret
}

func TestLogin_rehash(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, secret := confirmedWithOutdatedHash(t, f)

	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login: %v", err)
	}

	rec := f.record(t, fakeLogin)
	if rec.PasswordHash == outdatedHash || !strings.HasPrefix(rec.PasswordHash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("stored hash = %q, want a rehash at today's parameters", rec.PasswordHash)
	}

	// The rewritten hash still verifies the password.
	f.step(1)
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login after the rehash: %v", err)
	}

	wantNoSecretsLogged(t, f, fakePassword, secret, outdatedHash, rec.PasswordHash)
}

func TestLogin_noRehashForACurrentHash(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, secret := f.confirmed(t)
	before := f.record(t, fakeLogin).PasswordHash

	f.work.reset()
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if after := f.record(t, fakeLogin).PasswordHash; after != before {
		t.Errorf("a current hash was rewritten")
	}
	if derivations, _ := f.work.snapshot(); len(derivations) != 1 {
		t.Errorf("%d derivations for a login with a current hash, want 1", len(derivations))
	}
}

func TestLogin_rehashFailureDoesNotFailTheLogin(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) { f.store.setHashErr = errors.New("fake store failure") })
	id, secret := f.confirmed(t)
	setStoredHash(t, f, outdatedHash)

	code := f.code(t, secret, 0)
	got, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
	if err != nil || got != id {
		t.Fatalf("Login = %+v, %v, want %+v: a failed rehash must not fail the login", got, err, id)
	}

	logs := f.logs.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, id.ID) || !strings.Contains(logs, "fake store failure") {
		t.Errorf("the failed rehash was not logged as a warning with the account id and the cause:\n%s", logs)
	}

	// No hash at all, also not the new one the failed write carried.
	wantNoSecretsLogged(t, f, fakePassword, secret, code, outdatedHash, "$argon2id$")
}

func TestLogin_rehashBelowThePolicyKeepsTheHash(t *testing.T) {
	t.Parallel()

	// An adopted account may hold a password shorter than today's policy.
	const shortPassword = "fake-short"

	f := newFixture(t)
	_, secret := f.confirmed(t)
	short := account.FakeHashOf(shortPassword, 19456, 2, 1)
	setStoredHash(t, f, short)

	if _, err := f.svc.Login(t.Context(), fakeLogin, shortPassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with a short adopted password: %v", err)
	}
	if got := f.record(t, fakeLogin).PasswordHash; got != short {
		t.Errorf("the hash changed to %q, want it kept", got)
	}

	wantNoSecretsLogged(t, f, shortPassword, secret, short, "$argon2id$")
}

// TestLogin_rehashYieldsToAPasswordChange covers a password change that lands
// between the login's lookup and its rehash write. The rehash must not put
// the old password back.
func TestLogin_rehashYieldsToAPasswordChange(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, secret := confirmedWithOutdatedHash(t, f)

	const changed = "$argon2id$v=19$m=65536,t=3,p=4$ZmFrZS1jaGFuZ2Vk$ZmFrZS1jaGFuZ2VkLWhhc2gtbm90LXJlYWw"

	f.store.finds.Store(0)
	hook := func(call int) {
		if call == 1 {
			setStoredHash(t, f, changed)
		}
	}
	f.store.findHook.Store(&hook)

	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if got := f.record(t, fakeLogin).PasswordHash; got != changed {
		t.Errorf("stored hash = %q, want the concurrently changed one kept", got)
	}

	// A lost compare and set is expected, not a failure: debug, not a warning.
	logs := f.logs.String()
	if strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "level=DEBUG") {
		t.Errorf("the dropped rehash was not logged at debug level only:\n%s", logs)
	}

	wantNoSecretsLogged(t, f, fakePassword, secret, "$argon2id$")
}

func TestConfirmTOTP(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	want, enrolment := f.signUp(t, fakeLogin)

	code := f.code(t, enrolment.Secret, 0)
	got, err := f.svc.ConfirmTOTP(t.Context(), fakeLogin, fakePassword, code)
	if err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}
	if got != want {
		t.Errorf("ConfirmTOTP identity = %+v, want %+v", got, want)
	}

	if rec := f.record(t, fakeLogin); rec.TOTPCounter != f.now().Unix()/30 {
		t.Errorf("stored counter = %d, want %d, the confirming code's", rec.TOTPCounter, f.now().Unix()/30)
	}

	// The confirming code is consumed and cannot log in.
	_, err = f.svc.Login(t.Context(), fakeLogin, fakePassword, code)
	wantRefused(t, err)

	f.step(1)
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, enrolment.Secret, 0)); err != nil {
		t.Fatalf("Login after confirm: %v", err)
	}
}

func TestConfirmTOTP_everyRefusalIsTheSame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, f *fixture) (login, password, code string)
	}{
		{"unknown login", func(t *testing.T, f *fixture) (string, string, string) {
			_, enrolment := f.signUp(t, fakeLogin)
			return "bob@example.com", fakePassword, f.code(t, enrolment.Secret, 0)
		}},
		{"wrong password", func(t *testing.T, f *fixture) (string, string, string) {
			_, enrolment := f.signUp(t, fakeLogin)
			return fakeLogin, fakeWrong, f.code(t, enrolment.Secret, 0)
		}},
		{"wrong code", func(t *testing.T, f *fixture) (string, string, string) {
			f.signUp(t, fakeLogin)
			return fakeLogin, fakePassword, f.code(t, fakeOtherSecret, 0)
		}},
		{"missing code", func(t *testing.T, f *fixture) (string, string, string) {
			f.signUp(t, fakeLogin)
			return fakeLogin, fakePassword, ""
		}},
		{"already confirmed", func(t *testing.T, f *fixture) (string, string, string) {
			_, secret := f.confirmed(t)
			return fakeLogin, fakePassword, f.code(t, secret, 0)
		}},
		{"disabled account", func(t *testing.T, f *fixture) (string, string, string) {
			id, enrolment := f.signUp(t, fakeLogin)
			f.memory.Disable(id.ID)
			return fakeLogin, fakePassword, f.code(t, enrolment.Secret, 0)
		}},
		{"account without a password", func(t *testing.T, f *fixture) (string, string, string) {
			_, enrolment := f.signUp(t, fakeLogin)
			setStoredHash(t, f, "")
			return fakeLogin, fakePassword, f.code(t, enrolment.Secret, 0)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			login, password, code := tt.setup(t, f)

			f.work.reset()

			id, err := f.svc.ConfirmTOTP(t.Context(), login, password, code)
			wantRefused(t, err)
			if id != (account.Identity{}) {
				t.Errorf("a refused confirm returned the identity %+v", id)
			}

			wantEqualWork(t, f)
		})
	}
}

// TestConfirmTOTP_alreadyConfirmedRefusedByTheService checks that the service
// refuses a confirmed secret itself and never asks the store to confirm it.
func TestConfirmTOTP_alreadyConfirmedRefusedByTheService(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, secret := f.confirmed(t)

	before := len(f.events.all())

	_, err := f.svc.ConfirmTOTP(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0))
	wantRefused(t, err)

	if after := f.events.all(); len(after) != before {
		t.Errorf("the service wrote for a confirmed secret: %v", after[before:])
	}
}

func TestConfirmTOTP_racingConfirmsSucceedOnce(t *testing.T) {
	t.Parallel()

	for round := range 10 {
		f := newFixture(t)
		_, enrolment := f.signUp(t, fakeLogin)
		code := f.code(t, enrolment.Secret, 0)

		// Every confirm reads the unconfirmed account before any writes,
		// so only the store's conditional write can refuse.
		f.store.findBarrier.Store(newBarrier(racers))

		errs := make([]error, racers)
		start := make(chan struct{})

		var wg sync.WaitGroup
		for i := range racers {
			wg.Go(func() {
				<-start
				_, errs[i] = f.svc.ConfirmTOTP(t.Context(), fakeLogin, fakePassword, code)
			})
		}
		close(start)
		wg.Wait()

		if ok := countRefusals(t, errs); ok != 1 {
			t.Fatalf("round %d: %d of %d racing confirms succeeded, want exactly 1", round, ok, racers)
		}
	}
}

func TestConfirmTOTP_storeFailure(t *testing.T) {
	t.Parallel()

	errStore := errors.New("fake store failure")
	f := newFixtureWith(t, func(f *fixture) { f.store.confirmErr = errStore })
	_, enrolment := f.signUp(t, fakeLogin)

	_, err := f.svc.ConfirmTOTP(t.Context(), fakeLogin, fakePassword, f.code(t, enrolment.Secret, 0))
	if !errors.Is(err, errStore) || errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("ConfirmTOTP = %v, want the store failure", err)
	}
}

func TestChangePassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id, secret := f.confirmed(t)
	before := len(f.events.all())

	if err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// The new credential is committed first, then the sessions end.
	got := f.events.all()[before:]
	want := []string{"SetPasswordHash " + id.ID, "EndSessions " + id.ID}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}

	_, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0))
	wantRefused(t, err)

	if _, err := f.svc.Login(t.Context(), fakeLogin, fakeNewPassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with the new password: %v", err)
	}

	wantNoSecretsLogged(t, f, fakePassword, fakeNewPassword)
}

func TestChangePassword_refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		identity        func(id account.Identity) account.Identity
		current, next   string
		want            error
		wantDerivations int
	}{
		{"wrong current password", nil, fakeWrong, fakeNewPassword, account.ErrInvalidCredentials, 1},
		{"current password above the bound", nil, strings.Repeat("x", account.MaxPasswordLength+1), fakeNewPassword, account.ErrInvalidCredentials, 1},
		{"new password too short", nil, fakePassword, "fake-short", account.ErrPasswordTooShort, 0},
		{"new password too long", nil, fakePassword, strings.Repeat("x", account.MaxPasswordLength+1), account.ErrPasswordTooLong, 0},
		{"identity of another account", func(id account.Identity) account.Identity {
			id.ID = "memory-999"
			return id
		}, fakePassword, fakeNewPassword, account.ErrInvalidCredentials, 0},
		{"login that no longer exists", func(id account.Identity) account.Identity {
			id.Login = "bob@example.com"
			return id
		}, fakePassword, fakeNewPassword, account.ErrInvalidCredentials, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			id, _ := f.confirmed(t)
			hash := f.record(t, fakeLogin).PasswordHash
			before := len(f.events.all())

			if tt.identity != nil {
				id = tt.identity(id)
			}

			f.work.reset()

			err := f.svc.ChangePassword(t.Context(), id, tt.current, tt.next)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ChangePassword = %v, want %v", err, tt.want)
			}
			if errors.Is(tt.want, account.ErrInvalidCredentials) {
				wantRefused(t, err)
			}

			if derivations, _ := f.work.snapshot(); len(derivations) != tt.wantDerivations {
				t.Errorf("%d derivations ran, want %d", len(derivations), tt.wantDerivations)
			}
			if after := f.events.all(); len(after) != before {
				t.Errorf("a refused change wrote or ended sessions: %v", after[before:])
			}
			if got := f.record(t, fakeLogin).PasswordHash; got != hash {
				t.Errorf("the stored hash changed")
			}
		})
	}
}

// TestChangePassword_racingChangesOneWins starts two password changes that
// both read the same hash before either writes. Exactly one may win. The
// other must not overwrite it.
func TestChangePassword_racingChangesOneWins(t *testing.T) {
	t.Parallel()

	const (
		passwordA = "fake-password-a-not-a-secret"
		passwordB = "fake-password-b-not-a-secret"
	)

	for round := range 10 {
		f := newFixture(t)
		id, secret := f.confirmed(t)

		f.store.findBarrier.Store(newBarrier(2))

		next := []string{passwordA, passwordB}
		errs := make([]error, len(next))

		var wg sync.WaitGroup
		for i := range next {
			wg.Go(func() {
				errs[i] = f.svc.ChangePassword(t.Context(), id, fakePassword, next[i])
			})
		}
		wg.Wait()

		f.store.findBarrier.Store(nil)

		winner, loser := -1, -1
		for i, err := range errs {
			switch {
			case err == nil:
				winner = i
			case errors.Is(err, account.ErrStale):
				loser = i
			default:
				t.Fatalf("round %d: ChangePassword = %v, want nil or account.ErrStale", round, err)
			}
		}
		if winner < 0 || loser < 0 {
			t.Fatalf("round %d: ChangePassword results = %v, want exactly one winner and one ErrStale", round, errs)
		}

		ended := 0
		for _, e := range f.events.all() {
			if strings.HasPrefix(e, "EndSessions") {
				ended++
			}
		}
		if ended != 1 {
			t.Errorf("round %d: EndSessions ran %d times, want once, for the winner", round, ended)
		}

		_, err := f.svc.Login(t.Context(), fakeLogin, next[loser], f.code(t, secret, 0))
		wantRefused(t, err)
		if _, err := f.svc.Login(t.Context(), fakeLogin, next[winner], f.code(t, secret, 0)); err != nil {
			t.Fatalf("round %d: Login with the winning password: %v", round, err)
		}
	}
}

// TestChangePassword_staleWriteChangesNothing lets another password change
// land between the lookup and the write. The write must be refused, keep the
// other change and end no session.
func TestChangePassword_staleWriteChangesNothing(t *testing.T) {
	t.Parallel()

	const otherPassword = "fake-other-password-not-a-secret"

	f := newFixture(t)
	id, secret := f.confirmed(t)
	changed := account.FakeHashOf(otherPassword, 64*1024, 3, 4)

	f.store.finds.Store(0)
	hook := func(call int) {
		if call == 1 {
			setStoredHash(t, f, changed)
		}
	}
	f.store.findHook.Store(&hook)

	before := len(f.events.all())

	err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword)
	if !errors.Is(err, account.ErrStale) {
		t.Fatalf("ChangePassword = %v, want account.ErrStale", err)
	}

	if got := f.record(t, fakeLogin).PasswordHash; got != changed {
		t.Errorf("stored hash = %q, want the other change kept", got)
	}
	for _, e := range f.events.all()[before:] {
		if strings.HasPrefix(e, "EndSessions") {
			t.Errorf("sessions ended for a refused change: %v", f.events.all()[before:])
		}
	}

	if _, err := f.svc.Login(t.Context(), fakeLogin, otherPassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with the other change's password: %v", err)
	}
}

func TestChangePassword_hashingBusy(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id, _ := f.confirmed(t)
	before := len(f.events.all())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := f.svc.ChangePassword(ctx, id, fakePassword, fakeNewPassword)
	if !errors.Is(err, account.ErrHashingBusy) {
		t.Fatalf("ChangePassword with an ended context = %v, want account.ErrHashingBusy", err)
	}
	if after := f.events.all(); len(after) != before {
		t.Errorf("an undecided change wrote or ended sessions: %v", after[before:])
	}
}

// TestChangePassword_storeFailureEndsSessionsAnyway fails the write with a
// store error. The service cannot know whether it committed, so it ends the
// sessions anyway and returns the store error, not ErrStale.
func TestChangePassword_storeFailureEndsSessionsAnyway(t *testing.T) {
	t.Parallel()

	errStore := errors.New("fake store failure")
	f := newFixtureWith(t, func(f *fixture) { f.store.setHashErr = errStore })
	id, secret := f.confirmed(t)

	err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword)
	if !errors.Is(err, errStore) || errors.Is(err, account.ErrSessionsNotEnded) || errors.Is(err, account.ErrStale) {
		t.Fatalf("ChangePassword = %v, want the store failure alone", err)
	}

	got := f.events.all()
	if n := len(got); n < 2 || got[n-2] != "SetPasswordHash "+id.ID || got[n-1] != "EndSessions "+id.ID {
		t.Errorf("events = %v, want the write, then EndSessions", got)
	}

	// This fake failed before writing, so the old password still holds.
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakePassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with the unchanged password: %v", err)
	}
}

// TestChangePassword_staleEndsNoSessions checks that a lost compare and set,
// which certainly wrote nothing, ends no session.
func TestChangePassword_staleEndsNoSessions(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, func(f *fixture) { f.store.setHashErr = account.ErrNotFound })
	id, _ := f.confirmed(t)

	if err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword); !errors.Is(err, account.ErrStale) {
		t.Fatalf("ChangePassword = %v, want account.ErrStale", err)
	}

	for _, e := range f.events.all() {
		if strings.HasPrefix(e, "EndSessions") {
			t.Errorf("sessions ended for a change that certainly wrote nothing: %v", f.events.all())
		}
	}
}

func TestChangePassword_endSessionsFailureKeepsThePassword(t *testing.T) {
	t.Parallel()

	errEnd := errors.New("fake session store failure")
	f := newFixtureWith(t, func(f *fixture) { f.endErr = errEnd })
	id, secret := f.confirmed(t)

	err := f.svc.ChangePassword(t.Context(), id, fakePassword, fakeNewPassword)
	if !errors.Is(err, account.ErrSessionsNotEnded) || !errors.Is(err, errEnd) {
		t.Fatalf("ChangePassword = %v, want ErrSessionsNotEnded wrapping the cause", err)
	}

	// Not rolled back: the new password is in force.
	if _, err := f.svc.Login(t.Context(), fakeLogin, fakeNewPassword, f.code(t, secret, 0)); err != nil {
		t.Fatalf("Login with the new password: %v", err)
	}
}

// TestChangePassword_endSessionsOutlivesTheRequest cancels the caller's
// context right after the commit. Ending the sessions must still run with a
// live context, or a client that hangs up would keep every old session.
func TestChangePassword_endSessionsOutlivesTheRequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	f := newFixtureWith(t, func(f *fixture) { f.store.setHashHook = cancel })
	id, _ := f.confirmed(t)

	// confirmed ran no SetPasswordHash, so the hook fires on the change.
	if err := f.svc.ChangePassword(ctx, id, fakePassword, fakeNewPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	select {
	case err := <-f.endCtxErrs:
		if err != nil {
			t.Errorf("EndSessions ran with an ended context: %v", err)
		}
	default:
		t.Fatal("EndSessions did not run")
	}
}
