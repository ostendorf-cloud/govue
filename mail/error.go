// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/textproto"
	"strings"
)

// ErrNoStartTLS means the server did not offer STARTTLS. The sender stops
// before any credential or message byte leaves.
var ErrNoStartTLS = errors.New("mail: server does not offer STARTTLS")

// ErrNoAuth means credentials are configured and the server offers neither
// AUTH PLAIN nor AUTH LOGIN.
var ErrNoAuth = errors.New("mail: server offers no usable AUTH mechanism")

// Error is a failed conversation with the server. Error() carries the stage
// and the reply code only: the reply text can echo an address from the
// message. Reply holds it for the operator's log, never for a client.
type Error struct {
	// Stage names the step: dial, tls, greeting, ehlo, starttls, auth,
	// mail, rcpt, data or send.
	Stage string
	// Code is the SMTP reply code, 0 when the failure was not a reply.
	Code int
	// Reply is the server's reply text, with the password removed.
	Reply string
	// Err is the cause when it was not a reply, with the same care.
	Err error
}

func (e *Error) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("mail: smtp %s: reply %d", e.Stage, e.Code)
	}
	if e.Err != nil {
		return fmt.Sprintf("mail: smtp %s: %v", e.Stage, e.Err)
	}
	return "mail: smtp " + e.Stage
}

// Unwrap returns the cause, nil for a reply.
func (e *Error) Unwrap() error { return e.Err }

// wrap turns an error of one stage into an *Error. A sentinel of this
// package passes through unchanged.
func (s *SMTP) wrap(stage string, err error) error {
	if err == nil || errors.Is(err, ErrNoStartTLS) || errors.Is(err, ErrNoAuth) {
		return err
	}
	var tp *textproto.Error
	if errors.As(err, &tp) {
		return &Error{Stage: stage, Code: tp.Code, Reply: s.scrub(tp.Msg)}
	}
	return &Error{Stage: stage, Err: scrubbed{msg: s.scrub(err.Error()), err: err}}
}

// scrub removes the password from text a server or a library produced. It
// also removes the base64 forms a server may echo: the AUTH PLAIN string and
// the password alone, with and without padding.
func (s *SMTP) scrub(text string) string {
	pw := s.cfg.Password.Reveal()
	if pw == "" {
		return text
	}
	plain := []byte("\x00" + s.cfg.Username + "\x00" + pw)
	for _, secret := range []string{
		base64.StdEncoding.EncodeToString(plain),
		base64.RawStdEncoding.EncodeToString(plain),
		base64.StdEncoding.EncodeToString([]byte(pw)),
		base64.RawStdEncoding.EncodeToString([]byte(pw)),
		pw,
	} {
		text = strings.ReplaceAll(text, secret, redacted)
	}
	return text
}

// scrubbed keeps the cause matchable with errors.Is while its text is clean.
type scrubbed struct {
	msg string
	err error
}

func (e scrubbed) Error() string { return e.msg }
func (e scrubbed) Unwrap() error { return e.err }
