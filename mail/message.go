// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The size limits of a message. A value over its limit is refused, never cut.
const (
	// MaxSubjectBytes bounds the subject before encoding.
	MaxSubjectBytes = 255
	// MaxBodyBytes bounds the body.
	MaxBodyBytes = 256 << 10
	// MaxRecipients bounds the To list.
	MaxRecipients = 20
	// MaxHeaders bounds the extra headers.
	MaxHeaders = 20
	// MaxHeaderValueBytes bounds the value of an extra header.
	MaxHeaderValueBytes = 900
	// MaxNameBytes bounds a display name before encoding.
	MaxNameBytes = 200
	// MaxAddressBytes bounds the address itself, RFC 5321's path limit.
	MaxAddressBytes = 254
)

// ErrInvalid is wrapped by every error a message is refused with. The error
// names the field and the rule, never the value, because a value may be a
// visitor's name or address and an error reaches logs.
var ErrInvalid = errors.New("mail: invalid message")

// Message is one plain text mail. Every field is checked by Build and by
// Send, and a refused field refuses the whole message: nothing is stripped
// or repaired, because a silent repair hides an attack.
type Message struct {
	// From is one address, with an optional display name:
	// "Site <noreply@example.org>".
	From string
	// To holds one to MaxRecipients addresses of the same form.
	To []string
	// ReplyTo is empty or one address of the same form. A visitor's address
	// belongs here and never in From, so the mail still passes the sender
	// domain's SPF and DKIM.
	ReplyTo string
	// Subject is any UTF-8 text without control characters.
	Subject string
	// Body is UTF-8 text. Line breaks are allowed, NUL is not.
	Body string
	// Headers are extra headers by name, for example Auto-Submitted. A
	// header the builder sets itself is refused.
	Headers map[string]string
}

// reserved are the headers Build writes itself.
var reserved = map[string]bool{
	"From": true, "To": true, "Cc": true, "Bcc": true, "Reply-To": true,
	"Sender": true, "Subject": true, "Date": true, "Message-Id": true,
	"Mime-Version": true, "Content-Type": true,
	"Content-Transfer-Encoding": true, "Return-Path": true,
}

var (
	headerName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)
	// addrSpec is the part of RFC 5321 a mail needs: an ASCII dot atom, an
	// @ and a host name. Quoted local parts and domain literals are refused.
	addrSpec = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(\\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*" +
		"@[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$")
)

func invalid(field, rule string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalid, field, rule)
}

// checkText refuses what could end a header or confuse a parser: invalid
// UTF-8, NUL, CR, LF, every other control character, and the Unicode line
// and paragraph separators some software treats as a line break.
func checkText(field, s string) error {
	if !utf8.ValidString(s) {
		return invalid(field, "is not valid UTF-8")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return invalid(field, "contains a control character")
		}
	}
	return nil
}

// parseAddress accepts exactly one mailbox: not a list, not a group.
func parseAddress(field, raw string) (*mail.Address, error) {
	if err := checkText(field, raw); err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, invalid(field, "is empty")
	}
	if len(raw) > MaxAddressBytes+MaxNameBytes+4 {
		return nil, invalid(field, "is too long")
	}
	a, err := mail.ParseAddress(raw)
	if err != nil {
		return nil, invalid(field, "is not a single address")
	}
	if len(a.Address) > MaxAddressBytes || !addrSpec.MatchString(a.Address) {
		return nil, invalid(field, "is not a plain ASCII address")
	}
	if len(a.Name) > MaxNameBytes {
		return nil, invalid(field, "has too long a name")
	}
	if err := checkText(field, a.Name); err != nil {
		return nil, err
	}
	return a, nil
}

// built is a message ready for the wire, with its envelope.
type built struct {
	from  string
	rcpts []string
	data  []byte
}

