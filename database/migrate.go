// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The migrations are plain SQL files a product embeds, applied in order by
// the small runner below. A migration library would add a dependency and a
// second dialect for a schema that is a handful of files.
//
// Each file runs in one transaction together with its bookkeeping row, so a
// statement that cannot run inside a transaction, such as
// "create index concurrently", cannot be a migration here. A change like that
// needs the runner extended first.

// migrationLock is an arbitrary but fixed advisory lock key. Every pod runs
// Migrate at start, and the lock makes the second one wait instead of applying
// the same file twice. It is the key the first product's own runner used, so
// a pod with the old runner and one with this package exclude each other.
const migrationLock = 8153_2026

// The retry pause of MigrateUntilReady, and the bound of one attempt.
const (
	defaultPause   = time.Second
	maxPause       = 30 * time.Second
	attemptTimeout = time.Minute
)

// ErrChangedMigration is wrapped by the error of a run that found a file
// whose checksum differs from the one recorded when it was applied. The error
// names the file and its version, never its content.
var ErrChangedMigration = errors.New("file changed after it was applied")

// ErrMissingMigration is wrapped by the error of a run that found a recorded
// version with no file, for example after a rollback to code that predates
// it.
var ErrMissingMigration = errors.New("recorded as applied but has no file")

type migration struct {
	version int
	name    string
	sql     string
	// sum is the checksum of the file's exact bytes.
	sum string
}

// checksum is the SHA-256 of the file's exact bytes, in hex. It hashes raw
// bytes: a checkout that turns LF into CRLF changes it. Keep the files LF
// with "*.sql text eol=lf" in .gitattributes.
func checksum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// readMigrations reads the files named NNNN_name.sql in dir of fsys and
// checks their numbering: the versions start at 1 and have no gap and no
// repeat. A file ending in .sql in any spelling must follow the naming, and
// an empty one is refused, so a typo never drops a migration silently. Other
// files and subdirectories are ignored, so a README next to the migrations
// is fine.
func readMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}
	var out []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".sql") {
			return nil, fmt.Errorf("database: migration %s must end in .sql in lower case", entry.Name())
		}
		// 0001_init.sql: the number orders them, the rest is for the reader.
		number, rest, found := strings.Cut(strings.TrimSuffix(entry.Name(), ".sql"), "_")
		if !found || rest == "" {
			return nil, fmt.Errorf("database: migration %s is not named <version>_<name>.sql", entry.Name())
		}
		version, err := strconv.Atoi(number)
		if err != nil || version < 1 || strings.HasPrefix(number, "+") || strings.HasPrefix(number, "-") {
			return nil, fmt.Errorf("database: migration %s has no version number", entry.Name())
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("database: read migration %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("database: migration %s is empty", entry.Name())
		}
		out = append(out, migration{version: version, name: entry.Name(), sql: string(body), sum: checksum(body)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("database: no migration in %s", dir)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("database: migration versions have a gap or a repeat at %s", m.name)
		}
	}
	return out, nil
}

// record is one row of schema_migrations. An empty sum is a row written
// before checksums existed.
type record struct {
	version int
	name    string
	sum     string
}

// comparison is what compare found.
type comparison struct {
	fill    []migration    // recorded without a checksum
	changed []migration    // recorded with another checksum
	missing []int          // recorded versions with no file
	names   map[int]string // recorded names of the missing versions
}

// compare checks every recorded row against the files. It touches nothing.
func compare(files []migration, rows []record) comparison {
	byVersion := make(map[int]migration, len(files))
	for _, m := range files {
		byVersion[m.version] = m
	}
	res := comparison{names: map[int]string{}}
	for _, r := range rows {
		m, ok := byVersion[r.version]
		switch {
		case !ok:
			res.missing = append(res.missing, r.version)
			res.names[r.version] = r.name
		case r.sum == "":
			res.fill = append(res.fill, m)
		case r.sum != m.sum:
			res.changed = append(res.changed, m)
		}
	}
	return res
}

