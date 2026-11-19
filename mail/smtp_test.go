// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const secretPassword = "hunter2-correct-horse"

func credentials(cfg Config) Config {
	cfg.Username = "mailer"
	cfg.Password = NewSecret(secretPassword)
	return cfg
}

func newSender(t *testing.T, cfg Config) *SMTP {
	t.Helper()
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSendStartTLS(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	var s Sender = newSender(t, credentials(f.config()))
	if err := s.Send(t.Context(), validMessage()); err != nil {
		t.Fatal(err)
	}
	if f.dataCount != 1 {
		t.Fatalf("dataCount = %d, want 1", f.dataCount)
	}
	if !strings.Contains(f.data, "Subject: Hello") || !strings.Contains(f.data, "Body") {
		t.Errorf("data = %q", f.data)
	}
	if f.authSecret != "\x00mailer\x00"+secretPassword {
		t.Errorf("auth secret did not arrive intact")
	}
	// Everything before STARTTLS is plain text on the wire, and holds no
	// credential and no message.
	for _, line := range f.plain {
		if strings.Contains(line, secretPassword) || strings.HasPrefix(strings.ToUpper(line), "AUTH") ||
			strings.HasPrefix(strings.ToUpper(line), "MAIL") {
			t.Errorf("plain text line %q", line)
		}
	}
	want := []string{"plain:EHLO localhost", "plain:STARTTLS", "tls:EHLO localhost"}
	for i, w := range want {
		if f.commands[i] != w {
			t.Errorf("command %d = %q, want %q", i, f.commands[i], w)
		}
	}
}

func TestSendImplicitTLS(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.implicit = true
	cfg := credentials(f.config())
	cfg.Port, cfg.Security = ImplicitTLSPort, ImplicitTLS
	if err := newSender(t, cfg).Send(t.Context(), validMessage()); err != nil {
		t.Fatal(err)
	}
	if f.dataCount != 1 || f.sawCommand("STARTTLS") {
		t.Errorf("dataCount = %d, commands = %v", f.dataCount, f.commands)
	}
	for _, c := range f.commands {
		if strings.HasPrefix(c, "plain:") {
			t.Errorf("plain text command %q", c)
		}
	}
}

func TestSendImplicitTLSRefusesPlainServer(t *testing.T) {
	t.Parallel()

	f := newFake(t) // speaks plain text, the client expects TLS
	cfg := credentials(f.config())
	cfg.Security = ImplicitTLS
	err := newSender(t, cfg).Send(t.Context(), validMessage())
	if err == nil {
		t.Fatal("sent to a server without TLS")
	}
	if f.sawCommand("AUTH") || f.dataCount != 0 {
		t.Errorf("commands = %v", f.commands)
	}
}

func TestSendLogin(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.auth = "LOGIN"
	if err := newSender(t, credentials(f.config())).Send(t.Context(), validMessage()); err != nil {
		t.Fatal(err)
	}
	if f.authSecret != "mailer\x00"+secretPassword || f.dataCount != 1 {
		t.Errorf("dataCount = %d", f.dataCount)
	}
}

func TestSendWithoutCredentials(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	if err := newSender(t, f.config()).Send(t.Context(), validMessage()); err != nil {
		t.Fatal(err)
	}
	if f.sawCommand("AUTH") || f.dataCount != 1 {
		t.Errorf("commands = %v", f.commands)
	}
}

func TestSendPlainSink(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	cfg := f.config()
	cfg.Security = Plain
	if err := newSender(t, cfg).Send(t.Context(), validMessage()); err != nil {
		t.Fatal(err)
	}
	if f.sawCommand("STARTTLS") || f.dataCount != 1 {
		t.Errorf("commands = %v", f.commands)
	}
}

func TestPlainRefusesCredentials(t *testing.T) {
	t.Parallel()

	cfg := credentials(Config{Host: testHost, Security: Plain})
	if _, err := NewSMTP(cfg); err == nil {
		t.Fatal("plain security accepted credentials")
	}
}

// The server offers no STARTTLS: nothing but EHLO and QUIT may reach it.
func TestNoStartTLSRefused(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.noStartTLS = true
	f.authEarly = true
	err := newSender(t, credentials(f.config())).Send(t.Context(), validMessage())
	if !errors.Is(err, ErrNoStartTLS) {
		t.Fatalf("err = %v, want ErrNoStartTLS", err)
	}
	for _, verb := range []string{"AUTH", "MAIL", "RCPT", "DATA"} {
		if f.sawCommand(verb) {
			t.Errorf("server saw %s", verb)
		}
	}
	for _, line := range f.plain {
		if strings.Contains(line, secretPassword) {
			t.Error("password on the wire")
		}
	}
}

func TestStartTLSRefusedByServer(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.startTLSErr = "454 TLS not available"
	f.authEarly = true
	err := newSender(t, credentials(f.config())).Send(t.Context(), validMessage())
	var se *Error
	if !errors.As(err, &se) || se.Stage != "starttls" || se.Code != 454 {
		t.Fatalf("err = %v", err)
	}
	if f.sawCommand("AUTH") || f.sawCommand("MAIL") {
		t.Errorf("commands = %v", f.commands)
	}
}

// A server that offers AUTH before TLS gets no AUTH before TLS.
func TestAuthOnlyAfterTLS(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.authEarly = true
	if err := newSender(t, credentials(f.config())).Send(t.Context(), validMessage()); err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, c := range f.commands {
		if strings.HasPrefix(strings.ToUpper(strings.TrimPrefix(strings.TrimPrefix(c, "plain:"), "tls:")), "AUTH") {
			seen = true
			if !strings.HasPrefix(c, "tls:") {
				t.Errorf("AUTH outside TLS: %q", c)
			}
		}
	}
	if !seen {
		t.Error("no AUTH at all")
	}
}

func TestNoUsableAuth(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.auth = "CRAM-MD5 XOAUTH2"
	err := newSender(t, credentials(f.config())).Send(t.Context(), validMessage())
	if !errors.Is(err, ErrNoAuth) {
		t.Fatalf("err = %v, want ErrNoAuth", err)
	}
	if f.sawCommand("MAIL") {
		t.Error("mail sent without AUTH")
	}
}

func TestUntrustedCertificateRefused(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	cfg := credentials(f.config())
	cfg.RootCAs = nil // the system roots do not know the test certificate
	err := newSender(t, cfg).Send(t.Context(), validMessage())
	if err == nil {
		t.Fatal("trusted an unknown certificate")
	}
	if f.sawCommand("AUTH") || f.dataCount != 0 {
		t.Errorf("commands = %v", f.commands)
	}
}

func TestWrongHostRefused(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	cfg := credentials(f.config())
	cfg.Host = "other.test"
	if err := newSender(t, cfg).Send(t.Context(), validMessage()); err == nil {
		t.Fatal("accepted a certificate for another host")
	}
	if f.sawCommand("AUTH") {
		t.Error("AUTH reached a server with the wrong name")
	}
}

func TestOldTLSRefused(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.tlsFn = func(c *tls.Config) { c.MinVersion, c.MaxVersion = tls.VersionTLS10, tls.VersionTLS11 }
	if err := newSender(t, credentials(f.config())).Send(t.Context(), validMessage()); err == nil {
		t.Fatal("negotiated TLS below 1.2")
	}
	if f.sawCommand("AUTH") {
		t.Error("AUTH over old TLS")
	}
}

func TestContextDeadline(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.silent = true
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := newSender(t, f.config()).Send(ctx, validMessage())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestContextCancel(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.silent = true
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := newSender(t, f.config()).Send(ctx, validMessage())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestConfigTimeout(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.silent = true
	cfg := f.config()
	cfg.Timeout = time.Second
	start := time.Now()
	err := newSender(t, cfg).Send(t.Context(), validMessage())
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestContextCancelDuringData(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.hangAtData = true
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := newSender(t, f.config()).Send(ctx, validMessage()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if f.dataCount != 1 {
		t.Errorf("dataCount = %d", f.dataCount)
	}
}

// A failure after the data went out is never retried: the server may have
// the message already.
func TestNoRetry(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.dropAtEnd = true
	err := newSender(t, f.config()).Send(t.Context(), validMessage())
	var se *Error
	if !errors.As(err, &se) || se.Stage != "data" {
		t.Fatalf("err = %v", err)
	}
	if f.dataCount != 1 || f.dials != 1 {
		t.Errorf("dataCount = %d, dials = %d, want 1 and 1", f.dataCount, f.dials)
	}
}

func TestRejectedRecipientSendsNoData(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.rcptReply = "550 5.1.1 owner@example.org no such user"
	err := newSender(t, f.config()).Send(t.Context(), validMessage())
	var se *Error
	if !errors.As(err, &se) || se.Stage != "rcpt" || se.Code != 550 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(se.Reply, "no such user") {
		t.Errorf("Reply = %q", se.Reply)
	}
	// The reply text is for the log through Reply, and stays out of Error().
	if strings.Contains(err.Error(), "owner@example.org") {
		t.Errorf("Error() carries an address: %q", err)
	}
	if f.dataCount != 0 {
		t.Error("data sent after a refused recipient")
	}
}

func TestEndOfDataRejection(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.endReply = "554 5.7.1 rejected as spam"
	err := newSender(t, f.config()).Send(t.Context(), validMessage())
	var se *Error
	if !errors.As(err, &se) || se.Code != 554 || se.Stage != "data" {
		t.Fatalf("err = %v", err)
	}
}

func TestPasswordNeverInErrors(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.authReply = "535 5.7.8 bad credentials " + secretPassword
	cfg := credentials(f.config())
	err := newSender(t, cfg).Send(t.Context(), validMessage())
	var se *Error
	if !errors.As(err, &se) || se.Stage != "auth" || se.Code != 535 {
		t.Fatalf("err = %v", err)
	}
	for _, text := range []string{err.Error(), se.Reply, fmt.Sprintf("%v %+v %#v", err, err, se)} {
		if strings.Contains(text, secretPassword) {
			t.Errorf("password in %q", text)
		}
	}
	if !strings.Contains(se.Reply, "bad credentials") {
		t.Errorf("Reply = %q", se.Reply)
	}
}

func TestPasswordNeverInConfigOutput(t *testing.T) {
	t.Parallel()

	cfg := credentials(Config{Host: testHost})
	js, err := json.Marshal(struct {
		Username string
		Password Secret
	}{cfg.Username, cfg.Password})
	if err != nil {
		t.Fatal(err)
	}
	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("cfg", "password", cfg.Password, "config", cfg)
	for _, text := range []string{
		fmt.Sprintf("%v %+v %#v %s %q", cfg, cfg, cfg, cfg.Password, cfg.Password), string(js), logged.String(),
	} {
		if strings.Contains(text, secretPassword) {
			t.Errorf("password in %q", text)
		}
	}
	if !strings.Contains(string(js), "[redacted]") {
		t.Errorf("json = %s", js)
	}
}

// A refused message never opens a connection.
func TestRefusedMessageNeverDials(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	s := newSender(t, f.config())
	for _, m := range []Message{
		func() Message { m := validMessage(); m.Subject = "a\r\nBcc: x@example.net"; return m }(),
		func() Message { m := validMessage(); m.From = "x\ny@example.org"; return m }(),
		func() Message {
			m := validMessage()
			m.To = []string{"a@example.org\r\nRCPT TO:<b@example.org>"}
			return m
		}(),
		func() Message { m := validMessage(); m.ReplyTo = "a@example.org\r\n"; return m }(),
		func() Message { m := validMessage(); m.Body = strings.Repeat("x", MaxBodyBytes+1); return m }(),
	} {
		if err := s.Send(t.Context(), m); !errors.Is(err, ErrInvalid) {
			t.Errorf("err = %v, want ErrInvalid", err)
		}
	}
	if f.dials != 0 {
		t.Errorf("dials = %d, want 0", f.dials)
	}
}

func TestDialError(t *testing.T) {
	t.Parallel()

	cfg := Config{Host: testHost, DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("refused")
	}}
	err := newSender(t, cfg).Send(t.Context(), validMessage())
	var se *Error
	if !errors.As(err, &se) || se.Stage != "dial" {
		t.Fatalf("err = %v", err)
	}
}
