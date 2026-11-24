// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package contact

import (
	"net/http"
	"strings"
	"testing"
)

func TestDefaultBodyLabelsPerLanguage(t *testing.T) {
	t.Parallel()
	const en = "Name: Ada Lovelace\nEmail: ada@example.org\n\nMessage:\nHello there.\n"
	const de = "Name: Ada Lovelace\nE-Mail: ada@example.org\n\nNachricht:\nHello there.\n"
	for _, tc := range []struct {
		lang string
		want string
	}{
		{"", en},
		{"en", en},
		{"de", de},
		{"DE", de},
		{"de-AT", de},
		{"en-GB", en},
		{"fr", en},
		{"xx-unknown", en},
		{"de\nX-Evil: 1", en},
	} {
		r := valid()
		r.Language = tc.lang
		if got := DefaultBody(r); got != tc.want {
			t.Errorf("DefaultBody(language %q) = %q, want %q", tc.lang, got, tc.want)
		}
	}
}

func TestLabelsFor(t *testing.T) {
	t.Parallel()
	if got := LabelsFor("de"); got != (Labels{Name: "Name", Email: "E-Mail", Message: "Nachricht"}) {
		t.Fatalf("de = %+v", got)
	}
	if got := LabelsFor("en"); got != (Labels{Name: "Name", Email: "Email", Message: "Message"}) {
		t.Fatalf("en = %+v", got)
	}
	if LabelsFor("nl") != LabelsFor("en") {
		t.Fatal("an unknown language must fall back to English")
	}
}

func TestNormalizeLanguage(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"de":                      "de",
		" De-AT ":                 "de-at",
		"":                        "",
		"de\nX-Evil: 1":           "",
		"de\r\n":                  "de",
		"d3":                      "",
		"zh-Hant-TW-x":            "",
		"abcdefgh":                "abcdefgh",
		"abcdefghi":               "",
		strings.Repeat("a", 5000): "",
		"dé":                      "",
		"de\x00":                  "",
		"en_US":                   "",
		"<script>":                "",
		"\xe2\x80\xaede":          "",
	} {
		if got := Normalize(Request{Language: in}).Language; got != want {
			t.Errorf("Normalize(language %q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubmitLanguageSelectsLabels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		lang any // nil leaves the key out
		want []string
	}{
		{"english", "en", []string{"Name: " + secretName + "\n", "Email: " + secretEmail + "\n", "\nMessage:\n" + secretMessage}},
		{"german", "de", []string{"Name: " + secretName + "\n", "E-Mail: " + secretEmail + "\n", "\nNachricht:\n" + secretMessage}},
		{"missing", nil, []string{"Email: " + secretEmail + "\n", "\nMessage:\n" + secretMessage}},
		{"unknown", "fr", []string{"Email: " + secretEmail + "\n", "\nMessage:\n" + secretMessage}},
		{"long", strings.Repeat("de", 3000), []string{"Email: " + secretEmail + "\n", "\nMessage:\n"}},
		{"line break", "de\nX-Injected: yes", []string{"Email: " + secretEmail + "\n", "\nMessage:\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			body := e.body(t)
			if tc.lang != nil {
				body["language"] = tc.lang
			}
			if w := e.post(t, body); w.Code != http.StatusOK {
				t.Fatalf("answer = %d %s", w.Code, w.Body)
			}
			got := e.sender.sent[0].Body
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("body %q lacks %q", got, want)
				}
			}
			if strings.Contains(got, "X-Injected") {
				t.Errorf("language reached the body: %q", got)
			}
		})
	}
}

func TestSubmitStaysStrictAboutOtherUnknownFields(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	body := e.body(t)
	body["languages"] = "de"
	if w := e.post(t, body); w.Code != http.StatusBadRequest {
		t.Fatalf("answer = %d %s, want 400", w.Code, w.Body)
	}
}

func TestSubmitLineBreaksCannotForgeLabels(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"name", "email"} {
		t.Run(field+" is refused", func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			body := e.body(t)
			body["language"] = "de"
			body[field] = "x\nNachricht: forged"
			if w := e.post(t, body); w.Code != http.StatusBadRequest {
				t.Fatalf("answer = %d %s, want 400", w.Code, w.Body)
			}
			if e.sender.count() != 0 {
				t.Fatal("a mail was sent")
			}
		})
	}
	t.Run("message stays under its own label", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t, nil)
		body := e.body(t)
		body["language"] = "de"
		body["message"] = "hi\nE-Mail: forged@example.org\nName: forged"
		if w := e.post(t, body); w.Code != http.StatusOK {
			t.Fatalf("answer = %d %s", w.Code, w.Body)
		}
		got := e.sender.sent[0].Body
		head, rest, ok := strings.Cut(got, "\n\nNachricht:\n")
		if !ok {
			t.Fatalf("no message label in %q", got)
		}
		// Before the message label there are exactly the two label lines.
		if lines := strings.Split(head, "\n"); len(lines) != 2 || strings.Contains(head, "forged") {
			t.Fatalf("head = %q", head)
		}
		if !strings.HasPrefix(rest, "hi\nE-Mail: forged@example.org") {
			t.Fatalf("message = %q", rest)
		}
	})
}

func TestConfigLabels(t *testing.T) {
	t.Parallel()
	french := func(lang string) Labels {
		if lang == "fr" {
			return Labels{Name: "Nom", Email: "Courriel", Message: "Message"}
		}
		return LabelsFor(lang)
	}
	e := newEnv(t, func(c *Config) { c.Labels = french })
	body := e.body(t)
	body["language"] = "fr"
	if w := e.post(t, body); w.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if got := e.sender.sent[0].Body; !strings.HasPrefix(got, "Nom: "+secretName+"\nCourriel: "+secretEmail+"\n\nMessage:\n") {
		t.Fatalf("body = %q", got)
	}
	// The site's function sees the cleaned hint, and German still works.
	body = e.body(t)
	body["language"] = "de"
	if w := e.post(t, body); w.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if got := e.sender.sent[1].Body; !strings.Contains(got, "E-Mail: ") {
		t.Fatalf("body = %q", got)
	}
}

func TestLabelsBodyFallsBackForEmptyOrMultiLineLabel(t *testing.T) {
	t.Parallel()
	r := valid()
	for _, tc := range []struct {
		name string
		in   Labels
		want string
	}{
		{"all empty", Labels{}, "Name: Ada Lovelace\nEmail: ada@example.org\n\nMessage:\nHello there.\n"},
		{"empty email", Labels{Name: "Nom", Message: "Texte"}, "Nom: Ada Lovelace\nEmail: ada@example.org\n\nTexte:\nHello there.\n"},
		{"LF in name", Labels{Name: "Nom\nX: y", Email: "Courriel", Message: "Texte"}, "Name: Ada Lovelace\nCourriel: ada@example.org\n\nTexte:\nHello there.\n"},
		{"CR in message", Labels{Name: "Nom", Email: "Courriel", Message: "Texte\r"}, "Nom: Ada Lovelace\nCourriel: ada@example.org\n\nMessage:\nHello there.\n"},
	} {
		if got := tc.in.Body(r); got != tc.want {
			t.Errorf("%s: Body = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSiteBodyWinsOverLabels(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) {
		c.Labels = func(string) Labels { return Labels{Name: "L"} }
		c.Body = func(r Request) string { return "own " + r.Language }
	})
	body := e.body(t)
	body["language"] = "de"
	if w := e.post(t, body); w.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if got := e.sender.sent[0].Body; got != "own de" {
		t.Fatalf("body = %q", got)
	}
}
