// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDSNCarriesNoPasswordAndEscapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Config
		want []string
	}{
		{
			"plain",
			Config{Host: "db.example.invalid", Port: 5432, Name: "app", User: "app", Password: fakeSecret, SSLMode: SSLRequire},
			[]string{"postgres://app@db.example.invalid:5432/app?", "sslmode=require"},
		},
		{
			"ipv6 host",
			Config{Host: "::1", Port: 5432, Name: "app", User: "app", Password: fakeSecret, SSLMode: SSLDisable},
			[]string{"@[::1]:5432/"},
		},
		{
			"odd user and name",
			Config{Host: "h", Port: 5432, Name: "my db?x", User: "a@b:c/d", Password: fakeSecret, SSLMode: SSLDisable},
			[]string{"a%40b%3Ac%2Fd@h:5432/", "/my%20db%3Fx?"},
		},
		{
			"root certificate with a space",
			Config{Host: "h", Port: 5432, Name: "n", User: "u", Password: fakeSecret, SSLMode: SSLVerifyFull, SSLRootCert: "/etc/db ca/root.crt"},
			[]string{"sslmode=verify-full", "sslrootcert=%2Fetc%2Fdb+ca%2Froot.crt"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dsn(tc.cfg)
			if strings.Contains(got, "fake-p") || strings.Contains(got, url.QueryEscape(fakePassword)) {
				t.Fatalf("DSN contains the password: %s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("DSN %s is missing %s", got, want)
				}
			}
			// The DSN must parse back to the same parts.
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("DSN does not parse: %v", err)
			}
			if u.User.Username() != tc.cfg.User || u.Hostname() != tc.cfg.Host || strings.TrimPrefix(u.Path, "/") != tc.cfg.Name {
				t.Errorf("DSN parses to user %q host %q name %q", u.User.Username(), u.Hostname(), u.Path)
			}
		})
	}
}

// The parsed pgx config carries every setting, the password whole however odd
// its characters, and the user and database as given.
func TestPoolConfigCarriesTheSettings(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Host: "db.example.invalid", Port: 6432, Name: "my db", User: "a@b", Password: fakeSecret,
		SSLMode: SSLRequire, MaxConns: 7, ConnectTimeout: 3 * time.Second,
	}
	pc, err := poolConfig(cfg)
	if err != nil {
		t.Fatalf("poolConfig() error = %v", err)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"host", pc.ConnConfig.Host, "db.example.invalid"},
		{"port", pc.ConnConfig.Port, uint16(6432)},
		{"database", pc.ConnConfig.Database, "my db"},
		{"user", pc.ConnConfig.User, "a@b"},
		{"password", pc.ConnConfig.Password, fakePassword},
		{"connect timeout", pc.ConnConfig.ConnectTimeout, 3 * time.Second},
		{"max conns", pc.MaxConns, int32(7)},
		{"max conn lifetime", pc.MaxConnLifetime, maxConnLifetime},
		{"max conn idle time", pc.MaxConnIdleTime, maxConnIdleTime},
		{"health check period", pc.HealthCheckPeriod, healthCheckPeriod},
		{"fallbacks", len(pc.ConnConfig.Fallbacks), 0},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if strings.Contains(pc.ConnString(), "fake-p") {
		t.Errorf("the pool's connection string holds the password: %s", pc.ConnString())
	}
}

// A zero Config field takes the default in Open as well, verify-full
// included.
func TestPoolConfigFillsTheDefaults(t *testing.T) {
	t.Parallel()

	pc, err := poolConfig(Config{Host: "h", Name: "n", User: "u", Password: fakeSecret, SSLRootCert: writeRootCert(t)})
	if err != nil {
		t.Fatalf("poolConfig() error = %v", err)
	}
	if pc.ConnConfig.Port != DefaultPort || pc.MaxConns != DefaultMaxConns || pc.ConnConfig.ConnectTimeout != DefaultConnectTimeout {
		t.Errorf("defaults not applied: port %d, max conns %d, timeout %s", pc.ConnConfig.Port, pc.MaxConns, pc.ConnConfig.ConnectTimeout)
	}
	tc := pc.ConnConfig.TLSConfig
	if tc == nil || tc.InsecureSkipVerify || tc.ServerName != "h" || tc.RootCAs == nil {
		t.Errorf("default TLS is not verify-full: %+v", tc)
	}
}

func TestOpenRefusesAnInvalidConfigWithoutTheCause(t *testing.T) {
	t.Parallel()

	_, err := Open(t.Context(), Config{Host: "h", Name: "n", User: "u", Password: fakeSecret, SSLMode: "prefer"})
	if err == nil || !strings.Contains(err.Error(), `got "prefer"`) {
		t.Fatalf("Open() error = %v, want the invalid sslmode", err)
	}
	if strings.Contains(err.Error(), "fake-p") {
		t.Errorf("Open() error leaks the password: %v", err)
	}
}

