// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"fmt"
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

// userFixture is a user name that no error text contains by accident.
const userFixture = "smtp-user-7f3k9q"

// writeFile writes content into a new file of a test directory.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigUsernameFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"trailing newline", userFixture + "\n"},
		{"trailing CRLF", userFixture + "\r\n"},
		{"no newline", userFixture},
		{"surrounding white space", " \t" + userFixture + " \n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := LoadConfig(env(map[string]string{
				"SMTP_HOST":          "mail.example.org",
				"SMTP_USERNAME_FILE": writeFile(t, "username", tc.content),
				"SMTP_PASSWORD":      "p",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Username != userFixture {
				t.Errorf("Username = %q, want %q", cfg.Username, userFixture)
			}
		})
	}

	t.Run("file and password file together", func(t *testing.T) {
		t.Parallel()

		cfg, err := LoadConfig(env(map[string]string{
			"SMTP_HOST":          "mail.example.org",
			"SMTP_USERNAME_FILE": writeFile(t, "username", userFixture+"\n"),
			"SMTP_PASSWORD_FILE": writeFile(t, "password", secretPassword+"\n"),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Username != userFixture || cfg.Password.Reveal() != secretPassword {
			t.Errorf("Username = %q, password set = %v", cfg.Username, !cfg.Password.IsZero())
		}
	})

	t.Run("direct variable unchanged", func(t *testing.T) {
		t.Parallel()

		cfg, err := LoadConfig(env(map[string]string{
			"SMTP_HOST": "mail.example.org", "SMTP_USERNAME": " " + userFixture + " ", "SMTP_PASSWORD": "p",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Username != userFixture {
			t.Errorf("Username = %q, want %q", cfg.Username, userFixture)
		}
	})

	t.Run("neither variable means no AUTH", func(t *testing.T) {
		t.Parallel()

		cfg, err := LoadConfig(env(map[string]string{"SMTP_HOST": "mail.example.org"}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Username != "" || !cfg.Password.IsZero() {
			t.Errorf("Username = %q, password set = %v", cfg.Username, !cfg.Password.IsZero())
		}
	})
}

// errorTree is err and every error below it, through Unwrap() error and
// Unwrap() []error.
func errorTree(err error) []error {
	if err == nil {
		return nil
	}
	tree := []error{err}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		tree = append(tree, errorTree(single.Unwrap())...)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			tree = append(tree, errorTree(child)...)
		}
	}
	return tree
}

// assertHidden fails when any error of the tree, in any fmt verb, carries
// one of the hidden strings, also hex encoded.
func assertHidden(t *testing.T, err error, hidden ...string) {
	t.Helper()

	tree := errorTree(err)
	if len(tree) == 0 {
		t.Fatal("no error")
	}
	for _, node := range tree {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "% x", "%d"} {
			text := fmt.Sprintf(verb, node)
			for _, h := range hidden {
				for _, form := range []string{h, fmt.Sprintf("%x", h), fmt.Sprintf("%X", h), fmt.Sprintf("% x", h)} {
					if strings.Contains(text, form) {
						t.Errorf("%T in %s carries a hidden value: %s", node, verb, text)
					}
				}
			}
		}
	}
}

func TestLoadConfigUsernameFileRefusals(t *testing.T) {
	t.Parallel()

	unreadable := writeFile(t, "username-"+userFixture, userFixture+"\n")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(unreadable); err == nil && len(raw) > 0 {
		// Root reads a file without permission bits, so the case proves
		// nothing here. The missing file case still covers the read error.
		unreadable = ""
	}
	directory := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing-"+userFixture)

	for _, tc := range []struct {
		name   string
		values map[string]string
		want   []string
	}{
		{
			name: "both set",
			values: map[string]string{
				"SMTP_USERNAME":      userFixture,
				"SMTP_USERNAME_FILE": writeFile(t, "username-"+userFixture, userFixture+"\n"),
				"SMTP_PASSWORD":      secretPassword,
			},
			want: []string{"SMTP_USERNAME", "SMTP_USERNAME_FILE", "both set"},
		},
		{
			name: "empty file",
			values: map[string]string{
				"SMTP_USERNAME_FILE": writeFile(t, "username-"+userFixture, "\n"),
				"SMTP_PASSWORD":      secretPassword,
			},
			want: []string{"SMTP_USERNAME_FILE", "empty"},
		},
		{
			name: "missing file",
			values: map[string]string{
				"SMTP_USERNAME_FILE": missing,
				"SMTP_PASSWORD":      secretPassword,
			},
			want: []string{"SMTP_USERNAME_FILE", "cannot read"},
		},
		{
			name: "directory",
			values: map[string]string{
				"SMTP_USERNAME_FILE": directory,
				"SMTP_PASSWORD":      secretPassword,
			},
			want: []string{"SMTP_USERNAME_FILE", "cannot read"},
		},
		{
			name: "unreadable file",
			values: map[string]string{
				"SMTP_USERNAME_FILE": unreadable,
				"SMTP_PASSWORD":      secretPassword,
			},
			want: []string{"SMTP_USERNAME_FILE", "cannot read"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.values["SMTP_USERNAME_FILE"] == "" {
				t.Skip("running as root, a file without permission bits is readable")
			}
			tc.values["SMTP_HOST"] = "mail.example.org"
			cfg, err := LoadConfig(env(tc.values))
			if err == nil {
				t.Fatalf("LoadConfig() = %+v, want an error", cfg)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, does not name %q", err, want)
				}
			}
			assertHidden(t, err, userFixture, secretPassword, tc.values["SMTP_USERNAME_FILE"])
			if cfg.Username != "" || !cfg.Password.IsZero() {
				t.Error("a refused configuration is not the zero Config")
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
		{"host with colon", Config{Host: "h:25"}, false},
		{"host with space", Config{Host: "h x"}, false},
		{"host with unicode space", Config{Host: "h x"}, false},
		{"host with tab", Config{Host: "h\tx"}, false},
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
