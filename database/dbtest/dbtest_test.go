// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/database"
)

// Without a URL: a loud skip locally, a failure in CI, nothing when it is set.
func TestMissing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		raw, ci  string
		wantSkip bool
		wantMsg  string
	}{
		{"set", "postgres://u:p@h/db?sslmode=disable", "", false, ""},
		{"set in CI", "postgres://u:p@h/db?sslmode=disable", "true", false, ""},
		{"missing locally", "", "", true, "SKIPPED, NOT RUN: " + EnvURL + " is not set"},
		{"missing in CI", "", "true", false, EnvURL + " is not set in CI"},
		{"missing in CI, upper case", "", "TRUE", false, EnvURL + " is not set in CI"},
		{"missing in CI, one", "", "1", false, EnvURL + " is not set in CI"},
		{"missing, CI false", "", "false", true, "SKIPPED, NOT RUN"},
		{"missing, CI zero", "", "0", true, "SKIPPED, NOT RUN"},
		{"missing, CI other", "", "yes please", true, "SKIPPED, NOT RUN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			skip, msg := missing(tc.raw, tc.ci)
			if skip != tc.wantSkip {
				t.Errorf("skip = %v, want %v", skip, tc.wantSkip)
			}
			if tc.wantMsg == "" {
				if msg != "" {
					t.Errorf("msg = %q, want none", msg)
				}
				return
			}
			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("msg = %q, want one containing %q", msg, tc.wantMsg)
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	const fake = "fake-p@ss:w/rd"
	cfg, err := parse("postgres://govue:fake-p%40ss%3Aw%2Frd@[::1]:6543/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	want := database.Config{
		Host: "::1", Port: 6543, Name: "postgres", User: "govue",
		SSLMode: database.SSLDisable, MaxConns: 4, ConnectTimeout: 5 * time.Second,
	}
	if cfg.Password.Reveal() != fake {
		t.Error("parse() lost the password")
	}
	// Config holds a Secret, which == cannot compare: the printed form
	// carries every other field.
	cfg.Password = database.Secret{}
	if cfg.String() != want.String() {
		t.Errorf("parse() = %s, want %s", cfg, want)
	}

	bad := []struct {
		name, raw, want string
	}{
		{"not a URL", "host=h user=u", "must be a postgres:// URL"},
		{"other scheme", "mysql://u:fake@h/db?sslmode=disable", "must be a postgres:// URL"},
		{"no user", "postgres://h/db?sslmode=disable", "must be a postgres:// URL"},
		{"no password", "postgres://u@h/db?sslmode=disable", "has no password"},
		{"bad port", "postgres://u:fake@h:x/db?sslmode=disable", "must be a postgres:// URL"},
		{"no sslmode", "postgres://u:fake@h/db", "names no sslmode"},
		{"prefer", "postgres://u:fake@h/db?sslmode=prefer", `got "prefer"`},
		{"verify-full without a root", "postgres://u:fake@h/db?sslmode=verify-full", "needs a root certificate"},
	}
	for _, b := range bad {
		_, err := parse(b.raw)
		if err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: parse() error = %v, want one containing %q", b.name, err, b.want)
			continue
		}
		if strings.Contains(err.Error(), "fake") {
			t.Errorf("%s: parse() error quotes the URL: %v", b.name, err)
		}
	}
}

// Without a server, and outside CI, the helper skips, and Config never
// returns.
func TestConfigSkipsWithoutAServer(t *testing.T) {
	t.Setenv(EnvURL, "")
	t.Setenv("CI", "")
	var reached bool
	t.Run("inner", func(t *testing.T) {
		_ = Config(t)
		reached = true
	})
	if reached {
		t.Error("Config returned without a server")
	}
}
