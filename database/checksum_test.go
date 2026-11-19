// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The rule: the checksum is the SHA-256 of the exact bytes. A CRLF checkout
// is a different file.
func TestChecksumHashesTheExactBytes(t *testing.T) {
	t.Parallel()

	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := checksum(nil); got != empty {
		t.Errorf("checksum(nil) = %s, want the SHA-256 of nothing", got)
	}
	if first, second := checksum([]byte("a\n")), checksum([]byte("a\n")); first != second {
		t.Error("the checksum is not stable")
	}
	for name, other := range map[string]string{
		"crlf":            "a\r\n",
		"no newline":      "a",
		"trailing space":  "a \n",
		"other content":   "b\n",
		"leading newline": "\na\n",
	} {
		if checksum([]byte("a\n")) == checksum([]byte(other)) {
			t.Errorf("%s: the checksum did not change", name)
		}
	}
	if len(checksum([]byte("a"))) != 64 {
		t.Error("the checksum is not 64 hex characters")
	}
}

func TestReadMigrationsSetsTheChecksum(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"m/0001_a.sql": {Data: []byte("select 1\r\n")}}
	got, err := readMigrations(fsys, "m")
	if err != nil {
		t.Fatalf("readMigrations() error = %v", err)
	}
	if got[0].sum != checksum([]byte("select 1\r\n")) {
		t.Errorf("sum = %s, want the checksum of the raw bytes", got[0].sum)
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()

	files := []migration{
		{version: 1, name: "0001_a.sql", sum: "s1"},
		{version: 2, name: "0002_b.sql", sum: "s2"},
		{version: 3, name: "0003_c.sql", sum: "s3"},
	}
	cases := []struct {
		name        string
		rows        []record
		wantFill    []int
		wantChanged []int
		wantNewer   []int
	}{
		{name: "nothing recorded"},
		{name: "all equal", rows: []record{{version: 1, sum: "s1"}, {version: 2, sum: "s2"}}},
		{name: "legacy row", rows: []record{{version: 1, noSum: true}, {version: 2, sum: "s2"}}, wantFill: []int{1}},
		{name: "changed", rows: []record{{version: 1, sum: "s1"}, {version: 2, sum: "old"}}, wantChanged: []int{2}},
		{name: "empty string is a change, not a fill", rows: []record{{version: 1, sum: ""}}, wantChanged: []int{1}},
		{name: "newer than the code", rows: []record{{version: 1, sum: "s1"}, {version: 4, sum: "s4"}}, wantNewer: []int{4}},
		{
			name: "all three", rows: []record{{version: 1, noSum: true}, {version: 2, sum: "old"}, {version: 5, noSum: true}},
			wantFill: []int{1}, wantChanged: []int{2}, wantNewer: []int{5},
		},
	}
	versions := func(ms []migration) []int {
		var out []int
		for _, m := range ms {
			out = append(out, m.version)
		}
		return out
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compare(files, tc.rows)
			if !slices.Equal(versions(got.fill), tc.wantFill) {
				t.Errorf("fill = %v, want %v", versions(got.fill), tc.wantFill)
			}
			if !slices.Equal(versions(got.changed), tc.wantChanged) {
				t.Errorf("changed = %v, want %v", versions(got.changed), tc.wantChanged)
			}
			if !slices.Equal(got.newer, tc.wantNewer) {
				t.Errorf("newer = %v, want %v", got.newer, tc.wantNewer)
			}
			if wantErr := len(tc.wantChanged) > 0; (got.err() != nil) != wantErr {
				t.Errorf("err() = %v, want an error: %v", got.err(), wantErr)
			}
		})
	}
}

// The error names the file and the version, wraps the sentinel and carries
// no content and no checksum.
func TestComparisonErrorNamesTheFileOnly(t *testing.T) {
	t.Parallel()

	const secret = "fake-secret-in-file"
	files := []migration{{version: 1, name: "0001_a.sql", sql: "select '" + secret + "'", sum: "deadbeefsum"}}
	err := compare(files, []record{{version: 1, name: "0001_a.sql", sum: "oldbeefsum"}}).err()
	if !errors.Is(err, ErrChangedMigration) {
		t.Fatalf("err = %v, want ErrChangedMigration", err)
	}
	msg := err.Error()
	for _, want := range []string{"0001_a.sql", "version 1", "database: "} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	for _, bad := range []string{secret, "deadbeefsum", "oldbeefsum", "select"} {
		if strings.Contains(msg, bad) {
			t.Errorf("error %q carries %q", msg, bad)
		}
	}
}

// fakeLedger is the recorded rows of a database.
type fakeLedger struct {
	rows []record
	err  error
}

func (l fakeLedger) records(context.Context) ([]record, error) { return l.rows, l.err }

// Check after a migration: the edit of an applied file makes readiness fail,
// also under the option. A database ahead of the code does not: an old pod
// stays ready during a rolling deploy and after a rollback.
func TestCheckComparesTheRecordedRows(t *testing.T) {
	t.Parallel()

	files := []migration{{version: 1, name: "0001_a.sql", sum: "s1"}, {version: 2, name: "0002_b.sql", sum: "s2"}}
	cases := []struct {
		name    string
		rows    []record
		allow   bool
		ledger  error
		wantErr error
		wantAny bool
	}{
		{name: "equal", rows: []record{{version: 1, sum: "s1"}, {version: 2, sum: "s2"}}},
		{name: "legacy row is not a failure", rows: []record{{version: 1, noSum: true}}},
		{name: "changed", rows: []record{{version: 1, sum: "old"}}, wantErr: ErrChangedMigration},
		{name: "empty checksum", rows: []record{{version: 1, sum: ""}}, wantErr: ErrChangedMigration},
		{name: "changed with the option", rows: []record{{version: 1, sum: "old"}}, allow: true, wantErr: ErrChangedMigration},
		{name: "newer schema stays ready", rows: []record{{version: 1, sum: "s1"}, {version: 2, sum: "s2"}, {version: 3, name: "0003_c.sql", sum: "s3"}}},
		{name: "newer schema and a changed file", rows: []record{{version: 1, sum: "old"}, {version: 3, sum: "s3"}}, wantErr: ErrChangedMigration},
		{name: "ledger fails", ledger: errors.New("boom"), wantAny: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := &DB{pinger: fakePinger{}, ledger: fakeLedger{rows: tc.rows, err: tc.ledger}, allowChanged: tc.allow}
			db.files.Store(&files)
			db.migrated.Store(true)
			err := db.Check(t.Context())
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("Check() = %v, want %v", err, tc.wantErr)
			case tc.wantAny && err == nil:
				t.Error("Check() = nil, want the ledger error")
			case tc.wantErr == nil && !tc.wantAny && err != nil:
				t.Errorf("Check() = %v, want nil", err)
			}
		})
	}
}

// A changed file is no reason to wait and retry.
func TestRetryReturnsAtOnceOnAChangedMigration(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry(t.Context(), slog.New(slog.DiscardHandler), time.Hour, func(context.Context) error {
		calls++
		return errors.Join(errors.New("database: migration x"), ErrChangedMigration)
	})
	if !errors.Is(err, ErrChangedMigration) || calls != 1 {
		t.Errorf("retry() = %v after %d calls, want ErrChangedMigration after one", err, calls)
	}
}
