// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
	"unicode"

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
	// protects against a passive listener only. pgx, like libpq, turns it
	// into verify-ca when PGSSLROOTCERT is set or ~/.postgresql/root.crt
	// exists, which is stronger than asked and can refuse a server require
	// would accept.
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

// SystemRoots is the SSLRootCert value that verifies against the system's
// root certificates instead of a file, libpq's sslrootcert=system. It is
// accepted with verify-full only. pgx v5.13 switches any mode to
// verify-full when it sees sslrootcert=system, disable and require
// included, so another mode would not mean what it says. libpq 16 refuses
// the combination for the same reason.
const SystemRoots = "system"

// maxRootCertBytes bounds the root certificate file. A CA bundle is a few
// hundred kilobytes at most, and a path pointing at something else must not
// be read whole.
const maxRootCertBytes = 1 << 20

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
// encoding/json, encoding.TextMarshaler and log/slog, also inside a larger
// struct, behind a pointer, in an interface and in an unexported field. The
// last one holds because the password is a Secret, see there.
type Config struct {
	// Host is a host name or an IP address, an IPv6 address without
	// brackets. A unix socket path and a list of hosts are refused.
	Host string
	// Port is 1 to 65535. Zero means DefaultPort.
	Port     int
	Name     string
	User     string
	Password Secret
	// SSLMode is one of the SSLMode constants. Empty means DefaultSSLMode,
	// verify-full.
	SSLMode SSLMode
	// SSLRootCert is the path of a PEM file with the certificate authority
	// that signed the server's certificate, for example the CA of the
	// cluster's database operator, or SystemRoots for the system's roots
	// with verify-full. verify-ca and verify-full need it. disable and
	// require refuse it, because require with a root certificate silently
	// becomes verify-ca in libpq and pgx, and disable never reads it.
	SSLRootCert string
	// MaxConns caps the pool, 1 to 100. Several pods share one server, so
	// the cap is per pod. Zero means DefaultMaxConns.
	MaxConns int32
	// ConnectTimeout bounds one attempt to open a connection, TLS and
	// authentication included, 1s to 1m. Zero means DefaultConnectTimeout.
	ConnectTimeout time.Duration
	// AllowChangedMigrations lets the runner continue when an applied
	// migration file differs from the checksum recorded when it was applied.
	// The new checksum then replaces the stored one, so one run accepts the
	// edit, and MigrateUntilReady logs a warning (Migrate has no logger and
	// logs nothing).
	//
	// RISK: the schema no longer matches the file, and nothing checks it
	// again. The rewritten checksum also turns every running pod that still
	// holds the other version of the file unready (ErrChangedMigration) until
	// it is rolled over. Use it once, after a person has reviewed the edit,
	// for example a comment fix, then remove it. The default false refuses.
	// LoadConfig never sets it and no environment variable does: it is a
	// decision in code, so a typo in a manifest cannot weaken the check.
	//
	// The checksum guards against accidental edits. It is no tamper
	// protection against anyone with write access to the repository or the
	// database.
	AllowChangedMigrations bool
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
		Password:       NewSecret(l.RequiredSecretOrFile(EnvPassword)),
		SSLMode:        SSLMode(l.String(EnvSSLMode, string(DefaultSSLMode))),
		SSLRootCert:    l.String(EnvSSLRootCert, ""),
		MaxConns:       maxConnsFrom(l.Int(EnvMaxConns, DefaultMaxConns, minMaxConns, maxMaxConns)),
		ConnectTimeout: l.Duration(EnvConnectTimeout, DefaultConnectTimeout, minConnectTimeout, maxConnectTimeout),
	}
	// envconf has checked presence and ranges already. What is left is what
	// envconf cannot know: the shape of the host, the mode and the root
	// certificate.
	problems := append([]error{l.Err()}, cfg.hostProblems()...)
	problems = append(problems, cfg.tlsProblems()...)
	if err := errors.Join(problems...); err != nil {
		return Config{}, fmt.Errorf("database: load config: %w", err)
	}
	return cfg, nil
}