// Open builds the pool without dialling: no server is needed and none is
// reached.
func TestOpenDoesNotConnect(t *testing.T) {
	t.Parallel()

	db, err := Open(t.Context(), Config{Host: "db.example.invalid", Name: "n", User: "u", Password: fakeSecret, SSLMode: SSLDisable})
	if err != nil {
		t.Fatalf("Open() error = %v, want nil: the pool connects lazily", err)
	}
	if db.Pool() == nil {
		t.Error("Pool() = nil")
	}
	db.Close()
}

// The environment pgx reads for unset settings never weakens what the
// Config names.
func TestPoolConfigIgnoresAWeakerEnvironment(t *testing.T) {
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGHOST", "other.example.invalid")
	t.Setenv("PGPASSWORD", "other-fake-password")

	pc, err := poolConfig(Config{Host: "h", Name: "n", User: "u", Password: fakeSecret, SSLRootCert: writeRootCert(t)})
	if err != nil {
		t.Fatalf("poolConfig() error = %v", err)
	}
	if pc.ConnConfig.TLSConfig == nil || pc.ConnConfig.TLSConfig.InsecureSkipVerify || len(pc.ConnConfig.Fallbacks) != 0 {
		t.Errorf("PGSSLMODE=disable weakened the TLS configuration: %+v, %d fallbacks", pc.ConnConfig.TLSConfig, len(pc.ConnConfig.Fallbacks))
	}
	if pc.ConnConfig.Host != "h" || pc.ConnConfig.Password != fakePassword {
		t.Errorf("the environment replaced the host or the password: host %q", pc.ConnConfig.Host)
	}
}

