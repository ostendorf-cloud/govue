// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package optin

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ostendorf-cloud/govue/mail"
)

// The field names and codes of a refused request, under details as
// {field: code}.
const (
	FieldEmail   = "email"
	FieldConsent = "consent"

	CodeRequired      = "required"
	CodeTooLong       = "too_long"
	CodeInvalidChars  = "invalid_chars"
	CodeNonASCIIEmail = "non_ascii_email"
	CodeInvalid       = "invalid"
)

// MaxEmailRunes bounds an address.
const MaxEmailRunes = 254

// MaxLanguageLen is the longest language hint that is kept.
const MaxLanguageLen = 8

// checkAddress returns a field code, or "" for one plain ASCII mailbox that
// the mail package accepts as a recipient.
func checkAddress(s string) string {
	switch {
	case s == "":
		return CodeRequired
	case utf8.RuneCountInString(s) > MaxEmailRunes:
		return CodeTooLong
	case !utf8.ValidString(s):
		return CodeInvalidChars
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return CodeInvalidChars
		}
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			return CodeNonASCIIEmail
		}
	}
	parsed, err := netmail.ParseAddress(s)
	if err != nil || parsed.Name != "" || parsed.Address != s {
		return CodeInvalid
	}
	if _, err := mail.Build(mail.Message{From: "probe@example.org", To: []string{s}, Subject: "x", Body: "x"}); err != nil {
		return CodeInvalid
	}
	return ""
}

// cleanLanguage keeps a value with the shape of a language tag, lower cased,
// and turns anything else into "".
func cleanLanguage(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > MaxLanguageLen {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' {
			return ""
		}
	}
	return strings.ToLower(s)
}

// mailKind is what a mail is for.
type mailKind uint8

const (
	kindConfirmation mailKind = iota + 1
	kindMailing
)

// errKindNotAllowed refuses a mail that the state of the entry does not
// allow.
var errKindNotAllowed = errors.New("optin: mail kind not allowed for this entry")

// recipient is the only way to an address for [Handler.deliver]. Only
// newRecipient builds one, and it holds the rule: a confirmation goes to a
// pending entry only, a mailing to a confirmed entry only, nothing to a
// counter or a missing entry.
type recipient struct {
	key      Key
	address  string
	language string
	kind     mailKind
	created  time.Time
	consent  string
	at       time.Time
}

func newRecipient(e Entry, kind mailKind) (recipient, error) {
	switch {
	case kind == kindConfirmation && e.State == StatePending:
	case kind == kindMailing && e.State == StateConfirmed:
	default:
		return recipient{}, errKindNotAllowed
	}
	if e.Address == "" {
		return recipient{}, errKindNotAllowed
	}
	return recipient{
		key: e.Key, address: e.Address, language: e.Language, kind: kind,
		created: e.Created, consent: e.ConsentVersion, at: e.Confirmed,
	}, nil
}

// errMissingLink refuses a body without a link it must carry.
var errMissingLink = errors.New("optin: the mail body lacks a required link")

// checkContent refuses a body without every link in links.
func checkContent(c Content, links ...string) error {
	for _, l := range links {
		if !strings.Contains(c.Body, l) {
			return errMissingLink
		}
	}
	return nil
}

// deliver sends one mail to rc. Every mail of this package leaves here. It
// adds the unsubscribe headers of RFC 8058 and refuses a body without the
// unsubscribe link and the extra links.
func (h *Handler) deliver(ctx context.Context, rc recipient, c Content, links ...string) error {
	if rc.kind != kindConfirmation && rc.kind != kindMailing {
		return errKindNotAllowed
	}
	unsubscribe := h.keys.unsubscribeToken(rc.key)
	page := h.unsubscribeURL + "?token=" + unsubscribe
	if err := checkContent(c, append([]string{page}, links...)...); err != nil {
		return err
	}
	msg := mail.Message{
		From:    h.from,
		To:      []string{rc.address},
		Subject: c.Subject,
		Body:    c.Body,
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + h.oneClickURL + "?token=" + unsubscribe + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			"Auto-Submitted":        "auto-generated",
		},
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	var sendErr error
	if cause := guard("Sender.Send", func() { sendErr = h.sender.Send(ctx, msg) }); cause != nil {
		return cause
	}
	if sendErr != nil {
		return safeCause(sendErr)
	}
	return nil
}

