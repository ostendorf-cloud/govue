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
