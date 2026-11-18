// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database_test

// These tests run against a real PostgreSQL server named by
// GOVUE_TEST_DATABASE_URL, see database/dbtest. Without it they skip loudly,
// and in CI they fail instead of skipping. They are the one deliberate
// exception to "tests use no socket", see AGENTS.md.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ostendorf-cloud/govue/database"
	"github.com/ostendorf-cloud/govue/database/dbtest"
)

func sqlFile(sql string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(sql)} }

// schema is a two file fixture: a table, then a column on it.
var schema = fstest.MapFS{
	"m/0001_notes.sql":  sqlFile("create table notes (id bigint generated always as identity primary key, body text not null)"),
	"m/0002_author.sql": sqlFile("alter table notes add column author text"),
}

func countRows(t *testing.T, db *database.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func tableExists(t *testing.T, db *database.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.Pool().QueryRow(t.Context(), "select to_regclass($1) is not null", name).Scan(&exists); err != nil {
		t.Fatalf("to_regclass(%s): %v", name, err)
	}
	return exists
}

// Connect, then Check before and after Migrate, and Migrate twice.
func TestPostgresMigrateIsIdempotentAndMakesTheCheckReady(t *testing.T) {
	db := dbtest.Open(t)

	if err := db.Pool().Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v: the test database does not answer", err)
	}
	if err := db.Check(t.Context()); !errors.Is(err, database.ErrNotMigrated) {
		t.Fatalf("Check() before Migrate = %v, want ErrNotMigrated", err)
	}
	for range 2 {
		if err := db.Migrate(t.Context(), schema, "m"); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
	}
	if err := db.Check(t.Context()); err != nil {
		t.Errorf("Check() after Migrate = %v, want nil", err)
	}
	if n := countRows(t, db, "select count(*) from schema_migrations"); n != 2 {
		t.Errorf("schema_migrations has %d rows, want 2: a second Migrate must not apply twice", n)
	}
	if n := countRows(t, db, "select count(*) from information_schema.columns where table_name = 'notes' and column_name = 'author'"); n != 1 {
		t.Errorf("the second migration did not run: author column count %d", n)
	}
}

// A file that fails leaves nothing of itself: not its first statement, not
// its bookkeeping row. The files before it stay applied.
func TestPostgresAFailedFileRollsBackCompletely(t *testing.T) {
	db := dbtest.Open(t)

	broken := fstest.MapFS{
		"m/0001_notes.sql":  schema["m/0001_notes.sql"],
		"m/0002_broken.sql": sqlFile("create table half (id int); insert into notes (body) values ('x'); select 1/0"),
	}
	err := db.Migrate(t.Context(), broken, "m")
	if err == nil || !strings.Contains(err.Error(), "0002_broken.sql") {
		t.Fatalf("Migrate() error = %v, want the broken file named", err)
	}
	if tableExists(t, db, "half") {
		t.Error("the failed file's table exists: the file was not rolled back")
	}
	if n := countRows(t, db, "select count(*) from notes"); n != 0 {
		t.Errorf("notes has %d rows: the failed file's insert was not rolled back", n)
	}
	if n := countRows(t, db, "select count(*) from schema_migrations where version = 2"); n != 0 {
		t.Error("the failed file is recorded as applied")
	}
	if n := countRows(t, db, "select count(*) from schema_migrations where version = 1"); n != 1 {
		t.Error("the file before the failed one is not recorded")
	}
	if err := db.Check(t.Context()); !errors.Is(err, database.ErrNotMigrated) {
		t.Errorf("Check() after a failed Migrate = %v, want ErrNotMigrated", err)
	}
}

// A second migrator waits while another one holds the advisory lock, and
// applies nothing until it is released.
func TestPostgresTheAdvisoryLockBlocksASecondMigrator(t *testing.T) {
	cfg := dbtest.Config(t)
	holder, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer holder.Close()
	migrator, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer migrator.Close()

	tx, err := holder.Pool().Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "select pg_advisory_xact_lock($1)", database.MigrationLock); err != nil {
		t.Fatalf("take the lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	err = migrator.Migrate(ctx, schema, "m")
	cancel()
	if err == nil {
		t.Fatal("Migrate() succeeded while another migrator held the lock")
	}
	if tableExists(t, holder, "notes") {
		t.Error("a migration ran while another migrator held the lock")
	}

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	if err := migrator.Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() after the lock was released: %v", err)
	}
}

// Four pods starting together all succeed and apply each file once.
func TestPostgresConcurrentMigratorsApplyOnce(t *testing.T) {
	cfg := dbtest.Config(t)

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			errs <- db.Migrate(t.Context(), schema, "m")
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate() error = %v", err)
		}
	}

	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if n := countRows(t, db, "select count(*) from schema_migrations"); n != 2 {
		t.Errorf("schema_migrations has %d rows, want 2", n)
	}
}

// MigrateUntilReady against a server that is there applies at once.
func TestPostgresMigrateUntilReady(t *testing.T) {
	db := dbtest.Open(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := db.MigrateUntilReady(ctx, slog.New(slog.NewTextHandler(t.Output(), nil)), schema, "m"); err != nil {
		t.Fatalf("MigrateUntilReady() error = %v", err)
	}
	if err := db.Check(t.Context()); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}
}

// A wrong password is refused, and the error does not carry it.
func TestPostgresWrongPasswordIsRefused(t *testing.T) {
	cfg := dbtest.Config(t)
	cfg.Password = "wrong-fake-password"
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	err = db.Check(t.Context())
	if err == nil {
		t.Fatal("Check() = nil with a wrong password")
	}
	if strings.Contains(err.Error(), "wrong-fake-password") {
		t.Errorf("the error leaks the password: %v", err)
	}
}

// Every test gets its own database, and it is gone after the test.
func TestPostgresEveryTestGetsItsOwnDatabase(t *testing.T) {
	// First, so the whole test skips without a server.
	db := dbtest.Open(t)

	var first, second database.Config
	t.Run("first", func(t *testing.T) { first = dbtest.Config(t) })
	t.Run("second", func(t *testing.T) { second = dbtest.Config(t) })
	if first.Name == "" || first.Name == second.Name {
		t.Fatalf("database names %q and %q, want two distinct ones", first.Name, second.Name)
	}
	for _, name := range []string{first.Name, second.Name} {
		if n := countRows(t, db, "select count(*) from pg_database where datname = $1", name); n != 0 {
			t.Errorf("database %s still exists after its test ended", name)
		}
	}
}
