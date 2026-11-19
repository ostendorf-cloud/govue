// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"testing"
	"time"
)

func validMessage() Message {
	return Message{
		From:    "Site <noreply@example.org>",
		To:      []string{"owner@example.org"},
		ReplyTo: "visitor@example.net",
		Subject: "Hello",
		Body:    "Body\n",
	}
}

func parse(t *testing.T, raw []byte) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	return msg
}

func TestBuildHeaders(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.Headers = map[string]string{"auto-submitted": "auto-generated", "X-Site": "one"}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	msg := parse(t, raw)
	want := map[string]string{
		"From":                      "\"Site\" <noreply@example.org>",
		"To":                        "<owner@example.org>",
		"Reply-To":                  "<visitor@example.net>",
		"Subject":                   "Hello",
		"Mime-Version":              "1.0",
		"Content-Type":              "text/plain; charset=utf-8",
		"Content-Transfer-Encoding": "quoted-printable",
		"Auto-Submitted":            "auto-generated",
		"X-Site":                    "one",
	}
	for k, v := range want {
		if got := msg.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if len(msg.Header) != len(want)+2 { // plus Date and Message-Id
		t.Errorf("headers = %v, want exactly %d plus Date and Message-Id", msg.Header, len(want))
	}
	if !strings.Contains(string(raw), "\r\n\r\n") || strings.Contains(strings.ReplaceAll(string(raw), "\r\n", ""), "\n") {
		t.Errorf("line ends are not CRLF only")
	}
	if _, err := msg.Header.Date(); err != nil {
		t.Errorf("Date: %v", err)
	}
}

func TestBuildNoReplyTo(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.ReplyTo = ""
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parse(t, raw).Header["Reply-To"]; ok {
		t.Error("Reply-To present without a ReplyTo")
	}
}

// Every field that reaches a header or the envelope refuses every character
// that could end a line or confuse a parser. An error never carries the value.
func TestInjectionRefused(t *testing.T) {
	t.Parallel()

	bad := []string{"\r\n", "\n", "\r", "\x00", "\x7f", "\x1b", "\u0085", " ", " ", "\t"}
	fields := map[string]func(m *Message, s string){
		"subject":     func(m *Message, s string) { m.Subject = "Hi" + s + "Bcc: evil@example.net" },
		"from name":   func(m *Message, s string) { m.From = "Si" + s + "te <noreply@example.org>" },
		"from addr":   func(m *Message, s string) { m.From = "noreply" + s + "@example.org" },
		"to":          func(m *Message, s string) { m.To = []string{"owner" + s + "@example.org"} },
		"to name":     func(m *Message, s string) { m.To = []string{"Ow" + s + "ner <owner@example.org>"} },
		"second to":   func(m *Message, s string) { m.To = []string{"a@example.org", "b" + s + "@example.org"} },
		"reply-to":    func(m *Message, s string) { m.ReplyTo = "v" + s + "@example.net" },
		"reply name":  func(m *Message, s string) { m.ReplyTo = "Vi" + s + "sitor <v@example.net>" },
		"header":      func(m *Message, s string) { m.Headers = map[string]string{"X-A": "a" + s + "b"} },
		"header name": func(m *Message, s string) { m.Headers = map[string]string{"X-A" + s: "a"} },
	}
	for name, set := range fields {
		for _, s := range bad {
			t.Run(name+"/"+strings.NewReplacer("\r", "CR", "\n", "LF", "\x00", "NUL", "\t", "TAB").Replace(s), func(t *testing.T) {
				t.Parallel()

				m := validMessage()
				set(&m, s)
				raw, err := Build(m)
				if err == nil {
					t.Fatalf("built a message:\n%q", raw)
				}
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("err = %v, want ErrInvalid", err)
				}
				if strings.Contains(err.Error(), "evil") || strings.ContainsAny(err.Error(), "\r\n\x00") {
					t.Errorf("error carries the value: %q", err)
				}
			})
		}
	}
}

func TestAddressesRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  func(m *Message)
	}{
		{"list in to", func(m *Message) { m.To = []string{"a@example.org, b@example.org"} }},
		{"list in reply-to", func(m *Message) { m.ReplyTo = "a@example.org, b@example.org" }},
		{"list in from", func(m *Message) { m.From = "a@example.org, b@example.org" }},
		{"group", func(m *Message) { m.To = []string{"team: a@example.org, b@example.org;"} }},
		{"empty group", func(m *Message) { m.ReplyTo = "team:;" }},
		{"group of one", func(m *Message) { m.ReplyTo = "g: a@example.org;" }},
		{"group of one in to", func(m *Message) { m.To = []string{"g: a@example.org;"} }},
		{"group of one in from", func(m *Message) { m.From = "g: noreply@example.org;" }},
		{"local part over 64", func(m *Message) { m.ReplyTo = strings.Repeat("l", MaxLocalPartBytes+1) + "@example.net" }},
		{"no address", func(m *Message) { m.ReplyTo = "visitor" }},
		{"non ascii local part", func(m *Message) { m.ReplyTo = "björn@example.net" }},
		{"non ascii domain", func(m *Message) { m.ReplyTo = "a@büro.example" }},
		{"quoted local part", func(m *Message) { m.ReplyTo = "\"a b\"@example.net" }},
		{"domain literal", func(m *Message) { m.ReplyTo = "a@[127.0.0.1]" }},
		{"angle injection", func(m *Message) { m.ReplyTo = "a@example.net>, <b@example.org" }},
		{"empty from", func(m *Message) { m.From = "" }},
		{"no recipient", func(m *Message) { m.To = nil }},
		{"too many recipients", func(m *Message) {
			m.To = nil
			for range MaxRecipients + 1 {
				m.To = append(m.To, "a@example.org")
			}
		}},
		{"long address", func(m *Message) { m.ReplyTo = strings.Repeat("a", MaxAddressBytes) + "@example.net" }},
		{"long name", func(m *Message) { m.ReplyTo = strings.Repeat("n", MaxNameBytes+1) + " <a@example.net>" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := validMessage()
			tc.set(&m)
			if _, err := Build(m); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSizeLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  func(m *Message)
		ok   bool
	}{
		{"subject at limit", func(m *Message) { m.Subject = strings.Repeat("s", MaxSubjectBytes) }, true},
		{"subject over limit", func(m *Message) { m.Subject = strings.Repeat("s", MaxSubjectBytes+1) }, false},
		{"body at limit", func(m *Message) { m.Body = strings.Repeat("b", MaxBodyBytes) }, true},
		{"body over limit", func(m *Message) { m.Body = strings.Repeat("b", MaxBodyBytes+1) }, false},
		{"body with NUL", func(m *Message) { m.Body = "a\x00b" }, false},
		{"body not UTF-8", func(m *Message) { m.Body = "a\xffb" }, false},
		{"subject not UTF-8", func(m *Message) { m.Subject = "a\xffb" }, false},
		{"too many headers", func(m *Message) {
			m.Headers = map[string]string{}
			for i := range MaxHeaders + 1 {
				m.Headers["X-H"+strings.Repeat("a", i+1)] = "v"
			}
		}, false},
		{"long header value", func(m *Message) { m.Headers = map[string]string{"X-A": strings.Repeat("v", MaxHeaderValueBytes+1)} }, false},
		{"reserved header", func(m *Message) { m.Headers = map[string]string{"bcc": "x@example.org"} }, false},
		{"reserved message id", func(m *Message) { m.Headers = map[string]string{"Message-ID": "<x@y>"} }, false},
		{"duplicate header", func(m *Message) { m.Headers = map[string]string{"x-a": "1", "X-A": "2"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := validMessage()
			tc.set(&m)
			_, err := Build(m)
			if tc.ok && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestUnicode(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.From = "Jörg Müller <noreply@example.org>"
	m.ReplyTo = "日本語 <visitor@example.net>"
	m.Subject = "Grüße " + strings.Repeat("日本語", 25)
	m.Body = "Grüße,\nzweite Zeile mit € und " + strings.Repeat("lang ", 60) + "\n.\nDot line\n"
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Errorf("line of %d bytes", len(line))
		}
	}
	msg := parse(t, raw)
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != m.Subject {
		t.Errorf("subject = %q, %v", subject, err)
	}
	if !strings.HasPrefix(msg.Header.Get("Subject"), "=?utf-8?q?") {
		t.Errorf("subject is not an encoded word: %q", msg.Header.Get("Subject"))
	}
	for _, h := range []struct{ name, want string }{
		{"From", "Jörg Müller"},
		{"Reply-To", "日本語"},
	} {
		list, err := msg.Header.AddressList(h.name)
		if err != nil || len(list) != 1 || list[0].Name != h.want {
			t.Errorf("%s = %v, %v", h.name, list, err)
		}
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || strings.ReplaceAll(string(body), "\r\n", "\n") != m.Body {
		t.Errorf("body = %q, %v", body, err)
	}
	for _, line := range strings.Split(strings.SplitN(string(raw), "\r\n\r\n", 2)[1], "\r\n") {
		if len(line) > 76 {
			t.Errorf("body line of %d bytes", len(line))
		}
	}
}

// A subject that looks like a header stays inside the Subject header.
func TestHeaderLookalikeStaysOneHeader(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.Subject = "Bcc: evil@example.net"
	m.From = "\"Bcc: evil@example.net\" <noreply@example.org>"
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	h := parse(t, raw).Header
	if _, ok := h["Bcc"]; ok {
		t.Error("a Bcc header exists")
	}
	if h.Get("Subject") != "Bcc: evil@example.net" {
		t.Errorf("Subject = %q", h.Get("Subject"))
	}
}

func TestBodyLoneCR(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.Body = "a\rb\r\nc"
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(parse(t, raw).Body))
	if string(body) != "a\r\nb\r\nc" {
		t.Errorf("body = %q", body)
	}
}

var messageID = regexp.MustCompile(`^<[0-9a-f]{32}@example\.org>$`)

func TestMessageIDAndDate(t *testing.T) {
	t.Parallel()

	first, err := Build(validMessage())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(validMessage())
	if err != nil {
		t.Fatal(err)
	}
	a, b := parse(t, first).Header.Get("Message-Id"), parse(t, second).Header.Get("Message-Id")
	if !messageID.MatchString(a) || !messageID.MatchString(b) {
		t.Errorf("ids = %q, %q", a, b)
	}
	if a == b {
		t.Errorf("two builds share the Message-ID %q", a)
	}

	at := time.Date(2026, 11, 19, 12, 30, 0, 0, time.FixedZone("x", 3600))
	got, err := build(validMessage(), at, strings.NewReader(strings.Repeat("x", 16)))
	if err != nil {
		t.Fatal(err)
	}
	if d := parse(t, got.data).Header.Get("Date"); d != "Thu, 19 Nov 2026 11:30:00 +0000" {
		t.Errorf("Date = %q", d)
	}
}

// A long non ASCII value is folded into encoded words: no line passes 78
// bytes, and the words decode back to the value.
func TestLongEncodedHeadersFold(t *testing.T) {
	t.Parallel()

	long := func(n int) string { return strings.Repeat("ü", n) }
	m := validMessage()
	m.From = long(MaxNameBytes/2) + " <noreply@example.org>"
	m.To = []string{long(MaxNameBytes/2) + " <owner@example.org>", "日本語 <b@example.org>"}
	m.ReplyTo = long(MaxNameBytes/2) + " <visitor@example.net>"
	m.Subject = long(MaxSubjectBytes / 2)
	m.Headers = map[string]string{"X-A": long(MaxHeaderValueBytes / 2), "X-Long-Header-Name": "日本語 " + long(100)}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if len(line) > 78 {
			t.Errorf("header line of %d bytes: %.40q", len(line), line)
		}
	}
	msg := parse(t, raw)
	dec := new(mime.WordDecoder)
	for name, want := range map[string]string{
		"Subject":            m.Subject,
		"X-A":                m.Headers["X-A"],
		"X-Long-Header-Name": m.Headers["X-Long-Header-Name"],
	} {
		if got, err := dec.DecodeHeader(msg.Header.Get(name)); err != nil || got != want {
			t.Errorf("%s decodes to %q, %v", name, got, err)
		}
	}
	for name, want := range map[string]string{
		"From": long(MaxNameBytes / 2), "To": long(MaxNameBytes / 2), "Reply-To": long(MaxNameBytes / 2),
	} {
		list, err := msg.Header.AddressList(name)
		if err != nil || len(list) == 0 || list[0].Name != want {
			t.Errorf("%s = %v, %v", name, list, err)
		}
	}
	if list, _ := msg.Header.AddressList("To"); len(list) != 2 || list[1].Name != "日本語" {
		t.Errorf("To = %v", list)
	}
}

// Whatever the limits allow, no line of the message passes 998 bytes.
func TestNoLineOver998(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.From = strings.Repeat("😀", MaxNameBytes/4) + " <" + strings.Repeat("a", MaxLocalPartBytes) + "@example.org>"
	m.Subject = strings.Repeat("😀", MaxSubjectBytes/4)
	m.Headers = map[string]string{"X-A": strings.Repeat("😀", MaxHeaderValueBytes/4)}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Errorf("line of %d bytes", len(line))
		}
	}
}

// A literal encoded word in a subject or header value is encoded itself, so a
// reader shows the text and never decodes a forged word.
func TestLiteralEncodedWordIsEncoded(t *testing.T) {
	t.Parallel()

	forged := "=?utf-8?q?a=0D=0ABcc:_evil@example.net?="
	m := validMessage()
	m.Subject = forged
	m.Headers = map[string]string{"X-A": "x " + forged}
	m.From = "\"" + forged + "\" <noreply@example.org>"
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "=?utf-8?q?a=0D") {
		t.Errorf("the forged word is on the wire:\n%s", raw)
	}
	msg := parse(t, raw)
	dec := new(mime.WordDecoder)
	if got, _ := dec.DecodeHeader(msg.Header.Get("Subject")); got != forged {
		t.Errorf("Subject decodes to %q, want the literal text", got)
	}
	if got, _ := dec.DecodeHeader(msg.Header.Get("X-A")); got != "x "+forged {
		t.Errorf("X-A decodes to %q", got)
	}
	if list, err := msg.Header.AddressList("From"); err != nil || len(list) != 1 || list[0].Name != forged {
		t.Errorf("From = %v, %v", list, err)
	}
	if _, ok := msg.Header["Bcc"]; ok {
		t.Error("a Bcc header exists")
	}
}

