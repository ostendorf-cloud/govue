// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database_test

// These tests run against a real PostgreSQL server named by
// GOVUE_TEST_DATABASE_URL, see database/dbtest. Without it they skip loudly,
// and in CI they fail instead of skipping. They are the one deliberate
// exception to "tests use no socket", see AGENTS.md.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// Each file takes the lock on its own, not only the creation of the
// bookkeeping table: with file 1 applied and the table there, a held lock
// still keeps file 2 from running.
func TestPostgresEachFileWaitsForTheLock(t *testing.T) {
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

	first := fstest.MapFS{"m/0001_notes.sql": schema["m/0001_notes.sql"]}
	if err := migrator.Migrate(t.Context(), first, "m"); err != nil {
		t.Fatalf("Migrate() of file 1: %v", err)
	}

	tx, err := holder.Pool().Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "select pg_advisory_xact_lock($1)", database.MigrationLock); err != nil {
		t.Fatalf("take the lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	err = migrator.ApplyFile(ctx, 2, "0002_author.sql", "alter table notes add column author text")
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyFile() error = %v, want it to wait for the lock until its deadline", err)
	}
	if n := countRows(t, holder, "select count(*) from information_schema.columns where table_name = 'notes' and column_name = 'author'"); n != 0 {
		t.Error("file 2 ran while another migrator held the lock")
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
	cfg.Password = database.NewSecret("wrong-fake-password")
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

// The checksum tests below. edited is schema with the first file changed.
func edited(first string) fstest.MapFS {
	return fstest.MapFS{"m/0001_notes.sql": sqlFile(first), "m/0002_author.sql": schema["m/0002_author.sql"]}
}

const editedFirst = "create table notes (id bigint generated always as identity primary key, body text not null) -- fake-edit-marker"

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func openWith(t *testing.T, cfg database.Config) *database.DB {
	t.Helper()
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func storedSum(t *testing.T, db *database.DB, version int) string {
	t.Helper()
	var sum *string
	if err := db.Pool().QueryRow(t.Context(), "select checksum from schema_migrations where version = $1", version).Scan(&sum); err != nil {
		t.Fatalf("read checksum of %d: %v", version, err)
	}
	if sum == nil {
		return ""
	}
	return *sum
}

// A clean run stores the SHA-256 of each file's bytes.
func TestPostgresMigrateRecordsTheChecksum(t *testing.T) {
	db := dbtest.Open(t)

	if err := db.Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	for version, file := range map[int]string{1: "m/0001_notes.sql", 2: "m/0002_author.sql"} {
		if got, want := storedSum(t, db, version), hexSum(string(schema[file].Data)); got != want {
			t.Errorf("checksum of version %d = %q, want %q", version, got, want)
		}
	}
}

// A changed file stops the run before a newer file is applied. The error
// names the file and carries neither its content nor a checksum, and
// readiness stays down.
func TestPostgresAChangedFileRefusesTheRun(t *testing.T) {
	cfg := dbtest.Config(t)
	first := openWith(t, cfg)
	if err := first.Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	changed := edited(editedFirst)
	changed["m/0003_extra.sql"] = sqlFile("create table extra (id int)")
	second := openWith(t, cfg)
	err := second.Migrate(t.Context(), changed, "m")
	if !errors.Is(err, database.ErrChangedMigration) {
		t.Fatalf("Migrate() error = %v, want ErrChangedMigration", err)
	}
	if !strings.Contains(err.Error(), "0001_notes.sql") || !strings.Contains(err.Error(), "version 1") {
		t.Errorf("error %q does not name the file and version", err)
	}
	for _, bad := range []string{"fake-edit-marker", hexSum(editedFirst), hexSum(string(schema["m/0001_notes.sql"].Data))} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error carries %q", bad)
		}
	}
	if tableExists(t, first, "extra") {
		t.Error("a newer file was applied although an older one changed")
	}
	if err := second.Check(t.Context()); !errors.Is(err, database.ErrNotMigrated) {
		t.Errorf("Check() after the refusal = %v, want not ready", err)
	}
	if got, want := storedSum(t, first, 1), hexSum(string(schema["m/0001_notes.sql"].Data)); got != want {
		t.Error("the refused run changed the stored checksum")
	}
}

// A line ending change is a change: the rule is raw bytes.
func TestPostgresALineEndingChangeIsAChange(t *testing.T) {
	cfg := dbtest.Config(t)
	if err := openWith(t, cfg).Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	crlf := strings.ReplaceAll(string(schema["m/0001_notes.sql"].Data), " ", "\r\n")
	err := openWith(t, cfg).Migrate(t.Context(), edited(crlf), "m")
	if !errors.Is(err, database.ErrChangedMigration) {
		t.Errorf("Migrate() with other line endings = %v, want ErrChangedMigration", err)
	}
}

// MigrateUntilReady does not retry a changed file.
func TestPostgresMigrateUntilReadyStopsOnAChangedFile(t *testing.T) {
	cfg := dbtest.Config(t)
	if err := openWith(t, cfg).Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	err := openWith(t, cfg).MigrateUntilReady(ctx, slog.New(slog.NewTextHandler(t.Output(), nil)), edited(editedFirst), "m")
	if !errors.Is(err, database.ErrChangedMigration) {
		t.Errorf("MigrateUntilReady() = %v, want ErrChangedMigration at once", err)
	}
}

// Rows without a checksum, from a table made before the column existed and
// from a NULL in a table that has it, are filled on the next run.
func TestPostgresRowsWithoutAChecksumAreFilled(t *testing.T) {
	cfg := dbtest.Config(t)
	db := openWith(t, cfg)
	if err := db.Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	ctx := t.Context()

	// A NULL checksum in a table that has the column.
	if _, err := db.Pool().Exec(ctx, "update schema_migrations set checksum = null where version = 1"); err != nil {
		t.Fatalf("clear checksum: %v", err)
	}
	if err := openWith(t, cfg).Migrate(ctx, schema, "m"); err != nil {
		t.Fatalf("Migrate() with a NULL checksum = %v, want it filled", err)
	}
	if got, want := storedSum(t, db, 1), hexSum(string(schema["m/0001_notes.sql"].Data)); got != want {
		t.Errorf("filled checksum = %q, want %q", got, want)
	}

	// A table of the shape before checksums: no column at all.
	if _, err := db.Pool().Exec(ctx, "alter table schema_migrations drop column checksum"); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if err := openWith(t, cfg).Migrate(ctx, schema, "m"); err != nil {
		t.Fatalf("Migrate() on a table without the column = %v", err)
	}
	for version, file := range map[int]string{1: "m/0001_notes.sql", 2: "m/0002_author.sql"} {
		if got, want := storedSum(t, db, version), hexSum(string(schema[file].Data)); got != want {
			t.Errorf("checksum of version %d after the column was added = %q, want %q", version, got, want)
		}
	}
	if n := countRows(t, db, "select count(*) from schema_migrations"); n != 2 {
		t.Errorf("schema_migrations has %d rows, want 2: the fill must not apply a file again", n)
	}

	// A legacy table written by SQL alone, with a file still to apply.
	legacy := dbtest.Config(t)
	ldb := openWith(t, legacy)
	if _, err := ldb.Pool().Exec(ctx, `
		create table schema_migrations (version integer primary key, name text not null, applied_at timestamptz not null default now());
		create table notes (id bigint generated always as identity primary key, body text not null);
		insert into schema_migrations (version, name) values (1, '0001_notes.sql')`); err != nil {
		t.Fatalf("legacy setup: %v", err)
	}
	if err := ldb.Migrate(ctx, schema, "m"); err != nil {
		t.Fatalf("Migrate() on a legacy table = %v", err)
	}
	if got, want := storedSum(t, ldb, 1), hexSum(string(schema["m/0001_notes.sql"].Data)); got != want {
		t.Errorf("legacy row checksum = %q, want %q", got, want)
	}
	if got, want := storedSum(t, ldb, 2), hexSum(string(schema["m/0002_author.sql"].Data)); got != want {
		t.Errorf("new row checksum = %q, want %q", got, want)
	}
}

// A recorded version with no file refuses the run, and names the file the
// row recorded.
func TestPostgresARecordedVersionWithoutAFileRefuses(t *testing.T) {
	cfg := dbtest.Config(t)
	if err := openWith(t, cfg).Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	older := fstest.MapFS{"m/0001_notes.sql": schema["m/0001_notes.sql"]}
	db := openWith(t, cfg)
	err := db.Migrate(t.Context(), older, "m")
	if !errors.Is(err, database.ErrMissingMigration) {
		t.Fatalf("Migrate() = %v, want ErrMissingMigration", err)
	}
	if !strings.Contains(err.Error(), "0002_author.sql") {
		t.Errorf("error %q does not name the recorded file", err)
	}
}

// The option accepts a changed file, once: the stored checksum follows the
// file, so the next run needs no option.
func TestPostgresAllowChangedMigrations(t *testing.T) {
	cfg := dbtest.Config(t)
	if err := openWith(t, cfg).Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	changed := edited(editedFirst)

	allow := cfg
	allow.AllowChangedMigrations = true
	db := openWith(t, allow)
	if err := db.Migrate(t.Context(), changed, "m"); err != nil {
		t.Fatalf("Migrate() with the option = %v", err)
	}
	if err := db.Check(t.Context()); err != nil {
		t.Errorf("Check() with the option = %v", err)
	}
	if got, want := storedSum(t, db, 1), hexSum(editedFirst); got != want {
		t.Errorf("checksum after the option = %q, want the new file's", got)
	}
	strict := openWith(t, cfg)
	if err := strict.Migrate(t.Context(), changed, "m"); err != nil {
		t.Errorf("Migrate() without the option after it accepted the edit = %v", err)
	}

	// A version with no file is accepted too.
	older := fstest.MapFS{"m/0001_notes.sql": sqlFile(editedFirst)}
	if err := openWith(t, allow).Migrate(t.Context(), older, "m"); err != nil {
		t.Errorf("Migrate() of a missing version with the option = %v", err)
	}
}

// Readiness fails when an applied file and its recorded checksum differ,
// and when a recorded version has no file, after the process migrated.
func TestPostgresCheckIsNotReadyAfterAnEdit(t *testing.T) {
	db := dbtest.Open(t)
	if err := db.Migrate(t.Context(), schema, "m"); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := db.Check(t.Context()); err != nil {
		t.Fatalf("Check() after Migrate = %v", err)
	}

	if _, err := db.Pool().Exec(t.Context(), "update schema_migrations set checksum = $1 where version = 1", hexSum(editedFirst)); err != nil {
		t.Fatalf("edit the record: %v", err)
	}
	if err := db.Check(t.Context()); !errors.Is(err, database.ErrChangedMigration) {
		t.Errorf("Check() after an edit = %v, want ErrChangedMigration", err)
	}

	if _, err := db.Pool().Exec(t.Context(), "update schema_migrations set checksum = $1 where version = 1",
		hexSum(string(schema["m/0001_notes.sql"].Data))); err != nil {
		t.Fatalf("restore the record: %v", err)
	}
	if _, err := db.Pool().Exec(t.Context(), "insert into schema_migrations (version, name, checksum) values (3, '0003_new.sql', 'x')"); err != nil {
		t.Fatalf("add a version: %v", err)
	}
	if err := db.Check(t.Context()); !errors.Is(err, database.ErrMissingMigration) {
		t.Errorf("Check() with a version that has no file = %v, want ErrMissingMigration", err)
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
