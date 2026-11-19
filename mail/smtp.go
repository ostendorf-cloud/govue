// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Sender sends one message. Consumers depend on this interface and fake it in
// their own tests.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTP submits messages to one server. It is safe for concurrent use and
// holds no connection between calls.
type SMTP struct {
	cfg Config
}

var _ Sender = (*SMTP)(nil)

// NewSMTP validates cfg and returns a sender for it.
func NewSMTP(cfg Config) (*SMTP, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &SMTP{cfg: cfg.withDefaults()}, nil
}

// Send builds m and submits it in one conversation. It never retries: a
// failure after the server may have taken the message would send it twice,
// so whether to try again is the caller's decision. A message refused by the
// builder never reaches the network.
//
// Credentials leave only inside TLS, and a server that does not offer
// STARTTLS is refused before anything else is sent, unless Security is
// Plain. The context and Config.Timeout end the conversation, whichever
// comes first.
func (s *SMTP) Send(ctx context.Context, m Message) (err error) {
	b, err := build(m, time.Now(), rand.Reader)
	if err != nil {
		return err
	}
	cfg := s.cfg
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, RootCAs: cfg.RootCAs}

	// One deadline covers the dial and the whole conversation, so a Send never
	// takes twice the timeout and a custom dialer is bound too.
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	defer func() {
		// A deadline or cancel ends a pending read as an i/o timeout, which
		// would hide why the conversation ended. This runs before cancel.
		if err != nil && ctx.Err() != nil {
			err = fmt.Errorf("%w (%w)", err, context.Cause(ctx))
		}
	}()

	dial := cfg.DialContext
	if dial == nil {
		d := &net.Dialer{}
		dial = d.DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return s.wrap("dial", err)
	}
	// Only the context ends the connection: no deadline is set on it, so the
	// error never races between an i/o timeout and the context error. The
	// closure holds the raw connection, which TLS wraps later.
	raw := conn
	stop := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	secure := false
	if cfg.Security == ImplicitTLS {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return s.wrap("tls", err)
		}
		conn, secure = tc, true
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return s.wrap("greeting", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello(cfg.HeloName); err != nil {
		return s.wrap("ehlo", err)
	}
	if cfg.Security == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrNoStartTLS
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return s.wrap("starttls", err)
		}
		secure = true
	}
	if cfg.Username != "" {
		if !secure {
			// Validate refuses this already. The check stays here so no
			// later change can send a password in the clear.
			return s.wrap("auth", errors.New("refusing to authenticate without TLS"))
		}
		auth, err := chooseAuth(c, cfg.Username, cfg.Password.Reveal())
		if err != nil {
			return err
		}
		if err := c.Auth(auth); err != nil {
			return s.wrap("auth", err)
		}
	}
	if err := c.Mail(b.from); err != nil {
		return s.wrap("mail", err)
	}
	for _, r := range b.rcpts {
		if err := c.Rcpt(r); err != nil {
			return s.wrap("rcpt", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return s.wrap("data", err)
	}
	if _, err := w.Write(b.data); err != nil {
		return s.wrap("data", err)
	}
	if err := w.Close(); err != nil {
		return s.wrap("data", err)
	}
	// The server took the message with the reply to the end of data. A
	// failing QUIT changes nothing about that.
	_ = c.Quit()
	return nil
}

// chooseAuth picks PLAIN, then LOGIN, from what the server advertised.
func chooseAuth(c *smtp.Client, user, pass string) (smtp.Auth, error) {
	_, params := c.Extension("AUTH")
	mechs := strings.Fields(strings.ToUpper(params))
	switch {
	case slices.Contains(mechs, "PLAIN"):
		return plainAuth{user, pass}, nil
	case slices.Contains(mechs, "LOGIN"):
		return &loginAuth{user: user, pass: pass}, nil
	}
	return nil, ErrNoAuth
}

// plainAuth is AUTH PLAIN that refuses a connection without TLS, also on
// localhost, which net/smtp's own PlainAuth exempts.
type plainAuth struct{ user, pass string }

// Start implements smtp.Auth.
func (a plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("AUTH PLAIN without TLS")
	}
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

// Next implements smtp.Auth.
func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

// loginAuth is AUTH LOGIN, which answers two prompts: user, then password.
type loginAuth struct {
	user, pass string
	step       int
}

// Start implements smtp.Auth.
func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("AUTH LOGIN without TLS")
	}
	return "LOGIN", nil, nil
}

// Next implements smtp.Auth.
func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.user), nil
	case 2:
		return []byte(a.pass), nil
	default:
		return nil, errors.New("unexpected server challenge")
	}
}