// An encoded word is decoded before the check, so one that decodes to a
// control character is refused like the character itself.
func TestDecodedNameRefused(t *testing.T) {
	t.Parallel()

	for _, word := range []string{
		"=?utf-8?q?a=0Db?=", "=?utf-8?q?a=0Ab?=", "=?utf-8?q?a=00b?=", "=?utf-8?q?a=1Bb?=",
		"=?utf-8?q?a=E2=80=A8b?=", "=?utf-8?q?a=E2=80=A9b?=", "=?utf-8?q?a=C2=85b?=",
		"=?utf-8?q?a=E2=80=AEb?=", "=?utf-8?q?a=E2=80=8Bb?=", "=?utf-8?b?YQ0KQmNjOiB4?=",
	} {
		for field, set := range map[string]func(m *Message){
			"From":     func(m *Message) { m.From = word + " <noreply@example.org>" },
			"To":       func(m *Message) { m.To = []string{word + " <owner@example.org>"} },
			"Reply-To": func(m *Message) { m.ReplyTo = word + " <v@example.net>" },
		} {
			m := validMessage()
			set(&m)
			raw, err := Build(m)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%s %s: err = %v, built %q", field, word, err, raw)
			} else if !strings.Contains(err.Error(), field) {
				t.Errorf("%s %s: error does not name the field: %v", field, word, err)
			}
		}
	}
}