// maxConnsFrom narrows the pool size to int32 only after checking it against
// the bounds, so a value outside them can never wrap around, for example
// 4294967297 to 1. envconf.Loader.Int has checked the same bounds and
// recorded the problem, and returns the default for a bad value. This check
// keeps the conversion safe on its own, whatever the caller passed.
func maxConnsFrom(n int) int32 {
	if n < minMaxConns || n > maxMaxConns {
		return DefaultMaxConns
	}
	return int32(n)
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
	problems = append(problems, c.hostProblems()...)
	if c.Port < 1 || c.Port > 65535 {
		add("port (%s) must be between 1 and 65535, got %d", EnvPort, c.Port)
	}
	if c.Name == "" {
		add("name (%s) is required", EnvName)
	}
	if c.User == "" {
		add("user (%s) is required", EnvUser)
	}
	if c.Password.IsZero() {
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

// hostProblems refuses the host spellings pgx would read as something else
// or that fail only on the first connection: a path is a unix socket, where
// TLS is silently off even under verify-full, brackets fail only later when
// the pool is built, a comma makes a list of fallback hosts, and a colon
// outside an IPv6 address, whitespace or an @ would be looked up as a name
// that cannot exist.
//
// No message quotes the host. The spellings refused here are the ones a
// connection URL pasted into DB_HOST has, user:password@host among them, and
// a startup error reaches CI and startup logs.
func (c Config) hostProblems() []error {
	var problems []error
	add := func(rule string, args ...any) {
		problems = append(problems, fmt.Errorf("host (%s) "+rule+" (the value is not shown)", append([]any{EnvHost}, args...)...))
	}
	if strings.HasPrefix(c.Host, "/") {
		add("is a unix socket path, which is not supported: TLS would be off")
	}
	if strings.ContainsAny(c.Host, "[]") {
		add("must be an IPv6 address without brackets")
	}
	if strings.Contains(c.Host, ",") {
		add("is a list of hosts, which is not supported")
	}
	if strings.Contains(c.Host, ":") && !strings.ContainsAny(c.Host, "[]") && net.ParseIP(c.Host) == nil {
		add("holds a colon outside an IPv6 address: put the port into %s", EnvPort)
	}
	if strings.ContainsFunc(c.Host, unicode.IsSpace) {
		add("holds whitespace")
	}
	if strings.Contains(c.Host, "@") {
		add("holds an @: the user goes into %s and the password into %s or %s_FILE", EnvUser, EnvPassword, EnvPassword)
	}
	return problems
}

// tlsProblems checks the mode and its root certificate. A root certificate
// file is read here, so a wrong path or a file without a certificate fails
// at startup with every other problem, not on the first connection.
func (c Config) tlsProblems() []error {
	mode := c.SSLMode
	if mode == "" {
		mode = DefaultSSLMode
	}
	if !mode.valid() {
		return []error{fmt.Errorf("sslmode (%s) must be one of %v, got %q", EnvSSLMode, sslModes, string(mode))}
	}
	root := c.SSLRootCert
	switch {
	case mode.verifies() && root == "":
		return []error{fmt.Errorf("sslmode %s needs a root certificate (%s): the path of the CA's PEM file, or %q with verify-full for the system roots",
			mode, EnvSSLRootCert, SystemRoots)}
	case !mode.verifies() && root != "":
		return []error{fmt.Errorf("sslmode %s ignores a root certificate (%s): use verify-full or remove it", mode, EnvSSLRootCert)}
	case root == SystemRoots && mode != SSLVerifyFull:
		return []error{fmt.Errorf("root certificate (%s) %q needs sslmode verify-full, got %s", EnvSSLRootCert, SystemRoots, mode)}
	case root == "" || root == SystemRoots:
		return nil
	}
	pem, err := readBounded(root, maxRootCertBytes)
	if err != nil {
		return []error{fmt.Errorf("root certificate (%s): %w", EnvSSLRootCert, err)}
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return []error{fmt.Errorf("root certificate (%s): %s holds no PEM certificate", EnvSSLRootCert, root)}
	}
	return nil
}

// readBounded reads path, and refuses a file larger than limit bytes.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, nil
}

// String prints the connection without its password.
func (c Config) String() string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s sslrootcert=%s max_conns=%d connect_timeout=%s",
		c.Host, c.Port, c.Name, c.User, c.Password, c.SSLMode, c.SSLRootCert, c.MaxConns, c.ConnectTimeout)
}

// GoString keeps %#v from printing the password field.
func (c Config) GoString() string { return c.String() }

// Format makes every fmt verb print String, %+v and %#v included.
func (c Config) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, c.String())
}

// MarshalJSON writes the fields as an object, the password through Secret.
// Without it, MarshalText would turn a Config into one JSON string.
func (c Config) MarshalJSON() ([]byte, error) {
	// plain has Config's fields and none of its methods, so this does not
	// call MarshalJSON again.
	type plain Config
	out, err := json.Marshal(plain(c))
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
		slog.String("password", c.Password.String()),
		slog.String("sslmode", string(c.SSLMode)),
		slog.String("sslrootcert", c.SSLRootCert),
		slog.Int("max_conns", int(c.MaxConns)),
		slog.Duration("connect_timeout", c.ConnectTimeout),
		slog.Bool("allow_changed_migrations", c.AllowChangedMigrations),
	)
}
