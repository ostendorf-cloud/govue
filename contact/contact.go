// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package contact validates a contact form and serves its two stateless
// HTTP handlers: a signed proof of work challenge and the submission that
// sends one plain text mail.
//
// The form is stateless on purpose. There is no session, no cookie and no CSRF
// token. A submission is protected by the origin check, a strict JSON content
// type, a signed single use challenge from the pow package, a per client
// rate limit and a global mail ceiling. Mail is built and sent through the
// mail package: From and To are fixed by the site, the visitor's address is
// only in Reply-To, and the mail holds no client address and no user agent.
//
// Every answer carries a stable code and never a message catalogue. A field
// error is a code under details, as {field: code}. No log line holds a name,
// an address or message text, in any path.
//
// A submission can carry the state of the form's consent box. With
// Config.RequireConsent a missing or false box is a field error. The state is
// what the client claims, never a proof, and govue writes no consent text
// into the mail: a site's Body adds its own line from Request.Consent and
// Request.Received if it wants a record.
//
// The labels of the default mail body (Name, Email, Message) follow the
// optional Request.Language, the page language the client sends as a hint:
// English and German are built in, see [LabelsFor], and Config.Labels
// replaces them. An unknown or malformed language gives English, never an
// error, and the value never enters the mail.
//
// The limits and the replay set are per process. With several replicas the
// budgets multiply. This package imports no session package.
//
// Privacy facts for a site's own privacy text. Nothing is persisted by this
// package. In process memory it holds the limiter keys, which are the IPv4
// address or the IPv6 /64 of a client (up to 100000 keys, kept until the
// bucket refills), and the replay set of the proof of work. The visitor's
// name, address and message leave the process only through the mail. The site
// has to say so in its privacy text. This is a statement of fact, not legal
// advice.
package contact

import (
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	govuemail "github.com/ostendorf-cloud/govue/mail"
)

// The length limits, in runes and not in bytes.
const (
	MaxNameRunes    = 200
	MaxEmailRunes   = 254
	MaxMessageRunes = 5000
)

// The stable field codes of [Validate].
const (
	CodeRequired      = "required"
	CodeTooLong       = "too_long"
	CodeInvalidChars  = "invalid_chars"
	CodeNonASCIIEmail = "non_ascii_email"
	CodeInvalid       = "invalid"
)

// The field names used in [Validate] and in the JSON body. FieldConsent is
// reported by [Handler.Submit] only, with [Config.RequireConsent].
const (
	FieldName    = "name"
	FieldEmail   = "email"
	FieldMessage = "message"
	FieldConsent = "consent"
)

// Request is what the visitor wrote, plus the time the server received it.
type Request struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`

	// Consent is the state of the form's consent box as the client sent it.
	// A missing key decodes as false. It records a claim of the client, not
	// a proof: a modified client sends true without showing a box. A site
	// that wants a trail puts it into the mail through Config.Body, together
	// with Received. GovueContactForm sends it when it shows the box.
	Consent bool `json:"consent"`

	// Language is the language of the page the visitor wrote on, a hint that
	// picks the labels of [DefaultBody]. It is optional: a missing key is the
	// empty string, and [Normalize] turns a value that is not a language tag
	// shape (ASCII letters and hyphens, at most [MaxLanguageLen] bytes) into
	// the empty string, never into an error. An unknown or empty language
	// selects the English labels. The value is never written into the mail.
	Language string `json:"language"`

	// Received is the server's clock (Config.Now) when Submit decoded the
	// request, without a monotonic reading. It never comes from the client:
	// a body that carries it is refused as an unknown field. It is the zero
	// time outside Submit.
	Received time.Time `json:"-"`
}

// Normalize trims white space around every text field and turns CRLF and CR
// in the message into LF. A Language that is not a language tag shape becomes
// empty and otherwise is lower cased. It changes nothing else: a control
// character stays and is refused by [Validate], never repaired, and Consent
// and Received are kept as they are.
func Normalize(r Request) Request {
	r.Language = cleanLanguage(r.Language)
	msg := strings.ReplaceAll(r.Message, "\r\n", "\n")
	msg = strings.ReplaceAll(msg, "\r", "\n")
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.TrimSpace(r.Email)
	r.Message = strings.TrimSpace(msg)
	return r
}

// Validate returns a code per field that fails, or nil when the request is
// valid. Call [Normalize] first. A value never appears in the result. It
// checks the three text fields only: whether consent is required is the
// handler's option, [Config.RequireConsent].
func Validate(r Request) map[string]string {
	errs := map[string]string{}
	if code := checkName(r.Name); code != "" {
		errs[FieldName] = code
	}
	if code := checkEmail(r.Email); code != "" {
		errs[FieldEmail] = code
	}
	if code := checkMessage(r.Message); code != "" {
		errs[FieldMessage] = code
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func checkName(s string) string {
	switch {
	case empty(s):
		return CodeRequired
	case utf8.RuneCountInString(s) > MaxNameRunes:
		return CodeTooLong
	case badChars(s, false):
		return CodeInvalidChars
	}
	return ""
}

func checkMessage(s string) string {
	switch {
	case empty(s):
		return CodeRequired
	case utf8.RuneCountInString(s) > MaxMessageRunes:
		return CodeTooLong
	case badChars(s, true):
		return CodeInvalidChars
	}
	return ""
}

func checkEmail(s string) string {
	switch {
	case empty(s):
		return CodeRequired
	case utf8.RuneCountInString(s) > MaxEmailRunes:
		return CodeTooLong
	case badChars(s, false):
		return CodeInvalidChars
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			return CodeNonASCIIEmail
		}
	}
	// One plain mailbox, as the mail package accepts it for Reply-To. The
	// check runs the builder itself, so an address that passes here cannot
	// be refused at send time.
	parsed, err := mail.ParseAddress(s)
	if err != nil || parsed.Name != "" || parsed.Address != s {
		return CodeInvalid
	}
	_, err = govuemail.Build(govuemail.Message{
		From: "a@example.org", To: []string{"a@example.org"}, ReplyTo: s, Subject: "x", Body: "x",
	})
	if err != nil {
		return CodeInvalid
	}
	return ""
}

// empty reports a value with nothing visible in it: no rune, or only white
// space, format characters, the zero width joiners, combining marks and
// variation selectors with no base rune, and the filler characters that render
// as blank (Hangul fillers, the blank braille pattern, the Khmer inherent
// vowels and the combining grapheme joiner). Such a value counts as
// missing, so a field cannot pass with a blank.
func empty(s string) bool {
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
		// A combining mark or a variation selector needs a base rune. With
		// none in the value, nothing is drawn.
		case unicode.Is(unicode.Mn, r), r >= '︀' && r <= '️', r >= 0xe0100 && r <= 0xe01ef:
		case r == 'ᅟ', r == 'ᅠ', r == 'ㅤ', r == 'ﾠ', r == '⠀',
			r == '឴', r == '឵', r == '͏':
		default:
			return false
		}
	}
	return true
}

// badChars reports invalid UTF-8, a control character, a line or paragraph
// separator and a Unicode format character, except the zero width joiners
// that some scripts and emoji need. A multi line text keeps LF and tab.
func badChars(s string, multiline bool) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
		if r == '‌' || r == '‍' {
			continue
		}
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return true
		}
	}
	return false
}