// MailingResult counts the outcome of [Handler.MailConfirmed].
type MailingResult struct {
	// Sent is the number of mails handed to the sender.
	Sent int
	// Failed is the number of mails the sender or the content refused.
	Failed int
	// Skipped is the number of entries that were no longer confirmed when
	// their turn came, for example after an unsubscribe during the run.
	Skipped int
}

// MailConfirmed sends one mail built by content to every confirmed entry, one
// after another. Each entry is read again just before its mail, so an
// address that unsubscribed during the run gets nothing, and only a
// confirmed entry can receive it. A failed mail is counted and logged by its
// type and the run goes on. The run stops when ctx ends. The global mail
// ceiling of the form does not apply: the site starts a mailing on purpose.
// A run that ends early cannot tell who got the mail, so a second run sends
// it again to everyone.
func (h *Handler) MailConfirmed(ctx context.Context, content func(MailingData) Content) (MailingResult, error) {
	var res MailingResult
	if !h.configured {
		return res, ErrNotConfigured
	}
	if content == nil {
		return res, errors.New("optin: mailing content is required")
	}
	logger := h.logOrContext(ctx)
	err := h.store.EachConfirmed(ctx, func(listed Entry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := h.store.Get(ctx, listed.Key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("optin: store: %w", err)
		}
		rc, err := newRecipient(e, kindMailing)
		if err != nil {
			// An entry that is no longer confirmed is skipped, not an error.
			res.Skipped++
			return nil
		}
		data := MailingData{
			Address: rc.address, Language: rc.language, ConsentVersion: rc.consent, Confirmed: rc.at,
			UnsubscribeURL: h.unsubscribeURL + "?token=" + h.keys.unsubscribeToken(rc.key),
		}
		var c Content
		if cause := guard("mailing content", func() { c = content(data) }); cause != nil {
			res.Failed++
			logger.ErrorContext(ctx, "optin: mailing not sent", "error", cause.Error())
			return nil
		}
		if err := h.deliver(ctx, rc, c); err != nil {
			res.Failed++
			logger.ErrorContext(ctx, "optin: mailing not sent", "error", err.Error())
			return nil
		}
		res.Sent++
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("optin: mailing: %w", err)
	}
	return res, nil
}

// guard runs fn and turns a panic into a cause that names the type of the
// panic value only, because the value can hold an address. Every guarded
// call runs outside the request goroutine (in New, in the mail goroutine or
// in MailConfirmed), so nothing would catch a re-panic and it would end the
// process: http.ErrAbortHandler is recovered like any other value.
func guard(what string, fn func()) (cause error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		cause = loggedCause(what + " panicked with " + fmt.Sprintf("%T", p))
	}()
	fn()
	return nil
}

// loggedCause is a cause whose text is safe for a log line.
type loggedCause string

func (c loggedCause) Error() string { return string(c) }

// stages are the stage names of the mail package a log line may carry.
var stages = map[string]bool{
	"dial": true, "tls": true, "greeting": true, "ehlo": true, "starttls": true,
	"auth": true, "mail": true, "rcpt": true, "data": true, "send": true,
}

// safeCause is the text of a send error that may reach a log: the stage and
// code of a *mail.Error, the text of a mail sentinel, and the Go type name of
// anything else, because a custom Sender's error can hold the address.
func safeCause(err error) (cause error) {
	defer func() {
		if recover() != nil {
			cause = loggedCause(fmt.Sprintf("send failed with %T", err))
		}
	}()
	var me *mail.Error
	switch {
	case errors.As(err, &me) && me != nil:
		stage := "unknown"
		if stages[me.Stage] {
			stage = me.Stage
		}
		return loggedCause(fmt.Sprintf("mail: smtp stage %s code %d", stage, me.Code))
	case errors.Is(err, mail.ErrInvalid):
		return loggedCause(mail.ErrInvalid.Error())
	case errors.Is(err, mail.ErrNoStartTLS):
		return loggedCause(mail.ErrNoStartTLS.Error())
	case errors.Is(err, mail.ErrNoAuth):
		return loggedCause(mail.ErrNoAuth.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return loggedCause("send timed out")
	}
	return loggedCause(fmt.Sprintf("send failed with %T", err))
}
