// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package dbtest gives a test its own database on a real PostgreSQL server,
// with no fake in between.
//
// The server's address comes from the environment variable named by EnvURL.
// Locally, where no server is configured, a test that calls Config or Open is
// skipped with a message naming the variable. With CI set to true or 1, a
// missing value fails the test instead, so a suite can never go green by
// skipping everything.
//
// This package imports database and through it pgx. It imports pgx nowhere
// itself.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/database"
)

// EnvURL names the variable holding the test server's URL, for example
// postgres://govue:govue@localhost:5432/postgres?sslmode=disable. The URL
// names its sslmode, and sslrootcert with a verifying mode. The role needs
// the CREATEDB privilege: every test gets a database of its own.
const EnvURL = "GOVUE_TEST_DATABASE_URL"

// setupTimeout bounds creating and dropping one test database.
const setupTimeout = 30 * time.Second

// Config creates an empty database on the configured server and returns the
// settings that reach it. The database is dropped, with force, when the test
// ends.
//
// With no server configured the test is skipped, loudly, unless the CI
// environment variable is set, in which case it fails.
func Config(t testing.TB) database.Config {
	t.Helper()

	raw := os.Getenv(EnvURL)
	if skip, msg := missing(raw, os.Getenv("CI")); msg != "" {
		if skip {
			t.Skip(msg)
		}
		t.Fatal(msg)
	}
	admin, err := parse(raw)
	if err != nil {
		t.Fatalf("dbtest: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	conn, err := database.Open(ctx, admin)
	if err != nil {
		t.Fatalf("dbtest: open the server named by %s: %v", EnvURL, err)
	}
	defer conn.Close()

	name := "govue_test_" + randomSuffix(t)
	// name is built from hex digits only, so quoting it is enough.
	if _, err := conn.Pool().Exec(ctx, `create database "`+name+`"`); err != nil {
		t.Fatalf("dbtest: create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
		defer cancel()
		conn, err := database.Open(ctx, admin)
		if err != nil {
			t.Errorf("dbtest: open the server to drop %s: %v", name, err)
			return
		}
		defer conn.Close()
		if _, err := conn.Pool().Exec(ctx, `drop database if exists "`+name+`" with (force)`); err != nil {
			t.Errorf("dbtest: drop test database %s: %v", name, err)
		}
	})

	admin.Name = name
	return admin
}

// Open is Config followed by database.Open. The pool is closed when the test
// ends, before the database is dropped.
func Open(t testing.TB) *database.DB {
	t.Helper()

	cfg := Config(t)
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dbtest: open the test database: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// missing decides what happens without a URL: nothing when it is set, a
// skip locally, a failure in CI. msg is empty when the test may go on.
func missing(raw, ci string) (skip bool, msg string) {
	if raw != "" {
		return false, ""
	}
	if isCI(ci) {
		return false, fmt.Sprintf("%s is not set in CI: the PostgreSQL service container is missing from the workflow", EnvURL)
	}
	return true, fmt.Sprintf("SKIPPED, NOT RUN: %s is not set, so this test against a real PostgreSQL did not run. "+
		"Start a server (docker compose -f example/compose.yaml up -d postgres) and set it, see AGENTS.md.", EnvURL)
}

// isCI reads the CI variable the way the CI systems set it: true or 1. A
// false or 0 a developer exported to switch it off does not count.
func isCI(ci string) bool {
	return strings.EqualFold(strings.TrimSpace(ci), "true") || strings.TrimSpace(ci) == "1"
}

// parse turns the URL into the admin connection's settings. Its errors never
// quote the URL, which holds a password.
func parse(raw string) (database.Config, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || u.User == nil {
		return database.Config{}, fmt.Errorf("%s must be a postgres:// URL with a user, a password and a host", EnvURL)
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return database.Config{}, fmt.Errorf("%s has no password", EnvURL)
	}
	port := database.DefaultPort
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return database.Config{}, fmt.Errorf("%s has a port that is not a number", EnvURL)
		}
	}
	q := u.Query()
	mode := q.Get("sslmode")
	if mode == "" {
		return database.Config{}, fmt.Errorf("%s names no sslmode, add ?sslmode=disable for a local server", EnvURL)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		name = "postgres"
	}
	cfg := database.Config{
		Host:           u.Hostname(),
		Port:           port,
		Name:           name,
		User:           u.User.Username(),
		Password:       database.NewSecret(password),
		SSLMode:        database.SSLMode(mode),
		SSLRootCert:    q.Get("sslrootcert"),
		MaxConns:       4,
		ConnectTimeout: 5 * time.Second,
	}
	if err := cfg.Validate(); err != nil {
		return database.Config{}, fmt.Errorf("%s: %w", EnvURL, err)
	}
	return cfg, nil
}

func randomSuffix(t testing.TB) string {
	t.Helper()

	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("dbtest: random suffix: %v", err)
	}
	return hex.EncodeToString(b)
}
