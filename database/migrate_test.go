// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ostendorf-cloud/govue/health"
)

// The loader's rules, on fixture file systems.
func TestReadMigrations(t *testing.T) {
	t.Parallel()

	file := func(sql string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(sql)} }
	cases := []struct {
		name    string
		fsys    fstest.MapFS
		want    []string
		wantErr string
	}{
		{"in order", fstest.MapFS{"m/0002_b.sql": file("b"), "m/0001_a.sql": file("a")}, []string{"0001_a.sql", "0002_b.sql"}, ""},
		{"ten after nine", fstest.MapFS{
			"m/0001_a.sql": file("a"), "m/0002_a.sql": file("a"), "m/0003_a.sql": file("a"), "m/0004_a.sql": file("a"),
			"m/0005_a.sql": file("a"), "m/0006_a.sql": file("a"), "m/0007_a.sql": file("a"), "m/0008_a.sql": file("a"),
			"m/0009_a.sql": file("a"), "m/0010_a.sql": file("a"),
		}, []string{
			"0001_a.sql", "0002_a.sql", "0003_a.sql", "0004_a.sql", "0005_a.sql",
			"0006_a.sql", "0007_a.sql", "0008_a.sql", "0009_a.sql", "0010_a.sql",
		}, ""},
		{"ignores other files", fstest.MapFS{"m/0001_a.sql": file("a"), "m/README.md": file("x"), "m/sub/0002_b.sql": file("b")}, []string{"0001_a.sql"}, ""},
		{"gap", fstest.MapFS{"m/0001_a.sql": file("a"), "m/0003_c.sql": file("c")}, nil, "gap or a repeat at 0003_c.sql"},
		{"repeat", fstest.MapFS{"m/0001_a.sql": file("a"), "m/0001_b.sql": file("b")}, nil, "gap or a repeat at 0001_b.sql"},
		{"repeat in another spelling", fstest.MapFS{"m/0001_a.sql": file("a"), "m/1_b.sql": file("b")}, nil, "gap or a repeat"},
		{"starts at two", fstest.MapFS{"m/0002_a.sql": file("a")}, nil, "gap or a repeat at 0002_a.sql"},
		{"version zero", fstest.MapFS{"m/0000_a.sql": file("a")}, nil, "no version number"},
		{"negative version", fstest.MapFS{"m/-001_a.sql": file("a")}, nil, "no version number"},
		{"signed version", fstest.MapFS{"m/+001_a.sql": file("a")}, nil, "no version number"},
		{"not a number", fstest.MapFS{"m/abc_a.sql": file("a")}, nil, "no version number"},
		{"no underscore", fstest.MapFS{"m/0001.sql": file("a")}, nil, "not named <version>_<name>.sql"},
		{"no name", fstest.MapFS{"m/0001_.sql": file("a")}, nil, "not named <version>_<name>.sql"},
		{"missing directory", fstest.MapFS{"other/0001_a.sql": file("a")}, nil, "read migrations"},
		{"empty directory", fstest.MapFS{"m/README.md": file("x")}, nil, "no migration in m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := readMigrations(tc.fsys, "m")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("readMigrations() error = %v, want one containing %q", err, tc.wantErr)
				}
				if !strings.HasPrefix(err.Error(), "database: ") {
					t.Errorf("error %q does not name the component", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("readMigrations() error = %v", err)
			}
			var names []string
			for _, m := range got {
				names = append(names, m.name)
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("migrations = %v, want %v", names, tc.want)
			}
		})
	}
}

// A broken set of files fails Migrate and MigrateUntilReady at once, before
// the database is asked: waiting cannot fix a gap. The DB has no pool, so a
// query would panic.
func TestBrokenMigrationsFailBeforeTheDatabase(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"m/0002_a.sql": &fstest.MapFile{Data: []byte("select 1")}}
	db := &DB{}
	if err := db.Migrate(t.Context(), fsys, "m"); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Errorf("Migrate() error = %v, want the gap", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := db.MigrateUntilReady(ctx, slog.New(slog.DiscardHandler), fsys, "m")
	if err == nil || !strings.Contains(err.Error(), "gap") {
		t.Errorf("MigrateUntilReady() error = %v, want the gap at once", err)
	}
	if db.migrated.Load() {
		t.Error("the DB claims to be migrated")
	}
}