// ca is a throwaway certificate authority.
type ca struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newCA(t *testing.T, name string) ca {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return ca{cert: cert, key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// serverCert issues a server certificate for host.
func (c ca) serverCert(t *testing.T, host string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// handshake runs a TLS handshake between the client configuration pgx built
// and a server presenting cert, over net.Pipe: in memory, no socket and no
// network. It returns the client's error.
func handshake(t *testing.T, client *tls.Config, cert tls.Certificate) error {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	server := tls.Server(serverConn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The server's own error is the mirror of the client's refusal.
		_ = server.HandshakeContext(ctx)
		_ = serverConn.Close()
	}()
	queued := newQueuedConn(clientConn)
	err := tls.Client(queued, client.Clone()).HandshakeContext(ctx)
	_ = queued.Close()
	<-done
	return err
}

// queuedConn makes writes asynchronous. net.Pipe has no buffer, so a client
// that refuses the server's certificate would otherwise block writing its
// alert while the server blocks writing the rest of its flight.
type queuedConn struct {
	net.Conn
	queue chan []byte
}

func newQueuedConn(c net.Conn) *queuedConn {
	q := &queuedConn{Conn: c, queue: make(chan []byte, 64)}
	go func() {
		for b := range q.queue {
			if _, err := c.Write(b); err != nil {
				// The peer is gone. Drain the queue so no writer blocks.
				for range q.queue { //nolint:revive // draining on purpose
				}
				return
			}
		}
	}()
	return q
}

func (q *queuedConn) Write(b []byte) (int, error) {
	q.queue <- append([]byte(nil), b...)
	return len(b), nil
}

func (q *queuedConn) Close() error {
	close(q.queue)
	return q.Conn.Close()
}

// The TLS configuration pgx builds per sslmode, inspected field by field and
// then proved by a handshake against a throwaway CA: the right CA and host
// pass, a wrong CA fails in both verifying modes, a wrong host fails in
// verify-full only.
func TestTLSPerSSLMode(t *testing.T) {
	t.Parallel()

	const host = "db.cluster.invalid"
	trusted := newCA(t, "trusted root")
	other := newCA(t, "other root")
	root := writePEM(t, trusted.certPEM)

	good := trusted.serverCert(t, host)
	wrongHost := trusted.serverCert(t, "elsewhere.invalid")
	wrongCA := other.serverCert(t, host)

	cases := []struct {
		name          string
		mode          SSLMode
		root          string
		wantTLS       bool
		wantSkip      bool // InsecureSkipVerify
		wantVerifyFn  bool // pgx' own chain check for verify-ca
		wantRoots     bool
		wantServer    string
		goodOK        bool
		wrongHostOK   bool
		wrongCAOK     bool
		runHandshakes bool
		// platformVerifier marks the system pool, which macOS and Windows
		// check with their own verifier and its own error type.
		platformVerifier bool
	}{
		{name: "disable", mode: SSLDisable},
		{
			name: "require", mode: SSLRequire, wantTLS: true, wantSkip: true, wantServer: host,
			goodOK: true, wrongHostOK: true, wrongCAOK: true, runHandshakes: true,
		},
		{
			name: "verify-ca", mode: SSLVerifyCA, root: root, wantTLS: true, wantSkip: true, wantVerifyFn: true, wantRoots: true, wantServer: host,
			goodOK: true, wrongHostOK: true, wrongCAOK: false, runHandshakes: true,
		},
		{
			name: "verify-full", mode: SSLVerifyFull, root: root, wantTLS: true, wantRoots: true, wantServer: host,
			goodOK: true, wrongHostOK: false, wrongCAOK: false, runHandshakes: true,
		},
		{
			// The system roots do not hold the throwaway CA, so even the
			// right host is refused: the system pool is used, not nothing.
			name: "verify-full with the system roots", mode: SSLVerifyFull, root: SystemRoots, wantTLS: true, wantRoots: true, wantServer: host,
			goodOK: false, wrongHostOK: false, wrongCAOK: false, runHandshakes: true, platformVerifier: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pc, err := poolConfig(Config{Host: host, Name: "n", User: "u", Password: fakeSecret, SSLMode: tc.mode, SSLRootCert: tc.root})
			if err != nil {
				t.Fatalf("poolConfig() error = %v", err)
			}
			if len(pc.ConnConfig.Fallbacks) != 0 {
				t.Fatalf("%d fallbacks: a mode never falls back to another", len(pc.ConnConfig.Fallbacks))
			}
			tlsCfg := pc.ConnConfig.TLSConfig
			if !tc.wantTLS {
				if tlsCfg != nil {
					t.Fatalf("TLSConfig = %+v, want none for disable", tlsCfg)
				}
				return
			}
			if tlsCfg == nil {
				t.Fatal("TLSConfig = nil, want TLS")
			}
			if tlsCfg.InsecureSkipVerify != tc.wantSkip {
				t.Errorf("InsecureSkipVerify = %v, want %v", tlsCfg.InsecureSkipVerify, tc.wantSkip)
			}
			if (tlsCfg.VerifyPeerCertificate != nil) != tc.wantVerifyFn {
				t.Errorf("VerifyPeerCertificate set = %v, want %v", tlsCfg.VerifyPeerCertificate != nil, tc.wantVerifyFn)
			}
			if (tlsCfg.RootCAs != nil) != tc.wantRoots {
				t.Errorf("RootCAs set = %v, want %v", tlsCfg.RootCAs != nil, tc.wantRoots)
			}
			if tlsCfg.ServerName != tc.wantServer {
				t.Errorf("ServerName = %q, want %q", tlsCfg.ServerName, tc.wantServer)
			}
			if !tc.runHandshakes {
				return
			}
			for _, hs := range []struct {
				name string
				cert tls.Certificate
				ok   bool
			}{
				{"right CA and host", good, tc.goodOK},
				{"wrong host", wrongHost, tc.wrongHostOK},
				{"wrong CA", wrongCA, tc.wrongCAOK},
			} {
				err := handshake(t, tlsCfg, hs.cert)
				if (err == nil) != hs.ok {
					t.Errorf("%s: handshake error = %v, want ok %v", hs.name, err, hs.ok)
				}
				// A refusal is the certificate check, not a timeout or a
				// broken pipe.
				var unknownCA x509.UnknownAuthorityError
				var wrongName x509.HostnameError
				if err != nil && !tc.platformVerifier && !errors.As(err, &unknownCA) && !errors.As(err, &wrongName) {
					t.Errorf("%s: handshake error = %v, want a certificate verification error", hs.name, err)
				}
				if err != nil && tc.platformVerifier && !strings.Contains(err.Error(), "x509") {
					t.Errorf("%s: handshake error = %v, want a certificate verification error", hs.name, err)
				}
			}
		})
	}
}

// fakePinger answers Ping with err.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

// Readiness: unready while the database does not answer, unready until the
// schema is applied, ready after.
func TestCheck(t *testing.T) {
	t.Parallel()

	// The dial error of a refused connection, as the net package builds it.
	down := &net.OpError{
		Op: "dial", Net: "tcp",
		Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}
	cases := []struct {
		name     string
		ping     error
		migrated bool
		want     error
	}{
		{"unreachable", down, false, syscall.ECONNREFUSED},
		{"unreachable after migrate", down, true, syscall.ECONNREFUSED},
		{"reachable, not migrated", nil, false, ErrNotMigrated},
		{"reachable and migrated", nil, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := &DB{pinger: fakePinger{err: tc.ping}}
			db.migrated.Store(tc.migrated)
			err := db.Check(t.Context())
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Check() = %v, want %v", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "database: ") {
				t.Errorf("Check() error %q does not name the component", err)
			}
		})
	}
}
