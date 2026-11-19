// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte(secretPassword+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		values   map[string]string
		wantErr  string
		port     int
		security Security
		user     string
		password string
	}{
		{name: "defaults", values: map[string]string{"SMTP_HOST": "mail.example.org"}, port: 587, security: StartTLS},
		{name: "direct password", values: map[string]string{
			"SMTP_HOST": "mail.example.org", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p",
		}, port: 587, user: "u", password: "p"},
		{name: "password file", values: map[string]string{
			"SMTP_HOST": "mail.example.org", "SMTP_USERNAME": "u", "SMTP_PASSWORD_FILE": file,
		}, port: 587, user: "u", password: secretPassword},
		{name: "port 465 is implicit tls", values: map[string]string{
			"SMTP_HOST": "mail.example.org", "SMTP_PORT": "465",
		}, port: 465, security: ImplicitTLS},
		{name: "custom port", values: map[string]string{
			"SMTP_HOST": "mail.example.org", "SMTP_PORT": "2525",
		}, port: 2525, security: StartTLS},
		{name: "missing host", values: map[string]string{}, wantErr: "SMTP_HOST"},
		{name: "bad port", values: map[string]string{"SMTP_HOST": "h", "SMTP_PORT": "70000"}, wantErr: "SMTP_PORT"},
		{name: "both password and file", values: map[string]string{
			"SMTP_HOST": "h", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p", "SMTP_PASSWORD_FILE": file,
		}, wantErr: "SMTP_PASSWORD_FILE"},
		{name: "missing password file", values: map[string]string{
			"SMTP_HOST": "h", "SMTP_USERNAME": "u", "SMTP_PASSWORD_FILE": filepath.Join(t.TempDir(), "none"),
		}, wantErr: "SMTP_PASSWORD"},
		{name: "username without password", values: map[string]string{
			"SMTP_HOST": "h", "SMTP_USERNAME": "u",
		}, wantErr: "together"},
		{name: "password without username", values: map[string]string{
			"SMTP_HOST": "h", "SMTP_PASSWORD": "p",
		}, wantErr: "together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := LoadConfig(env(tc.values))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Port != tc.port || cfg.Security != tc.security || cfg.Username != tc.user ||
				cfg.Password.Reveal() != tc.password {
				t.Errorf("cfg = %+v", cfg)
			}
		})
	}
}

func TestLoadConfigNeverReturnsPlain(t *testing.T) {
	t.Parallel()

	for _, port := range []string{"25", "587", "465", "1025"} {
		cfg, err := LoadConfig(env(map[string]string{"SMTP_HOST": "h", "SMTP_PORT": port}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Security == Plain {
			t.Errorf("port %s gave plain", port)
		}
	}
}

func TestLoadConfigErrorHasNoPassword(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(env(map[string]string{"SMTP_PASSWORD": secretPassword, "SMTP_PORT": "x"}))
	if err == nil || strings.Contains(err.Error(), secretPassword) {
		t.Errorf("err = %v", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"minimal", Config{Host: "h"}, true},
		{"no host", Config{}, false},
		{"host with slash", Config{Host: "h/x"}, false},
		{"port out of range", Config{Host: "h", Port: 70000}, false},
		{"timeout too short", Config{Host: "h", Timeout: time.Millisecond}, false},
		{"timeout too long", Config{Host: "h", Timeout: time.Hour}, false},
		{"unknown security", Config{Host: "h", Security: Security(9)}, false},
		{"NUL in password", Config{Host: "h", Username: "u", Password: NewSecret("a\x00b")}, false},
		{"CRLF in username", Config{Host: "h", Username: "u\r\n", Password: NewSecret("p")}, false},
		{"bad helo", Config{Host: "h", HeloName: "a b"}, false},
	} {
		if err := tc.cfg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}