// lockedBuffer is a log sink the retry loop and the test share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The retry loop stops with its context, also while it waits between two
// attempts. The pause is an hour here: a loop that ignored the context during
// the pause would hold the test until its own timeout.
func TestRetryStopsWithItsContextDuringThePause(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	logs := &lockedBuffer{}
	var attempts atomic.Int32

	done := make(chan error, 1)
	go func() {
		done <- retry(ctx, slog.New(slog.NewTextHandler(logs, nil)), time.Hour, func(context.Context) error {
			attempts.Add(1)
			return errors.New("connection refused")
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("retry() error = %v, want the context's deadline", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retry() did not stop when its context ended during the pause")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 before the hour long pause", got)
	}
	if !strings.Contains(logs.String(), "retry_in=1h0m0s") {
		t.Errorf("the log does not show the configured pause:\n%s", logs.String())
	}
}

// The pause grows between attempts, and a success ends the loop.
func TestRetryRetriesUntilItSucceeds(t *testing.T) {
	t.Parallel()

	logs := &lockedBuffer{}
	var attempts atomic.Int32
	err := retry(t.Context(), slog.New(slog.NewTextHandler(logs, nil)), time.Millisecond, func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("an attempt has no deadline of its own")
		}
		if attempts.Add(1) < 4 {
			return errors.New("connection refused")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry() error = %v", err)
	}
	if got := attempts.Load(); got != 4 {
		t.Errorf("attempts = %d, want 4", got)
	}
	for _, want := range []string{"retry_in=1ms", "retry_in=2ms", "retry_in=4ms"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the log does not show %s, so the pause did not grow:\n%s", want, logs.String())
		}
	}
}

// An attempt that fails because the context ended reports the context, not
// a retry.
func TestRetryStopsWhenTheContextEndsDuringAnAttempt(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	err := retry(ctx, slog.New(slog.DiscardHandler), time.Hour, func(context.Context) error {
		cancel()
		return errors.New("canceled while connecting")
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("retry() error = %v, want context.Canceled", err)
	}
}

// The check plugs into health: readiness fails with the check's name and
// never its cause, liveness answers 200 whatever the database does.
func TestCheckPlugsIntoHealth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		ping      error
		migrated  bool
		wantReady int
	}{
		{"unreachable", errors.New("dial tcp: connection refused, password fake-p@ss"), false, http.StatusServiceUnavailable},
		{"not migrated", nil, false, http.StatusServiceUnavailable},
		{"ready", nil, true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := &DB{pinger: fakePinger{err: tc.ping}}
			db.migrated.Store(tc.migrated)
			registry := health.New()
			registry.Add("database", 0, db.Check)
			mux := http.NewServeMux()
			registry.Handle(mux)

			live := httptest.NewRecorder()
			mux.ServeHTTP(live, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
			if live.Code != http.StatusOK {
				t.Errorf("GET /healthz = %d, want 200: liveness never depends on the database", live.Code)
			}

			ready := httptest.NewRecorder()
			mux.ServeHTTP(ready, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
			if ready.Code != tc.wantReady {
				t.Errorf("GET /readyz = %d, want %d", ready.Code, tc.wantReady)
			}
			body := ready.Body.String()
			if tc.wantReady != http.StatusOK && !strings.Contains(body, `"database":"failed"`) {
				t.Errorf("GET /readyz body = %s, want the database check named", body)
			}
			if strings.Contains(body, "refused") || strings.Contains(body, "fake-p") || strings.Contains(body, "schema") {
				t.Errorf("GET /readyz body carries a cause: %s", body)
			}
		})
	}
}
