// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Without DB_HOST the example has no database and readiness does not wait
// for one.
func TestExampleRunsWithoutADatabase(t *testing.T) {
	t.Parallel()

	a, _ := newTestApp(t, 10)
	if a.db != nil {
		t.Fatal("a database was opened without DB_HOST")
	}
	rec := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /readyz = %d, want 200 without a database", rec.Code)
	}
}

// With DB_HOST the pool is opened, which does not connect, so this needs no
// server.
func TestExampleOpensTheDatabaseWithDBHost(t *testing.T) {
	t.Parallel()

	a, _ := newTestAppEnv(t, 10, map[string]string{
		"DB_HOST": "db.example.invalid", "DB_NAME": "govue", "DB_USER": "govue",
		"DB_PASSWORD": "fake-password-not-real", "DB_SSLMODE": "disable",
	})
	t.Cleanup(func() { _ = a.close() })
	if a.db == nil {
		t.Fatal("no database was opened with DB_HOST set")
	}
}

// The database settings join the example's one aggregated error, and the
// default verify-full asks for its root certificate.
func TestExampleDatabaseConfigProblemsAreAggregated(t *testing.T) {
	t.Parallel()

	env := testEnv(t, 10)
	env["DB_HOST"] = "db.example.invalid"
	env["OIDC_ISSUER"] = ""
	_, err := loadConfig(func(key string) string { return env[key] })
	if err == nil {
		t.Fatal("loadConfig() error = nil")
	}
	for _, want := range []string{"OIDC_ISSUER", "DB_NAME", "DB_USER", "DB_PASSWORD", "needs a root certificate (DB_SSLROOTCERT)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %s:\n%v", want, err)
		}
	}
}

// The embedded migrations are where MigrateUntilReady looks for them.
func TestExampleEmbedsItsMigrations(t *testing.T) {
	t.Parallel()

	files, err := fs.Glob(migrationFiles, migrationDir+"/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration embedded under %s: %v", migrationDir, err)
	}
}
