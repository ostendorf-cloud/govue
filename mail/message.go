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
	// MaxLocalPartBytes bounds the part of an address before the @, RFC 5321's
	// limit.
	MaxLocalPartBytes = 64
)

// The line lengths of RFC 5322: a generated line stays within the
// recommendation where it can, and never goes past the hard limit.
const (
	softLineBytes = 78
	hardLineBytes = 998
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

// maxWordBytes is the longest RFC 2047 encoded word.
const maxWordBytes = 75

const (
	zwnj = '‌' // zero width non joiner
	zwj  = '‍' // zero width joiner
)

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
	for _, r := range s {
		// Format characters (bidi controls, the BOM, tags, the soft hyphen)
		// are invisible and let a name or subject show something else than
		// it holds. The zero width non joiner and joiner stay allowed: scripts
		// such as Persian and Indic and emoji sequences need them. The line and
		// paragraph separators U+2028 and U+2029 are the Zl and Zp categories.
		if r == zwnj || r == zwj {
			continue
		}
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return invalid(field, "contains a format character")
		}
	}
	return nil
}

// hasGroupSyntax reports a semicolon outside a quoted string and a comment.
// Only a group ends in one, and net/mail's single address parser accepts a
// group of one member.
func hasGroupSyntax(s string) bool {
	quoted, depth := false, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && (quoted || depth > 0):
			i++
		case quoted:
			quoted = c != '"'
		case c == '"' && depth == 0:
			quoted = true
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case c == ';' && depth == 0:
			return true
		}
	}
	return false
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
	if hasGroupSyntax(raw) {
		return nil, invalid(field, "is not a single address")
	}
	a, err := mail.ParseAddress(raw)
	if err != nil {
		return nil, invalid(field, "is not a single address")
	}
	if len(a.Address) > MaxAddressBytes || !addrSpec.MatchString(a.Address) {
		return nil, invalid(field, "is not a plain ASCII address")
	}
	if local, _, _ := strings.Cut(a.Address, "@"); len(local) > MaxLocalPartBytes {
		return nil, invalid(field, "has too long a local part")
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
		{"From", formatAddress(from, len("From: "))},
		{"To", joinAddresses(to)},
	}
	if replyTo != nil {
		lines = append(lines, struct{ name, value string }{"Reply-To", formatAddress(replyTo, len("Reply-To: "))})
	}
	lines = append(lines,
		struct{ name, value string }{"Subject", encodeValue(len("Subject: "), m.Subject)},
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
	for _, line := range strings.Split(name+": "+value, "\r\n") {
		if len(line) > hardLineBytes {
			return invalid(name, "needs a line over 998 bytes")
		}
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
		prefix := 1 // the folding space
		if i == 0 {
			prefix = len("To: ")
		}
		parts[i] = formatAddress(a, prefix)
	}
	return strings.Join(parts, ",\r\n ")
}

// needsWords reports text that must travel as RFC 2047 words: non ASCII, or a
// literal "=?" that a reader could take for an encoded word the sender forged.
func needsWords(s string) bool {
	if strings.Contains(s, "=?") {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// encodeValue returns s as a header value, as RFC 2047 words folded with
// CRLF and a space when it needs them. prefix is the length of the line before
// the value.
func encodeValue(prefix int, s string) string {
	if !needsWords(s) {
		return s
	}
	words, _ := encodeWords(prefix, s)
	return words
}

// encodeWords writes s as "=?utf-8?q?...?=" words. No word, with its "=?utf-8?q?"
// and "?=", is longer than 75 bytes (RFC 2047), so a line stays within 78 bytes
// whenever the prefix leaves room for one, and always within 998. It returns
// the length of the last line too. Only letters and digits stay as they are,
// so no input can form a "=?".
func encodeWords(prefix int, s string) (string, int) {
	const overhead = len("=?utf-8?q?") + len("?=")
	// budget is the room for one whole word on the current line, at most 75.
	budget := min(max(softLineBytes-prefix, overhead+4*3), maxWordBytes) // room for one rune
	var out strings.Builder
	var word strings.Builder
	lineLen := prefix
	flush := func() {
		out.WriteString("=?utf-8?q?" + word.String() + "?=")
		lineLen += overhead + word.Len()
		word.Reset()
	}
	for _, r := range s {
		var enc strings.Builder
		var buf [utf8.UTFMax]byte
		for _, b := range buf[:utf8.EncodeRune(buf[:], r)] {
			switch {
			case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
				enc.WriteByte(b)
			case b == ' ':
				enc.WriteByte('_')
			default:
				fmt.Fprintf(&enc, "=%02X", b)
			}
		}
		if word.Len() > 0 && word.Len()+enc.Len() > budget-overhead {
			flush()
			out.WriteString("\r\n ")
			lineLen = 1
			budget = min(softLineBytes-1, maxWordBytes)
		}
		word.WriteString(enc.String())
	}
	flush()
	return out.String(), lineLen
}

// formatAddress renders one mailbox from the parsed parts, so nothing of the
// input survives except the name and the address. A name that needs words is
// folded, and the address goes on its own line when it would not fit.
func formatAddress(a *mail.Address, prefix int) string {
	if !needsWords(a.Name) {
		return a.String()
	}
	words, last := encodeWords(prefix, a.Name)
	angle := "<" + a.Address + ">"
	if last+1+len(angle) > softLineBytes {
		return words + "\r\n " + angle
	}
	return words + " " + angle
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
		out[canonical] = encodeValue(len(canonical)+2, value)
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