// err is nil when nothing is changed or missing. A row to fill is no error.
// The messages carry names and versions, never content.
func (c comparison) err() error {
	var errs []error
	for _, m := range c.changed {
		errs = append(errs, fmt.Errorf("database: migration %s (version %d): %w", m.name, m.version, ErrChangedMigration))
	}
	for _, v := range c.missing {
		errs = append(errs, fmt.Errorf("database: migration %s (version %d): %w", c.names[v], v, ErrMissingMigration))
	}
	return errors.Join(errs...)
}

// ledger reads the recorded rows. Tests replace it.
type ledger interface {
	records(ctx context.Context) ([]record, error)
}

// querier is what pgx.Tx and the pool share.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type queryLedger struct{ q querier }

func (l queryLedger) records(ctx context.Context) ([]record, error) { return readRecords(ctx, l.q) }

func readRecords(ctx context.Context, q querier) ([]record, error) {
	rows, err := q.Query(ctx, "select version, name, checksum from schema_migrations order by version")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	var out []record
	for rows.Next() {
		var r record
		var sum *string
		if err := rows.Scan(&r.version, &r.name, &sum); err != nil {
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		if sum != nil {
			r.sum = *sum
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return out, nil
}

// Migrate applies every migration in dir of fsys that is not recorded yet,
// each in its own transaction together with its bookkeeping row in
// schema_migrations: a file that fails leaves nothing of itself behind. After
// it returns nil, Check stops reporting ErrNotMigrated.
//
// Before it applies anything, it compares the recorded checksum of every
// applied file with the file and refuses on a difference, see
// ErrChangedMigration and Config.AllowChangedMigrations. Migrate logs
// nothing: MigrateUntilReady takes the logger.
func (db *DB) Migrate(ctx context.Context, fsys fs.FS, dir string) error {
	files, err := readMigrations(fsys, dir)
	if err != nil {
		return err
	}
	return db.apply(ctx, nil, files)
}

func (db *DB) apply(ctx context.Context, logger *slog.Logger, files []migration) error {
	// Inside the lock as well: two pods starting together would otherwise race
	// on create table if not exists, which PostgreSQL answers with a duplicate
	// key error on its catalogue rather than with a no-op. The checksum column
	// is added by an idempotent statement, so a table made before checksums
	// existed gets it.
	err := db.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			create table if not exists schema_migrations (
				version    integer     primary key,
				name       text        not null,
				applied_at timestamptz not null default now(),
				checksum   text
			)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "alter table schema_migrations add column if not exists checksum text")
		return err
	})
	if err != nil {
		return fmt.Errorf("database: create schema_migrations: %w", err)
	}

	// Before any file is applied: a changed file stops the run here.
	if err := db.verify(ctx, logger, files); err != nil {
		return err
	}

	for _, m := range files {
		if err := db.applyOne(ctx, m); err != nil {
			return fmt.Errorf("database: migration %s: %w", m.name, err)
		}
	}
	db.files.Store(&files)
	db.migrated.Store(true)
	return nil
}

// verify compares the recorded rows with files in one transaction under the
// lock. A row without a checksum gets it from its file. A changed file or a
// recorded version with no file is an error, unless AllowChangedMigrations is
// set: then the new checksum replaces the stored one, so the edit is accepted
// once and the option can be removed afterwards.
func (db *DB) verify(ctx context.Context, logger *slog.Logger, files []migration) error {
	return db.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("database: verify migrations: lock: %w", err)
		}
		rows, err := readRecords(ctx, tx)
		if err != nil {
			return fmt.Errorf("database: verify migrations: %w", err)
		}
		res := compare(files, rows)
		if err := res.err(); err != nil {
			if !db.allowChanged {
				return err
			}
			for _, m := range res.changed {
				logWarn(ctx, logger, "applied migration changed, accepted by AllowChangedMigrations", "file", m.name, "version", m.version)
			}
			for _, v := range res.missing {
				logWarn(ctx, logger, "recorded migration has no file, accepted by AllowChangedMigrations", "version", v)
			}
		}
		// Changed files are written only when the option accepted them: without
		// it, the error above returned already.
		write := append(append([]migration{}, res.fill...), res.changed...)
		for _, m := range write {
			if _, err := tx.Exec(ctx,
				"update schema_migrations set checksum = $2 where version = $1", m.version, m.sum,
			); err != nil {
				return fmt.Errorf("database: record checksum of %s: %w", m.name, err)
			}
		}
		for _, m := range res.fill {
			logInfo(ctx, logger, "recorded the checksum of an applied migration", "file", m.name, "version", m.version)
		}
		return nil
	})
}

