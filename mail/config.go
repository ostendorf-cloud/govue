// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"

	"github.com/ostendorf-cloud/govue/envconf"
)

// The environment variables LoadConfig reads. The user name and the password
// also have a _FILE twin, SMTP_USERNAME_FILE and SMTP_PASSWORD_FILE.
const (
	// EnvHost is the submission server's host name.
	EnvHost = "SMTP_HOST"
	// EnvPort is its port, 587 when unset.
	EnvPort = "SMTP_PORT"
	// EnvUsername is the AUTH user, empty for a server without AUTH, or
	// SMTP_USERNAME_FILE names a file that holds it.
	EnvUsername = "SMTP_USERNAME"
	// EnvPassword is the AUTH password, or SMTP_PASSWORD_FILE names a file.
	EnvPassword = "SMTP_PASSWORD"
)

const (
	// DefaultPort is the submission port, with STARTTLS.
	DefaultPort = 587
	// ImplicitTLSPort is the port of SMTPS, where TLS starts at connect.
	ImplicitTLSPort = 465
	// DefaultTimeout bounds one whole Send, dial to QUIT.
	DefaultTimeout = 30 * time.Second
	// DefaultHeloName is sent in EHLO when Config.HeloName is empty.
	DefaultHeloName = "localhost"

	minTimeout = time.Second
	maxTimeout = 5 * time.Minute
)

// Security is how the connection is protected.
type Security int

const (
	// StartTLS connects in plain text and upgrades with STARTTLS before
	// anything else. A server that does not offer it is refused. It is the
	// default and the usual port 587.
	StartTLS Security = iota
	// ImplicitTLS starts TLS at connect, the usual port 465.
	ImplicitTLS
	// Plain never uses TLS and never authenticates. It is for a local test
	// sink, and Validate refuses it together with credentials.
	Plain
)

func (s Security) String() string {
	switch s {
	case StartTLS:
		return "starttls"
	case ImplicitTLS:
		return "implicit-tls"
	case Plain:
		return "plain"
	default:
		return fmt.Sprintf("security(%d)", int(s))
	}
}

// Config is one submission server. A zero Port, Timeout and HeloName take
// the defaults, so a Config written in code is as safe as a loaded one.
type Config struct {
	// Host is the server's name. The TLS certificate must name it.
	Host string
	// Port defaults to DefaultPort.
	Port int
	// Username and Password are the AUTH credentials, both or neither.
	Username string
	Password Secret
	// Security defaults to StartTLS. Plain is the consumer's explicit choice
	// for a local test sink.
	Security Security
	// Timeout bounds one whole Send. A context deadline can only shorten it.
	Timeout time.Duration
	// HeloName is the name sent in EHLO. It defaults to DefaultHeloName and
	// never to the machine's host name, which would tell the server which
	// pod sent the mail.
	HeloName string
	// RootCAs replaces the system roots, for a server with a private CA.
	RootCAs *x509.CertPool
	// DialContext replaces the TCP dial, for a proxy or a test.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// LoadConfig reads SMTP_HOST, SMTP_PORT, SMTP_USERNAME or SMTP_USERNAME_FILE
// and SMTP_PASSWORD or SMTP_PASSWORD_FILE, and returns every problem in one
// error. Port 465 means ImplicitTLS, every other port StartTLS. The
// environment never selects Plain: that is a line of code in the consumer.
//
// A _FILE variable names a file whose content is the value, with surrounding
// white space such as the trailing newline trimmed, the same rule as the
// direct variable. Setting a variable and its _FILE twin together, an empty
// file and a file that cannot be read are errors. An error names the
// variables and never a value or a path.
func LoadConfig(getenv func(string) string) (Config, error) {
	l := envconf.New(getenv)
	cfg := Config{
		Host: l.Required(EnvHost),
		Port: l.Int(EnvPort, DefaultPort, 1, 65535),
		// The user name is not a secret, but it is half of the credentials
		// and is mounted next to the password, so it reads the same way.
		Username: l.SecretOrFile(EnvUsername),
		Password: NewSecret(l.SecretOrFile(EnvPassword)),
	}
	if cfg.Port == ImplicitTLSPort {
		cfg.Security = ImplicitTLS
	}
	if err := errors.Join(l.Err(), cfg.Validate()); err != nil {
		return Config{}, fmt.Errorf("mail: load config: %w", err)
	}
	return cfg, nil
}

// withDefaults fills the zero fields.
func (c Config) withDefaults() Config {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.HeloName == "" {
		c.HeloName = DefaultHeloName
	}
	return c
}

// Validate checks the configuration. It names the field and never a value.
// Host is a bare host name or IPv4 address: a value with a colon, which
// includes an IPv6 literal, is refused.
func (c Config) Validate() error {
	c = c.withDefaults()
	var problems []error
	if c.Host == "" {
		problems = append(problems, errors.New("host is required"))
	} else if strings.ContainsAny(c.Host, ":/@?#[]\\") || strings.ContainsFunc(c.Host, unicode.IsSpace) {
		problems = append(problems, errors.New("host is not a host name"))
	}
	if c.Port < 1 || c.Port > 65535 {
		problems = append(problems, errors.New("port is out of range"))
	}
	if c.Timeout < minTimeout || c.Timeout > maxTimeout {
		problems = append(problems, errors.New("timeout is out of range"))
	}
	if c.Security < StartTLS || c.Security > Plain {
		problems = append(problems, errors.New("security is unknown"))
	}
	if (c.Username == "") != c.Password.IsZero() {
		problems = append(problems, errors.New("username and password are set together or not at all"))
	}
	if strings.ContainsAny(c.Username, "\x00\r\n") || strings.ContainsAny(c.Password.Reveal(), "\x00\r\n") {
		problems = append(problems, errors.New("credentials contain NUL, CR or LF"))
	}
	if c.Security == Plain && c.Username != "" {
		problems = append(problems, errors.New("plain security never carries credentials"))
	}
	if strings.ContainsAny(c.HeloName, " \t\r\n\x00") {
		problems = append(problems, errors.New("helo name is not a host name"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("mail: config: %w", err)
	}
	return nil
}
