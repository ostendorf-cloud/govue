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
		wantMissing []int
	}{
		{"nothing recorded", nil, nil, nil, nil},
		{"all equal", []record{{1, "0001_a.sql", "s1"}, {2, "0002_b.sql", "s2"}}, nil, nil, nil},
		{"legacy row", []record{{1, "0001_a.sql", ""}, {2, "0002_b.sql", "s2"}}, []int{1}, nil, nil},
		{"changed", []record{{1, "0001_a.sql", "s1"}, {2, "0002_b.sql", "old"}}, nil, []int{2}, nil},
		{"missing", []record{{1, "0001_a.sql", "s1"}, {4, "0004_d.sql", "s4"}}, nil, nil, []int{4}},
		{"all three", []record{{1, "0001_a.sql", ""}, {2, "0002_b.sql", "old"}, {5, "0005_e.sql", ""}}, []int{1}, []int{2}, []int{5}},
	}
	versions := func(ms []migration) []int {
		var out []int
		for _, m := range ms {
			out = append(out, m.version)
		}
		return out
	}
	same := slices.Equal[[]int]
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compare(files, tc.rows)
			if !same(versions(got.fill), tc.wantFill) {
				t.Errorf("fill = %v, want %v", versions(got.fill), tc.wantFill)
			}
			if !same(versions(got.changed), tc.wantChanged) {
				t.Errorf("changed = %v, want %v", versions(got.changed), tc.wantChanged)
			}
			if !same(got.missing, tc.wantMissing) {
				t.Errorf("missing = %v, want %v", got.missing, tc.wantMissing)
			}
			if wantErr := len(tc.wantChanged)+len(tc.wantMissing) > 0; (got.err() != nil) != wantErr {
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
	err := compare(files, []record{{1, "0001_a.sql", "oldbeefsum"}, {2, "0002_gone.sql", "x"}}).err()
	if !errors.Is(err, ErrChangedMigration) || !errors.Is(err, ErrMissingMigration) {
		t.Fatalf("err = %v, want both sentinels", err)
	}
	msg := err.Error()
	for _, want := range []string{"0001_a.sql", "version 1", "0002_gone.sql", "version 2", "database: "} {
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

// Check after a migration: the edit of an applied file and a version with no
// file make readiness fail, and the option accepts both.
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
		{name: "equal", rows: []record{{1, "0001_a.sql", "s1"}, {2, "0002_b.sql", "s2"}}},
		{name: "legacy row is not a failure", rows: []record{{1, "0001_a.sql", ""}}},
		{name: "changed", rows: []record{{1, "0001_a.sql", "old"}}, wantErr: ErrChangedMigration},
		{name: "missing", rows: []record{{3, "0003_c.sql", "s3"}}, wantErr: ErrMissingMigration},
		{name: "changed with the option", rows: []record{{1, "0001_a.sql", "old"}}, allow: true},
		{name: "missing with the option", rows: []record{{3, "0003_c.sql", "s3"}}, allow: true},
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

	for _, sentinel := range []error{ErrChangedMigration, ErrMissingMigration} {
		calls := 0
		err := retry(t.Context(), slog.New(slog.DiscardHandler), time.Hour, func(context.Context) error {
			calls++
			return errors.Join(errors.New("database: migration x"), sentinel)
		})
		if !errors.Is(err, sentinel) || calls != 1 {
			t.Errorf("retry() = %v after %d calls, want %v after one", err, calls, sentinel)
		}
	}
}
