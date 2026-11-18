// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePassword is an obvious dummy with the characters a DSN would have to
// escape.
const fakePassword = "fake-p@ss:w/ord%?#&="

// writeRootCert writes a throwaway CA certificate and returns its path.
func writeRootCert(t *testing.T) string {
	t.Helper()
	ca := newCA(t, "govue test root")
	return writePEM(t, ca.certPEM)
}

func writePEM(t *testing.T, pem []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, pem, 0o600); err != nil {
		t.Fatalf("write root certificate: %v", err)
	}
	return path
}

func envOf(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoadConfigReadsEveryVariable(t *testing.T) {
	t.Parallel()

	root := writeRootCert(t)
	cfg, err := LoadConfig(envOf(map[string]string{
		"DB_HOST":            "db.example.invalid",
		"DB_PORT":            "6432",
		"DB_NAME":            "app",
		"DB_USER":            "app",
		"DB_PASSWORD":        fakePassword,
		"DB_SSLMODE":         "verify-ca",
		"DB_SSLROOTCERT":     root,
		"DB_MAX_CONNS":       "7",
		"DB_CONNECT_TIMEOUT": "3s",
	}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	want := Config{
		Host: "db.example.invalid", Port: 6432, Name: "app", User: "app", Password: fakePassword,
		SSLMode: SSLVerifyCA, SSLRootCert: root, MaxConns: 7, ConnectTimeout: 3 * time.Second,
	}
	if cfg != want {
		t.Errorf("LoadConfig() = %s, want %s", cfg, want)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()

	root := writeRootCert(t)
	cfg, err := LoadConfig(envOf(map[string]string{
		"DB_HOST": "db.example.invalid", "DB_NAME": "app", "DB_USER": "app",
		"DB_PASSWORD": fakePassword, "DB_SSLROOTCERT": root,
	}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.SSLMode != SSLVerifyFull {
		t.Errorf("default sslmode = %q, want verify-full", cfg.SSLMode)
	}
	if cfg.Port != 5432 || cfg.MaxConns != 10 || cfg.ConnectTimeout != 5*time.Second {
		t.Errorf("defaults = port %d, max conns %d, timeout %s, want 5432, 10, 5s", cfg.Port, cfg.MaxConns, cfg.ConnectTimeout)
	}
}

// The default is verify-full, so a deployment that sets nothing about TLS
// does not start without a root certificate. It never falls back to a weaker
// mode on its own.
func TestLoadConfigDefaultNeedsARootCertificate(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(envOf(map[string]string{
		"DB_HOST": "db.example.invalid", "DB_NAME": "app", "DB_USER": "app", "DB_PASSWORD": fakePassword,
	}))
	if err == nil || !strings.Contains(err.Error(), "sslmode verify-full needs a root certificate (DB_SSLROOTCERT)") {
		t.Fatalf("LoadConfig() error = %v, want the missing root certificate for verify-full", err)
	}
}

func TestLoadConfigReadsThePasswordFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(fakePassword+"\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	cfg, err := LoadConfig(envOf(map[string]string{
		"DB_HOST": "db.example.invalid", "DB_NAME": "app", "DB_USER": "app",
		"DB_PASSWORD_FILE": path, "DB_SSLMODE": "disable",
	}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Password != fakePassword {
		t.Errorf("password from DB_PASSWORD_FILE was not used")
	}
}

// Every bad value at once: one error that names each of them.
func TestLoadConfigAggregatesEveryProblem(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(envOf(map[string]string{
		"DB_PORT":            "70000",
		"DB_SSLMODE":         "prefer",
		"DB_MAX_CONNS":       "0",
		"DB_CONNECT_TIMEOUT": "2h",
	}))
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want every problem")
	}
	for _, want := range []string{
		"DB_HOST: is required",
		"DB_PORT: must be between 1 and 65535",
		"DB_NAME: is required",
		"DB_USER: is required",
		"DB_PASSWORD_FILE: is required",
		"DB_MAX_CONNS: must be between 1 and 100",
		"DB_CONNECT_TIMEOUT: must be between 1s and 1m0s",
		`sslmode (DB_SSLMODE) must be one of [disable require verify-ca verify-full], got "prefer"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
	if !strings.HasPrefix(err.Error(), "database: ") {
		t.Errorf("error %q does not name the component", err)
	}
}

func TestLoadConfigRefusesBothPasswordSources(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(envOf(map[string]string{
		"DB_HOST": "h", "DB_NAME": "n", "DB_USER": "u", "DB_SSLMODE": "disable",
		"DB_PASSWORD": fakePassword, "DB_PASSWORD_FILE": "/nonexistent/password",
	}))
	if err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("LoadConfig() error = %v, want both sources refused", err)
	}
	if strings.Contains(err.Error(), fakePassword) {
		t.Errorf("error leaks the password: %v", err)
	}
}

// The TLS rules, on Validate and therefore on Open and LoadConfig alike.
func TestValidateTLS(t *testing.T) {
	t.Parallel()

	root := writeRootCert(t)
	notPEM := writePEM(t, []byte("not a certificate"))
	base := Config{Host: "h", Name: "n", User: "u", Password: fakePassword}

	cases := []struct {
		name    string
		mode    SSLMode
		root    string
		wantErr string
	}{
		{"verify-full with a root", SSLVerifyFull, root, ""},
		{"verify-ca with a root", SSLVerifyCA, root, ""},
		{"default with a root", "", root, ""},
		{"require alone", SSLRequire, "", ""},
		{"disable alone", SSLDisable, "", ""},
		{"verify-full without a root", SSLVerifyFull, "", "sslmode verify-full needs a root certificate"},
		{"verify-ca without a root", SSLVerifyCA, "", "sslmode verify-ca needs a root certificate"},
		{"default without a root", "", "", "sslmode verify-full needs a root certificate"},
		{"require with a root", SSLRequire, root, "sslmode require ignores a root certificate"},
		{"disable with a root", SSLDisable, root, "sslmode disable ignores a root certificate"},
		{"prefer", "prefer", "", `got "prefer"`},
		{"allow", "allow", "", `got "allow"`},
		{"unknown", "verify_full", root, `got "verify_full"`},
		{"upper case", "VERIFY-FULL", root, `got "VERIFY-FULL"`},
		{"missing root file", SSLVerifyFull, filepath.Join(t.TempDir(), "missing.crt"), "root certificate (DB_SSLROOTCERT)"},
		{"root file without a certificate", SSLVerifyFull, notPEM, "holds no PEM certificate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			cfg.SSLMode = tc.mode
			cfg.SSLRootCert = tc.root
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// Validate is what a Config written in code goes through. Every bad field is
// in the one error, and a zero field takes the secure default.
func TestValidateAggregatesEveryProblem(t *testing.T) {
	t.Parallel()

	err := Config{Port: -1, MaxConns: 101, ConnectTimeout: time.Millisecond, SSLMode: "prefer"}.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil")
	}
	for _, want := range []string{
		"host (DB_HOST) is required",
		"port (DB_PORT) must be between 1 and 65535, got -1",
		"name (DB_NAME) is required",
		"user (DB_USER) is required",
		"password (DB_PASSWORD or DB_PASSWORD_FILE) is required",
		"max conns (DB_MAX_CONNS) must be between 1 and 100, got 101",
		"connect timeout (DB_CONNECT_TIMEOUT) must be between 1s and 1m0s, got 1ms",
		`got "prefer"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}

	bounds := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"port 65535", Config{Port: 65535}, true},
		{"port 65536", Config{Port: 65536}, false},
		{"max conns 1", Config{MaxConns: 1}, true},
		{"max conns 100", Config{MaxConns: 100}, true},
		{"max conns negative", Config{MaxConns: -1}, false},
		{"timeout 1s", Config{ConnectTimeout: time.Second}, true},
		{"timeout 1m", Config{ConnectTimeout: time.Minute}, true},
		{"timeout 61s", Config{ConnectTimeout: 61 * time.Second}, false},
	}
	for _, b := range bounds {
		cfg := b.cfg
		cfg.Host, cfg.Name, cfg.User, cfg.Password, cfg.SSLMode = "h", "n", "u", fakePassword, SSLDisable
		if err := cfg.Validate(); (err == nil) != b.ok {
			t.Errorf("%s: Validate() error = %v, want ok %v", b.name, err, b.ok)
		}
	}
}

// outer is a product's own config with the database inside, by value and by
// pointer.
type outer struct {
	Addr string
	DB   Config
	Ptr  *Config
}

func TestConfigNeverPrintsItsPassword(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Host: "db.example.invalid", Port: 5432, Name: "app", User: "app", Password: fakePassword,
		SSLMode: SSLVerifyFull, SSLRootCert: "/etc/db/ca.crt", MaxConns: 10, ConnectTimeout: 5 * time.Second,
	}
	whole := outer{Addr: ":8080", DB: cfg, Ptr: &cfg}

	render := map[string]func() string{}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		render["fmt "+verb+" value"] = func() string { return fmt.Sprintf(verb, cfg) }
		render["fmt "+verb+" pointer"] = func() string { return fmt.Sprintf(verb, &cfg) }
		render["fmt "+verb+" struct"] = func() string { return fmt.Sprintf(verb, whole) }
	}
	render["Sprint"] = func() string { return fmt.Sprint(cfg, &cfg, whole) }
	render["Println"] = func() string { return fmt.Sprintln(cfg, &cfg, whole) }
	render["error wrap"] = func() string { return fmt.Errorf("open %v: %w", cfg, os.ErrNotExist).Error() }
	render["json value"] = func() string { b, _ := json.Marshal(cfg); return string(b) }
	render["json pointer"] = func() string { b, _ := json.Marshal(&cfg); return string(b) }
	render["json struct"] = func() string { b, _ := json.Marshal(whole); return string(b) }
	render["text"] = func() string { b, _ := cfg.MarshalText(); return string(b) }
	for name, newHandler := range map[string]func(*bytes.Buffer) slog.Handler{
		"slog text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"slog json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		render[name+" value"] = func() string {
			var b bytes.Buffer
			slog.New(newHandler(&b)).Info("start", "db", cfg)
			return b.String()
		}
		render[name+" pointer"] = func() string {
			var b bytes.Buffer
			slog.New(newHandler(&b)).Info("start", "db", &cfg)
			return b.String()
		}
		render[name+" struct"] = func() string {
			var b bytes.Buffer
			slog.New(newHandler(&b)).Info("start", "config", whole)
			return b.String()
		}
		render[name+" any"] = func() string {
			var b bytes.Buffer
			slog.New(newHandler(&b)).Info("start", slog.Any("config", whole), slog.Any("db", cfg))
			return b.String()
		}
	}

	for name, fn := range render {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := fn()
			// The password's distinctive part, also escaped or hex encoded.
			for _, leak := range []string{"fake-p@ss", "w/ord", "fake-p\\u0040ss", fmt.Sprintf("%x", "fake-p@ss")} {
				if strings.Contains(got, leak) {
					t.Fatalf("output leaks the password (%q):\n%s", leak, got)
				}
			}
			if !strings.Contains(got, "db.example.invalid") && !strings.Contains(got, fmt.Sprintf("%x", "db.example.invalid")) {
				t.Errorf("output lost the host, so the test proves nothing:\n%s", got)
			}
		})
	}
}

// An empty password prints as empty, so a log line shows that it is missing.
func TestConfigShowsAMissingPassword(t *testing.T) {
	t.Parallel()

	if got := (Config{Host: "h"}).String(); !strings.Contains(got, "password= ") {
		t.Errorf("String() = %q, want an empty password shown as empty", got)
	}
	if got := (Config{Host: "h", Password: "x"}).String(); !strings.Contains(got, "password=[redacted]") {
		t.Errorf("String() = %q, want the password shown as redacted", got)
	}
}
