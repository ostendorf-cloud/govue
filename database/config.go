// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ostendorf-cloud/govue/envconf"
)

// SSLMode is how the connection uses TLS. The names and the meaning are
// libpq's, so a value an operator knows from psql means the same here.
type SSLMode string

// The accepted modes. libpq's allow and prefer are refused: both fall back to
// plain text without telling anyone, which is never what a deployment wants.
// An application that really wants plain text says disable.
const (
	// SSLDisable sends everything in plain text. It is for a local
	// development database and for a test server, never for a deployment.
	SSLDisable SSLMode = "disable"
	// SSLRequire encrypts and does not verify the server's certificate. It
	// protects against a passive listener only.
	SSLRequire SSLMode = "require"
	// SSLVerifyCA encrypts and checks that SSLRootCert signed the server's
	// certificate, without checking the host name in it.
	SSLVerifyCA SSLMode = "verify-ca"
	// SSLVerifyFull encrypts, checks the chain against SSLRootCert and checks
	// that the certificate names Host. It is the default.
	SSLVerifyFull SSLMode = "verify-full"
)

// sslModes lists the accepted modes in the order an error message names them.
var sslModes = []SSLMode{SSLDisable, SSLRequire, SSLVerifyCA, SSLVerifyFull}

func (m SSLMode) valid() bool {
	for _, known := range sslModes {
		if m == known {
			return true
		}
	}
	return false
}

// verifies reports whether the mode checks the server's certificate against
// a root certificate.
func (m SSLMode) verifies() bool {
	return m == SSLVerifyCA || m == SSLVerifyFull
}

// Defaults and bounds. A zero field of a Config means the default, so a
// Config written in code gets the same secure choices as one loaded from the
// environment.
const (
	DefaultPort           = 5432
	DefaultSSLMode        = SSLVerifyFull
	DefaultMaxConns       = 10
	DefaultConnectTimeout = 5 * time.Second

	minMaxConns       = 1
	maxMaxConns       = 100
	minConnectTimeout = time.Second
	maxConnectTimeout = time.Minute
)

// The environment variables LoadConfig reads. The names carry no prefix: a
// product has one database, and the same names in every product make the
// charts and compose files alike.
const (
	EnvHost           = "DB_HOST"
	EnvPort           = "DB_PORT"
	EnvName           = "DB_NAME"
	EnvUser           = "DB_USER"
	EnvPassword       = "DB_PASSWORD" // and DB_PASSWORD_FILE
	EnvSSLMode        = "DB_SSLMODE"
	EnvSSLRootCert    = "DB_SSLROOTCERT"
	EnvMaxConns       = "DB_MAX_CONNS"
	EnvConnectTimeout = "DB_CONNECT_TIMEOUT"
)

// Config is the PostgreSQL connection. It is built from separate fields,
// never from one connection string, so the password stays a secret of its own
// with a _FILE twin and never travels inside a URL that gets logged.
//
// Every way of printing a Config leaves the password out: every fmt verb,
// encoding/json, encoding.TextMarshaler and log/slog, also when the Config
// sits inside a larger struct or behind a pointer.
type Config struct {
	Host string
	// Port is 1 to 65535. Zero means DefaultPort.
	Port     int
	Name     string
	User     string
	Password string
	// SSLMode is one of the SSLMode constants. Empty means DefaultSSLMode,
	// verify-full.
	SSLMode SSLMode
	// SSLRootCert is the path of a PEM file with the certificate authority
	// that signed the server's certificate, for example the CA of the
	// cluster's database operator. verify-ca and verify-full need it. disable
	// and require refuse it, because require with a root certificate silently
	// becomes verify-ca in libpq and pgx, and disable never reads it.
	SSLRootCert string
	// MaxConns caps the pool, 1 to 100. Several pods share one server, so
	// the cap is per pod. Zero means DefaultMaxConns.
	MaxConns int32
	// ConnectTimeout bounds one attempt to open a connection, TLS and
	// authentication included, 1s to 1m. Zero means DefaultConnectTimeout.
	ConnectTimeout time.Duration
}

// LoadConfig reads the connection from the environment through envconf and
// returns every problem in one error. A nil getenv reads the process
// environment.
//
// The password is DB_PASSWORD or the content of the file DB_PASSWORD_FILE
// names, never both. Its surrounding whitespace is trimmed, see
// envconf.Loader.SecretOrFile.
func LoadConfig(getenv func(string) string) (Config, error) {
	l := envconf.New(getenv)
	cfg := Config{
		Host:           l.Required(EnvHost),
		Port:           l.Int(EnvPort, DefaultPort, 1, 65535),
		Name:           l.Required(EnvName),
		User:           l.Required(EnvUser),
		Password:       l.RequiredSecretOrFile(EnvPassword),
		SSLMode:        SSLMode(l.String(EnvSSLMode, string(DefaultSSLMode))),
		SSLRootCert:    l.String(EnvSSLRootCert, ""),
		MaxConns:       int32(l.Int(EnvMaxConns, DefaultMaxConns, minMaxConns, maxMaxConns)),
		ConnectTimeout: l.Duration(EnvConnectTimeout, DefaultConnectTimeout, minConnectTimeout, maxConnectTimeout),
	}
	// envconf has checked presence and ranges already. What is left is what
	// envconf cannot know: the mode and the root certificate.
	problems := append([]error{l.Err()}, cfg.tlsProblems()...)
	if err := errors.Join(problems...); err != nil {
		return Config{}, fmt.Errorf("database: load config: %w", err)
	}
	return cfg, nil
}

