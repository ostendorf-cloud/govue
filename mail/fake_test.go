// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

const testHost = "mail.test"

// testCert is a self signed certificate for testHost and the pool trusting it.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: testHost},
		DNSNames:              []string{testHost},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// fakeServer is a minimal in process SMTP server behind net.Pipe: no socket,
// no network. Set the fields before the first dial.
type fakeServer struct {
	cert  tls.Certificate
	pool  *x509.CertPool
	tlsFn func(*tls.Config) // adjusts the server TLS config

	implicit    bool   // TLS from the first byte
	noStartTLS  bool   // do not offer STARTTLS
	startTLSErr string // reply to STARTTLS instead of 220
	auth        string // mechanisms offered, after TLS
	authEarly   bool   // offer AUTH before TLS too
	authReply   string // reply to a finished AUTH, default 235
	rcptReply   string // reply to RCPT, default 250
	endReply    string // reply after the data, default 250
	silent      bool   // never greet, read until closed
	hangAtData  bool   // read the data, then never answer
	dropAtEnd   bool   // close the connection instead of answering the data

	mu         sync.Mutex
	wg         sync.WaitGroup
	dials      int
	commands   []string // "tls:EHLO x" or "plain:EHLO x"
	plain      []string // every line read before TLS
	dataCount  int
	data       string   // the data with the dot stuffing undone, LF line ends
	rawLines   []string // the data lines as they came on the wire
	authSecret string   // what an AUTH carried, decoded
}

func newFake(t *testing.T) *fakeServer {
	t.Helper()
	cert, pool := testCert(t)
	f := &fakeServer{cert: cert, pool: pool, auth: "PLAIN LOGIN"}
	t.Cleanup(f.wg.Wait)
	return f
}

func (f *fakeServer) config() Config {
	return Config{
		Host:        testHost,
		Port:        587,
		Timeout:     5 * time.Second,
		RootCAs:     f.pool,
		DialContext: f.dial,
	}
}

func (f *fakeServer) dial(_ context.Context, _, _ string) (net.Conn, error) {
	f.mu.Lock()
	f.dials++
	f.mu.Unlock()
	client, server := net.Pipe()
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer func() { _ = server.Close() }()
		f.serve(server)
	}()
	return client, nil
}

func (f *fakeServer) tlsConfig() *tls.Config {
	c := &tls.Config{Certificates: []tls.Certificate{f.cert}}
	if f.tlsFn != nil {
		f.tlsFn(c)
	}
	return c
}

func (f *fakeServer) record(secure bool, line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if secure {
		f.commands = append(f.commands, "tls:"+line)
		return
	}
	f.commands = append(f.commands, "plain:"+line)
	f.plain = append(f.plain, line)
}

func (f *fakeServer) serve(raw net.Conn) {
	if f.silent {
		_, _ = io.Copy(io.Discard, raw)
		return
	}
	conn := raw
	secure := false
	if f.implicit {
		tc := tls.Server(raw, f.tlsConfig())
		if tc.HandshakeContext(context.Background()) != nil {
			// A pipe has no buffer: read the client's alert so it does not
			// block on writing it.
			_, _ = io.Copy(io.Discard, raw)
			return
		}
		conn, secure = tc, true
	}
	r := textproto.NewReader(bufio.NewReader(conn))
	say := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	say("220 " + testHost + " ready")
	for {
		line, err := r.ReadLine()
		if err != nil {
			return
		}
		f.record(secure, line)
		verb, _, _ := strings.Cut(strings.ToUpper(line), " ")
		switch verb {
		case "EHLO", "HELO":
			say("250-" + testHost)
			if !secure && !f.noStartTLS {
				say("250-STARTTLS")
			}
			if f.auth != "" && (secure || f.authEarly) {
				say("250-AUTH " + f.auth)
			}
			say("250 8BITMIME")
		case "STARTTLS":
			if f.startTLSErr != "" {
				say(f.startTLSErr)
				continue
			}
			say("220 go ahead")
			tc := tls.Server(conn, f.tlsConfig())
			if tc.HandshakeContext(context.Background()) != nil {
				_, _ = io.Copy(io.Discard, raw)
				return
			}
			conn, secure = tc, true
			r = textproto.NewReader(bufio.NewReader(conn))
			say = func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
		case "AUTH":
			f.handleAuth(line, r, say)
		case "MAIL", "RSET":
			say("250 ok")
		case "RCPT":
			if f.rcptReply != "" {
				say(f.rcptReply)
				continue
			}
			say("250 ok")
		case "DATA":
			say("354 go")
			// Read the lines as they came, so a test sees the dot stuffing,
			// and undo it the way a server does.
			var raw, body []string
			for {
				line, err := r.ReadLine()
				if err != nil {
					return
				}
				if line == "." {
					break
				}
				raw = append(raw, line)
				body = append(body, strings.TrimPrefix(line, "."))
			}
			f.mu.Lock()
			f.dataCount++
			f.rawLines = raw
			f.data = strings.Join(body, "\n") + "\n"
			f.mu.Unlock()
			switch {
			case f.hangAtData:
				_, _ = io.Copy(io.Discard, conn)
				return
			case f.dropAtEnd:
				return
			case f.endReply != "":
				say(f.endReply)
			default:
				say("250 queued")
			}
		case "QUIT":
			say("221 bye")
			return
		default:
			say("500 unknown")
		}
	}
}

func (f *fakeServer) handleAuth(line string, r *textproto.Reader, say func(string)) {
	fields := strings.Fields(line)
	decode := func(s string) string {
		b, _ := base64.StdEncoding.DecodeString(s)
		return string(b)
	}
	var secret string
	switch strings.ToUpper(fields[1]) {
	case "PLAIN":
		if len(fields) > 2 {
			secret = decode(fields[2])
		}
	case "LOGIN":
		say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
		user, _ := r.ReadLine()
		say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
		pass, _ := r.ReadLine()
		secret = decode(user) + "\x00" + decode(pass)
	}
	f.mu.Lock()
	f.authSecret = secret
	f.mu.Unlock()
	if f.authReply != "" {
		say(f.authReply)
		return
	}
	say("235 ok")
}

// countCommands counts the commands with this verb.
func (f *fakeServer) countCommands(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.commands {
		_, line, _ := strings.Cut(c, ":")
		if strings.HasPrefix(strings.ToUpper(line), verb) {
			n++
		}
	}
	return n
}

// sawCommand reports whether a command with this verb arrived at all.
func (f *fakeServer) sawCommand(verb string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		_, line, _ := strings.Cut(c, ":")
		if strings.HasPrefix(strings.ToUpper(line), verb) {
			return true
		}
	}
	return false
}
