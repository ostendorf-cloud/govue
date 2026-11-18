// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
)

// TestPasskeyAndTOTPLoginYieldEqualIdentity runs a password and TOTP login
// through the account handlers and a passkey login through these handlers,
// with one StartSession function for both, and compares the two
// account.Identity values it receives.
func TestPasskeyAndTOTPLoginYieldEqualIdentity(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	var started []account.Identity
	startSession := func(_ context.Context, _ http.ResponseWriter, _ *http.Request, id account.Identity) error {
		started = append(started, id)
		return nil
	}

	accounts := accounttest.NewMemoryStore(accounttest.MemoryConfig{})
	hasher, err := account.NewHasher(account.HasherConfig{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := account.NewService(account.ServiceConfig{
		Store: accounts, Hasher: hasher, Issuer: "Example",
		EndSessions: func(context.Context, string) error { return nil },
		Logger:      slog.New(slog.DiscardHandler),
		Now:         now,
	})
	if err != nil {
		t.Fatal(err)
	}
	accountHandlers, err := account.NewHandlers(account.HandlerConfig{
		Service: service, StartSession: startSession,
		EndSession:      func(context.Context, http.ResponseWriter, *http.Request) error { return nil },
		CurrentIdentity: func(*http.Request) (account.Identity, bool, error) { return account.Identity{}, false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	const login, password = "dana@example.test", "fake-example-password-three"
	_, enrolment, err := service.SignUp(ctx, login, password)
	if err != nil {
		t.Fatal(err)
	}
	code := func() string {
		c, err := account.TOTPCode(enrolment.Secret, clock)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if _, err := service.ConfirmTOTP(ctx, login, password, code()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)

	raw, _ := json.Marshal(account.CredentialsRequest{Login: login, Password: password, Code: code()})
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/login", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	accountHandlers.Login().ServeHTTP(w, r)
	if w.Code != http.StatusOK || len(started) != 1 {
		t.Fatalf("totp login: %d %s", w.Code, w.Body)
	}
	viaTOTP := started[0]

	// The passkey store answers the account's id and login the way an adapter
	// joining the account table does: from the account row.
	rec, err := accounts.FindByLogin(ctx, login)
	if err != nil {
		t.Fatal(err)
	}
	passkeys := passkeytest.NewMemoryStore()
	passkeys.PutAccount(account.Identity{ID: rec.ID, Login: rec.Login})

	p := &rp{t: t, store: passkeys, clock: clock}
	h, err := passkey.New(passkey.Config{
		RPID: testRPID, RPDisplayName: "Example App", Origins: []string{testOrigin}, Store: passkeys,
		StartSession: startSession,
		CurrentAccount: func(*http.Request) (account.Identity, time.Time, bool, error) {
			return viaTOTP, clock, true, nil
		},
		OtherFactors: func(context.Context, account.Identity) (int, error) { return 1, nil },
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.h = h

	a := newAuthenticator(t)
	p.mustRegister(a)
	if w := p.login(a); w.Code != http.StatusOK {
		t.Fatalf("passkey login: %d %s", w.Code, w.Body)
	}
	if len(started) != 2 {
		t.Fatalf("%d sessions started, want 2", len(started))
	}
	viaPasskey := started[1]

	if viaPasskey != viaTOTP {
		t.Fatalf("identities differ: passkey %+v, totp %+v", viaPasskey, viaTOTP)
	}
	if viaTOTP.ID == "" || viaTOTP.Login != login {
		t.Fatalf("totp identity %+v is not the account", viaTOTP)
	}
}