func logWarn(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	if logger != nil {
		logger.WarnContext(ctx, msg, args...)
	}
}

func logInfo(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	if logger != nil {
		logger.InfoContext(ctx, msg, args...)
	}
}

func (db *DB) applyOne(ctx context.Context, m migration) error {
	return db.tx(ctx, func(tx pgx.Tx) error {
		// Held until the transaction ends, so two pods starting together apply
		// the file once.
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		var applied bool
		if err := tx.QueryRow(ctx,
			"select exists (select 1 from schema_migrations where version = $1)", m.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check: %w", err)
		}
		if applied {
			return nil
		}
		if _, err := tx.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("apply: %w", err)
		}
		// The checksum goes in with the row, in the file's own transaction.
		if _, err := tx.Exec(ctx,
			"insert into schema_migrations (version, name, checksum) values ($1, $2, $3)", m.version, m.name, m.sum,
		); err != nil {
			return fmt.Errorf("record: %w", err)
		}
		return nil
	})
}

// tx runs fn in a transaction and rolls back on any error.
func (db *DB) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		// The rollback of a committed transaction is a no-op, so this needs
		// no flag. Its error is only interesting when fn already failed, and
		// then fn's error is the one returned.
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// MigrateUntilReady applies the migrations at startup, retrying with a
// growing pause while the database is not there yet, and returns once they
// are applied or ctx ends. It runs in its own goroutine, next to the server.
//
// A broken set of files, a gap, a repeat or a bad name, returns at once:
// waiting does not fix it. So does an applied file that changed, see
// ErrChangedMigration, and a recorded version with no file. Everything else
// is retried and never given up on: a database that starts after the pod does
// is the normal case in a cluster, and Check reports the gap meanwhile. The
// pause starts at one second, doubles up to thirty seconds, and ends early
// when ctx does. A nil logger is an error.
func (db *DB) MigrateUntilReady(ctx context.Context, logger *slog.Logger, fsys fs.FS, dir string) error {
	// A nil logger is refused rather than replaced: this package takes a
	// logger and never builds one, and a retry loop that logs nothing would
	// hide why readiness stays at 503.
	if logger == nil {
		return errors.New("database: MigrateUntilReady needs a logger, got nil")
	}
	files, err := readMigrations(fsys, dir)
	if err != nil {
		return err
	}
	if err := retry(ctx, logger, db.pause, func(ctx context.Context) error { return db.apply(ctx, logger, files) }); err != nil {
		return err
	}
	logger.InfoContext(ctx, "database schema applied", "migrations", len(files))
	return nil
}

// retry calls attempt until it returns nil or ctx ends. Each attempt has its
// own timeout. The pause between two attempts starts at pause, or
// defaultPause when it is zero, and doubles up to maxPause. A changed or
// missing migration ends it at once.
func retry(ctx context.Context, logger *slog.Logger, pause time.Duration, attempt func(context.Context) error) error {
	wait := pause
	if wait <= 0 {
		wait = defaultPause
	}
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := attempt(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		// A decision for a person: waiting does not fix it.
		if errors.Is(err, ErrChangedMigration) || errors.Is(err, ErrMissingMigration) {
			return err
		}
		if ctx.Err() != nil {
			return fmt.Errorf("database: migrate: %w", errors.Join(ctx.Err(), err))
		}
		logger.WarnContext(ctx, "database schema not applied yet", "error", err.Error(), "retry_in", wait.String())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("database: migrate: %w", ctx.Err())
		case <-timer.C:
		}
		wait = min(wait*2, maxPause)
	}
}
