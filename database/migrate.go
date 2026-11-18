// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
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

type migration struct {
	version int
	name    string
	sql     string
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
		out = append(out, migration{version: version, name: entry.Name(), sql: string(body)})
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

// Migrate applies every migration in dir of fsys that is not recorded yet,
// each in its own transaction together with its bookkeeping row in
// schema_migrations: a file that fails leaves nothing of itself behind. After
// it returns nil, Check stops reporting ErrNotMigrated.
func (db *DB) Migrate(ctx context.Context, fsys fs.FS, dir string) error {
	files, err := readMigrations(fsys, dir)
	if err != nil {
		return err
	}
	return db.apply(ctx, files)
}

func (db *DB) apply(ctx context.Context, files []migration) error {
	// Inside the lock as well: two pods starting together would otherwise race
	// on create table if not exists, which PostgreSQL answers with a duplicate
	// key error on its catalogue rather than with a no-op.
	err := db.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		_, err := tx.Exec(ctx, `
			create table if not exists schema_migrations (
				version    integer     primary key,
				name       text        not null,
				applied_at timestamptz not null default now()
			)`)
		return err
	})
	if err != nil {
		return fmt.Errorf("database: create schema_migrations: %w", err)
	}

	for _, m := range files {
		if err := db.applyOne(ctx, m); err != nil {
			return fmt.Errorf("database: migration %s: %w", m.name, err)
		}
	}
	db.migrated.Store(true)
	return nil
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
		if _, err := tx.Exec(ctx,
			"insert into schema_migrations (version, name) values ($1, $2)", m.version, m.name,
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
// waiting does not fix it. Everything else is retried and never given up on:
// a database that starts after the pod does is the normal case in a cluster,
// and Check reports the gap meanwhile. The pause starts at one second, doubles
// up to thirty seconds, and ends early when ctx does. A nil logger is an
// error.
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
	if err := retry(ctx, logger, db.pause, func(ctx context.Context) error { return db.apply(ctx, files) }); err != nil {
		return err
	}
	logger.InfoContext(ctx, "database schema applied", "migrations", len(files))
	return nil
}

// retry calls attempt until it returns nil or ctx ends. Each attempt has its
// own timeout. The pause between two attempts starts at pause, or
// defaultPause when it is zero, and doubles up to maxPause.
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
