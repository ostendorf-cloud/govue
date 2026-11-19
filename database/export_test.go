// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import "context"

// MigrationLock is the advisory lock key, for the real database tests in
// package database_test that hold it to block a migrator.
const MigrationLock = migrationLock

// ApplyFile applies one migration file the way Migrate does after the
// bookkeeping table exists, so a test can prove the lock of each file on its
// own, apart from the lock around the table's creation.
func (db *DB) ApplyFile(ctx context.Context, version int, name, sql string) error {
	return db.applyOne(ctx, migration{version: version, name: name, sql: sql, sum: checksum([]byte(sql))})
}
