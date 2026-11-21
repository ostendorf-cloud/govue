// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package database gives a product its PostgreSQL connection: the
// configuration from the environment, a pgx pool, an embedded migration
// runner and a readiness check for health.
//
// It is the one package of this module that imports github.com/jackc/pgx/v5,
// so an application that does not use it never pulls pgx in. The product
// keeps its schema, its queries and its store types. This package has no
// query of its own beyond the migration bookkeeping.
//
// The usual wiring in main:
//
//	cfg, err := database.LoadConfig(os.Getenv)
//	db, err := database.Open(ctx, cfg)
//	defer db.Close()
//	registry.Add("database", 0, db.Check)
//	go db.MigrateUntilReady(ctx, logger, migrations, "migrations")
//
// Open does not connect, so a database that starts after the pod does not
// stop it. Readiness stays unready until the database answers and the schema
// is applied. Liveness never depends on the database: health.Registry.Live
// runs no check.
//
// TLS follows libpq's sslmode. The default is verify-full, which needs the
// root certificate that signed the server's certificate. pgx also reads the
// PG* environment variables and the files under ~/.postgresql for settings
// this Config does not name, such as a client certificate. Every setting the
// Config names wins over them.
package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool lifetimes. A connection the pool keeps for hours survives a failover
// of the cluster only as a broken socket, so connections are recycled.
const (
	maxConnLifetime   = 30 * time.Minute
	maxConnIdleTime   = 5 * time.Minute
	healthCheckPeriod = time.Minute
)

// ErrNotMigrated is what Check returns while the database answers but no
// Migrate in this process has succeeded yet.
var ErrNotMigrated = errors.New("database: schema not applied yet")

// pinger is the part of the pool Check needs. Tests replace it.
type pinger interface {
	Ping(ctx context.Context) error
}

// DB is the pool together with the state of the schema. Build one with Open.
type DB struct {
	pool     *pgxpool.Pool
	pinger   pinger
	migrated atomic.Bool
	// files are the migrations of the last successful run, which Check
	// compares with the recorded rows. ledger reads those rows.
	files        atomic.Pointer[[]migration]
	ledger       ledger
	allowChanged bool
	// pause is the first wait between two migration attempts. Zero means
	// the default. Tests set it.
	pause time.Duration
}

// Open validates cfg and builds the pool. It does not connect: the first
// query does, so a database that is still starting does not stop the
// process. Check reports the gap.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	pc, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("database: pool: %w", err)
	}
	return &DB{pool: pool, pinger: pool, ledger: queryLedger{q: pool}, allowChanged: cfg.AllowChangedMigrations}, nil
}

// Pool is the pgx pool the product runs its queries on. Returning pgx' own
// type makes pgx v5 part of this package's contract: a pgx v6 would need a
// new major version of govue. That is accepted on purpose, see the concept
// doc.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Close waits for the pool's connections to be returned and closes them.
func (db *DB) Close() {
	db.pool.Close()
}

// Check is the readiness check, a health.Check: register it with
// registry.Add("database", 0, db.Check). It fails while the database does
// not answer, and with ErrNotMigrated until Migrate or MigrateUntilReady has
// applied the schema in this process. After that it fails when an applied
// file differs from its recorded checksum, see ErrChangedMigration. Recorded
// versions above the last file are ignored: code older than the schema stays
// ready, which a rolling deploy and a rollback need. It reads the small
// schema_migrations table on each call. Its error goes to the log, never to
// the client: health names the check and nothing else. A failed ping names
// the kind of failure only, never the host, the address or pgx' own text.
// A context error and an errno stay matchable with errors.Is.
//
// The ping needs a connection from the pool. When every connection is busy,
// it waits for one until health's timeout for the check ends, and readiness
// fails. Under an overload that saturates every pod's pool, every pod goes
// unready together. That is accepted: a pod that cannot get a connection
// cannot serve a request that needs one either. Size MaxConns for the
// concurrency a pod serves, and give the check a timeout in health.
func (db *DB) Check(ctx context.Context) error {
	if err := db.pinger.Ping(ctx); err != nil {
		return pingFailure(err)
	}
	if !db.migrated.Load() {
		return ErrNotMigrated
	}
	return db.verifyApplied(ctx)
}

// verifyApplied fails when an applied file changed. It checks the versions
// this code has a file for, also under AllowChangedMigrations: the run that
// accepted an edit stored the new checksum, so a pod of the other version
// that still holds the old file turns unready until it is rolled over.
func (db *DB) verifyApplied(ctx context.Context) error {
	files := db.files.Load()
	if files == nil || db.ledger == nil {
		return nil
	}
	rows, err := db.ledger.records(ctx)
	if err != nil {
		return fmt.Errorf("database: verify migrations: %w", err)
	}
	return compare(*files, rows).err()
}

// dsn is the connection string without the password, which is set on the
// parsed config instead: a password with a %, @ or : in it would otherwise
// have to be escaped correctly in two places, and a DSN is the thing that
// gets logged. The user goes through url.User and the host through
// net.JoinHostPort, so an odd user name or an IPv6 address cannot change
// what the string means.
func dsn(cfg Config) string {
	q := url.Values{}
	q.Set("sslmode", string(cfg.SSLMode))
	if cfg.SSLRootCert != "" {
		q.Set("sslrootcert", cfg.SSLRootCert)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.User(cfg.User),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Name,
		RawQuery: q.Encode(),
	}
	return u.String()
}

// poolConfig validates cfg and turns it into the pool's configuration. pgx
// builds the TLS configuration from sslmode and sslrootcert while it parses,
// the same way libpq does.
func poolConfig(cfg Config) (*pgxpool.Config, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	pc, err := pgxpool.ParseConfig(dsn(cfg))
	if err != nil {
		// pgx' parse error quotes the connection string, host included, so
		// it is not wrapped. Validate has refused what pgx cannot read, so
		// this is a version difference of pgx and not an input.
		return nil, errors.New("database: parse config: pgx did not accept the connection settings (the value is not shown)")
	}
	pc.ConnConfig.Password = cfg.Password.Reveal()
	pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	pc.MaxConns = cfg.MaxConns
	pc.MaxConnLifetime = maxConnLifetime
	pc.MaxConnIdleTime = maxConnIdleTime
	pc.HealthCheckPeriod = healthCheckPeriod
	return pc, nil
}