// withDefaults fills every zero field that has a default.
func (c Config) withDefaults() Config {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.SSLMode == "" {
		c.SSLMode = DefaultSSLMode
	}
	if c.MaxConns == 0 {
		c.MaxConns = DefaultMaxConns
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = DefaultConnectTimeout
	}
	return c
}

// Validate checks a Config written in code, after filling the defaults, and
// returns every problem in one error. Open calls it. LoadConfig checks the
// same rules while it reads.
func (c Config) Validate() error {
	c = c.withDefaults()
	var problems []error
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}
	if c.Host == "" {
		add("host (%s) is required", EnvHost)
	}
	if c.Port < 1 || c.Port > 65535 {
		add("port (%s) must be between 1 and 65535, got %d", EnvPort, c.Port)
	}
	if c.Name == "" {
		add("name (%s) is required", EnvName)
	}
	if c.User == "" {
		add("user (%s) is required", EnvUser)
	}
	if c.Password == "" {
		add("password (%s or %s_FILE) is required", EnvPassword, EnvPassword)
	}
	if c.MaxConns < minMaxConns || c.MaxConns > maxMaxConns {
		add("max conns (%s) must be between %d and %d, got %d", EnvMaxConns, minMaxConns, maxMaxConns, c.MaxConns)
	}
	if c.ConnectTimeout < minConnectTimeout || c.ConnectTimeout > maxConnectTimeout {
		add("connect timeout (%s) must be between %s and %s, got %s", EnvConnectTimeout, minConnectTimeout, maxConnectTimeout, c.ConnectTimeout)
	}
	problems = append(problems, c.tlsProblems()...)
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("database: invalid config: %w", err)
	}
	return nil
}

// tlsProblems checks the mode and its root certificate. A root certificate
// is read here, so a wrong path or a file without a certificate fails at
// startup with every other problem, not on the first connection.
func (c Config) tlsProblems() []error {
	mode := c.SSLMode
	if mode == "" {
		mode = DefaultSSLMode
	}
	if !mode.valid() {
		return []error{fmt.Errorf("sslmode (%s) must be one of %v, got %q", EnvSSLMode, sslModes, string(mode))}
	}
	switch {
	case mode.verifies() && c.SSLRootCert == "":
		return []error{fmt.Errorf("sslmode %s needs a root certificate (%s)", mode, EnvSSLRootCert)}
	case !mode.verifies() && c.SSLRootCert != "":
		return []error{fmt.Errorf("sslmode %s ignores a root certificate (%s): use verify-full or remove it", mode, EnvSSLRootCert)}
	case c.SSLRootCert == "":
		return nil
	}
	pem, err := os.ReadFile(c.SSLRootCert)
	if err != nil {
		return []error{fmt.Errorf("root certificate (%s): %w", EnvSSLRootCert, err)}
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return []error{fmt.Errorf("root certificate (%s): %s holds no PEM certificate", EnvSSLRootCert, c.SSLRootCert)}
	}
	return nil
}

// redacted stands in for the password wherever a Config is printed.
const redacted = "[redacted]"

// String prints the connection without its password.
func (c Config) String() string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s sslrootcert=%s max_conns=%d connect_timeout=%s",
		c.Host, c.Port, c.Name, c.User, c.redactedPassword(), c.SSLMode, c.SSLRootCert, c.MaxConns, c.ConnectTimeout)
}

// redactedPassword shows whether a password is set, never what it is.
func (c Config) redactedPassword() string {
	if c.Password == "" {
		return ""
	}
	return redacted
}

// GoString keeps %#v from printing the password field.
func (c Config) GoString() string { return c.String() }

// Format makes every fmt verb print String, %+v and %#v included.
func (c Config) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, c.String())
}

// MarshalJSON keeps the password out of JSON. It matters when a whole product
// config is logged through a JSON handler: LogValue applies only when a
// Config is the attribute value itself, while a Config nested inside another
// value reaches encoding/json, which would print every field.
func (c Config) MarshalJSON() ([]byte, error) {
	// plain has Config's fields and none of its methods, so this does not
	// call MarshalJSON again.
	type plain Config
	p := plain(c)
	p.Password = c.redactedPassword()
	out, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("database: marshal config: %w", err)
	}
	return out, nil
}

// MarshalText keeps the password out of every other text encoding.
func (c Config) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

// LogValue keeps the password out of structured logs.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", c.Host),
		slog.Int("port", c.Port),
		slog.String("name", c.Name),
		slog.String("user", c.User),
		slog.String("password", c.redactedPassword()),
		slog.String("sslmode", string(c.SSLMode)),
		slog.String("sslrootcert", c.SSLRootCert),
		slog.Int("max_conns", int(c.MaxConns)),
		slog.Duration("connect_timeout", c.ConnectTimeout),
	)
}
