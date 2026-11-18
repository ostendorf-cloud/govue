// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

// MigrationLock is the advisory lock key, for the real database tests in
// package database_test that hold it to block a migrator.
const MigrationLock = migrationLock