// Format characters are invisible: bidi controls, zero width characters, the
// BOM. They are refused in every name, the subject and every extra header.
func TestFormatCharactersRefused(t *testing.T) {
	t.Parallel()

	for _, r := range []string{
		string(rune(0x202a)), string(rune(0x202b)), string(rune(0x202c)), string(rune(0x202d)),
		string(rune(0x202e)), string(rune(0x2066)), string(rune(0x2067)), string(rune(0x2068)),
		string(rune(0x2069)), string(rune(0x200b)),
		string(rune(0x2060)), string(rune(0xfeff)), string(rune(0x00ad)),
		string(rune(0x2061)), string(rune(0x2062)), string(rune(0x2063)), string(rune(0x2064)),
		string(rune(0x180e)), string(rune(0xe0041)), string(rune(0xe0001)),
		string(rune(0x2028)), string(rune(0x2029)),
	} {
		for field, set := range map[string]func(m *Message){
			"Subject":  func(m *Message) { m.Subject = "a" + r + "b" },
			"X-A":      func(m *Message) { m.Headers = map[string]string{"X-A": "a" + r + "b"} },
			"From":     func(m *Message) { m.From = "a" + r + "b <noreply@example.org>" },
			"To":       func(m *Message) { m.To = []string{"\"a" + r + "b\" <owner@example.org>"} },
			"Reply-To": func(m *Message) { m.ReplyTo = "a" + r + "b <v@example.net>" },
		} {
			m := validMessage()
			set(&m)
			_, err := Build(m)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), field) {
				t.Errorf("%s %U: err = %v", field, []rune(r)[0], err)
			}
		}
	}
}

