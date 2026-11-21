// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

// hosts a Config written in code could hold, each with the fake token.
var leakyHosts = []string{
	"db.example.invalid?password=s3cr3tTOKEN",
	"db.example.invalid/s3cr3tTOKEN",
	"s3cr3tTOKEN.example.invalid",
}

// unreachableDB is a DB over a real pgx pool whose every dial fails with
// dialErr, so pgx builds its own connect error, which quotes host, with no
// socket and no lookup.
func unreachableDB(t *testing.T, host string, dialErr error) *DB {
	t.Helper()
	pc, err := pgxpool.ParseConfig("postgres://u@localhost:5432/n?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.Host = host
	pc.ConnConfig.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"192.0.2.1"}, nil }
	pc.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) { return nil, dialErr }
	pool, err := pgxpool.NewWithConfig(t.Context(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &DB{pool: pool, pinger: fakePinger{}, ledger: queryLedger{q: pool}, pause: time.Millisecond}
}

func refused() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

var oneMigration = fstest.MapFS{"m/0001_a.sql": {Data: []byte("select 1")}}

// Migrate runs before the database is up on every pod start. Its error
// reaches the startup log, so it holds the kind of the failure and not the
// host pgx quotes.
func TestMigrateKeepsTheHostOutOfTheConnectError(t *testing.T) {
	t.Parallel()

	for _, host := range leakyHosts {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			db := unreachableDB(t, host, refused())
			err := db.Migrate(t.Context(), oneMigration, "m")
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "database: create schema_migrations: begin: connection refused")
			if strings.Contains(err.Error(), "db.example.invalid") {
				t.Errorf("error %q holds the host", err)
			}
			if !errors.Is(err, syscall.ECONNREFUSED) {
				t.Errorf("error %v lost the errno", err)
			}
		})
	}
}

// The retry loop logs every failed attempt, and a context that ends joins the
// last error into the one it returns. Neither carries the host.
func TestMigrateUntilReadyKeepsTheHostOutOfLogAndError(t *testing.T) {
	t.Parallel()

	for name, dialErr := range map[string]error{
		"refused": refused(),
		"dns":     &net.DNSError{Err: "no such host", Name: leakyHosts[0], IsNotFound: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := unreachableDB(t, leakyHosts[0], dialErr)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()

			err := db.MigrateUntilReady(ctx, logger, oneMigration, "m")
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "database: migrate")
			if !strings.Contains(logs.String(), "database schema not applied yet") {
				t.Fatalf("log %q has no retry line: the test proves nothing", logs.String())
			}
			for _, secret := range []string{"s3cr3tTOKEN", "db.example.invalid", "192.0.2.1"} {
				if strings.Contains(logs.String(), secret) || strings.Contains(err.Error(), secret) {
					t.Errorf("log or error holds %q: %s / %v", secret, logs.String(), err)
				}
			}
		})
	}
}

// Check reads schema_migrations through the pool after the ping, and that
// read can dial too.
func TestCheckKeepsTheHostOutOfTheLedgerError(t *testing.T) {
	t.Parallel()

	for _, host := range leakyHosts {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			db := unreachableDB(t, host, refused())
			db.migrated.Store(true)
			files := []migration{}
			db.files.Store(&files)
			err := db.Check(t.Context())
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "database: verify migrations: read schema_migrations: connection refused")
		})
	}
}