// Build renders m as an RFC 5322 message with CRLF line ends, ready to hand
// to a submission server. It checks every field, see Message.
func Build(m Message) ([]byte, error) {
	b, err := build(m, time.Now(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return b.data, nil
}

func build(m Message, now time.Time, random io.Reader) (built, error) {
	from, err := parseAddress("From", m.From)
	if err != nil {
		return built{}, err
	}
	if len(m.To) == 0 || len(m.To) > MaxRecipients {
		return built{}, invalid("To", "needs one to "+fmt.Sprint(MaxRecipients)+" addresses")
	}
	to := make([]*mail.Address, len(m.To))
	for i, raw := range m.To {
		if to[i], err = parseAddress("To", raw); err != nil {
			return built{}, err
		}
	}
	var replyTo *mail.Address
	if m.ReplyTo != "" {
		if replyTo, err = parseAddress("Reply-To", m.ReplyTo); err != nil {
			return built{}, err
		}
	}
	if err := checkText("Subject", m.Subject); err != nil {
		return built{}, err
	}
	if len(m.Subject) > MaxSubjectBytes {
		return built{}, invalid("Subject", "is too long")
	}
	if !utf8.ValidString(m.Body) || strings.ContainsRune(m.Body, 0) {
		return built{}, invalid("Body", "is not UTF-8 text")
	}
	if len(m.Body) > MaxBodyBytes {
		return built{}, invalid("Body", "is too long")
	}
	extra, err := extraHeaders(m.Headers)
	if err != nil {
		return built{}, err
	}

	var id [16]byte
	if _, err := io.ReadFull(random, id[:]); err != nil {
		return built{}, fmt.Errorf("mail: message id: %w", err)
	}
	_, domain, _ := strings.Cut(from.Address, "@")

	var buf bytes.Buffer
	lines := []struct{ name, value string }{
		{"From", from.String()},
		{"To", joinAddresses(to)},
	}
	if replyTo != nil {
		lines = append(lines, struct{ name, value string }{"Reply-To", replyTo.String()})
	}
	lines = append(lines,
		struct{ name, value string }{"Subject", mime.QEncoding.Encode("utf-8", m.Subject)},
		struct{ name, value string }{"Date", now.UTC().Format(time.RFC1123Z)},
		struct{ name, value string }{"Message-ID", "<" + hex.EncodeToString(id[:]) + "@" + domain + ">"},
		struct{ name, value string }{"MIME-Version", "1.0"},
		struct{ name, value string }{"Content-Type", "text/plain; charset=utf-8"},
		struct{ name, value string }{"Content-Transfer-Encoding", "quoted-printable"},
	)
	for _, l := range lines {
		if err := writeHeader(&buf, l.name, l.value); err != nil {
			return built{}, err
		}
	}
	for _, name := range sortedKeys(extra) {
		if err := writeHeader(&buf, name, extra[name]); err != nil {
			return built{}, err
		}
	}
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(normalizeNewlines(m.Body))); err != nil {
		return built{}, fmt.Errorf("mail: encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return built{}, fmt.Errorf("mail: encode body: %w", err)
	}

	rcpts := make([]string, len(to))
	for i, a := range to {
		rcpts[i] = a.Address
	}
	return built{from: from.Address, rcpts: rcpts, data: buf.Bytes()}, nil
}

// writeHeader writes a generated header. The encoded words of a subject or a
// name legitimately hold "CRLF space" folds, so the control character check
// ran on the raw input and a generated line only has to carry no bare CR, LF
// or NUL.
func writeHeader(buf *bytes.Buffer, name, value string) error {
	if strings.ContainsRune(value, 0) || hasBareBreak(value) {
		return invalid(name, "contains a control character")
	}
	buf.WriteString(name + ": " + value + "\r\n")
	return nil
}

// hasBareBreak reports a CR or LF that is not part of a CRLF space fold.
func hasBareBreak(s string) bool {
	s = strings.ReplaceAll(s, "\r\n ", "")
	return strings.ContainsAny(s, "\r\n")
}

func joinAddresses(list []*mail.Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String()
	}
	return strings.Join(parts, ",\r\n ")
}

func extraHeaders(in map[string]string) (map[string]string, error) {
	if len(in) > MaxHeaders {
		return nil, invalid("Headers", "has too many entries")
	}
	out := make(map[string]string, len(in))
	for name, value := range in {
		if !headerName.MatchString(name) {
			return nil, invalid("Headers", "has an invalid name")
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if reserved[canonical] {
			return nil, invalid("Headers", "names a header the builder sets")
		}
		if _, dup := out[canonical]; dup {
			return nil, invalid("Headers", "names a header twice")
		}
		if err := checkText(canonical, value); err != nil {
			return nil, err
		}
		if len(value) > MaxHeaderValueBytes {
			return nil, invalid(canonical, "is too long")
		}
		out[canonical] = mime.QEncoding.Encode("utf-8", value)
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// normalizeNewlines turns every CR, LF or CRLF into LF, which the
// quoted-printable writer then writes as CRLF. A lone CR would otherwise be
// encoded as =0D and show up as a stray character.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