// The zero width non joiner and joiner are needed by Persian, Indic and emoji
// sequences, so names and subjects keep them.
func TestJoinersAllowed(t *testing.T) {
	t.Parallel()

	zwjS, zwnjS := string(rune(0x200d)), string(rune(0x200c))

	for _, text := range []string{
		"\U0001F468" + zwjS + "\U0001F469" + zwjS + "\U0001F467",
		"می" + zwnjS + "خواهم",
	} {
		m := validMessage()
		m.Subject = text
		m.From = text + " <noreply@example.org>"
		raw, err := Build(m)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		msg := parse(t, raw)
		if got, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); err != nil || got != text {
			t.Errorf("Subject decodes to %q, %v", got, err)
		}
		if list, err := msg.Header.AddressList("From"); err != nil || len(list) != 1 || list[0].Name != text {
			t.Errorf("From = %v, %v", list, err)
		}
	}
}

// A semicolon in a quoted string or a comment is no group.
func TestSemicolonOutsideGroupAccepted(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{`"a;b" <a@b.c>`, `"a\";b" <a@b.c>`, `a@b.c (x;y)`} {
		m := validMessage()
		m.ReplyTo = addr
		if _, err := Build(m); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
}

// Every encoded word is at most 75 bytes, not only every line.
func TestEncodedWordsAtMost75Bytes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{strings.Repeat("ü", 100), strings.Repeat("a", 190) + "=?"} {
		m := validMessage()
		m.From = name + " <noreply@example.org>"
		m.Subject = name
		raw, err := Build(m)
		if err != nil {
			t.Fatal(err)
		}
		head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
		words := regexp.MustCompile(`=\?utf-8\?q\?[^?]*\?=`).FindAllString(head, -1)
		if len(words) < 2 {
			t.Fatalf("only %d words for %.10q", len(words), name)
		}
		for _, w := range words {
			if len(w) > 75 {
				t.Errorf("encoded word of %d bytes: %s", len(w), w)
			}
		}
	}
}

// The Date is the time the message is built, not a fixed value.
func TestDateFollowsBuildTime(t *testing.T) {
	t.Parallel()

	rnd := func() io.Reader { return strings.NewReader(strings.Repeat("x", 16)) }
	first, err := build(validMessage(), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), rnd())
	if err != nil {
		t.Fatal(err)
	}
	second, err := build(validMessage(), time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC), rnd())
	if err != nil {
		t.Fatal(err)
	}
	a, b := parse(t, first.data).Header.Get("Date"), parse(t, second.data).Header.Get("Date")
	if a == b || a != "Fri, 02 Jan 2026 03:04:05 +0000" || b != "Sun, 07 Jun 2026 08:09:10 +0000" {
		t.Errorf("dates = %q, %q", a, b)
	}

	raw, err := Build(validMessage())
	if err != nil {
		t.Fatal(err)
	}
	d, err := parse(t, raw).Header.Date()
	if err != nil || time.Since(d) > time.Minute || time.Since(d) < -time.Minute {
		t.Errorf("Build Date = %v, %v", d, err)
	}
}

func TestEnvelope(t *testing.T) {
	t.Parallel()

	m := validMessage()
	m.To = []string{"A <a@example.org>", "b@example.org"}
	b, err := build(m, time.Now(), strings.NewReader(strings.Repeat("x", 16)))
	if err != nil {
		t.Fatal(err)
	}
	if b.from != "noreply@example.org" || strings.Join(b.rcpts, ",") != "a@example.org,b@example.org" {
		t.Errorf("envelope = %q %q", b.from, b.rcpts)
	}
}

func TestRandomFailure(t *testing.T) {
	t.Parallel()

	if _, err := build(validMessage(), time.Now(), strings.NewReader("short")); err == nil {
		t.Fatal("built without randomness")
	}
}
