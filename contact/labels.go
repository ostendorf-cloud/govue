// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package contact

import "strings"

// MaxLanguageLen is the longest language hint [Normalize] keeps.
const MaxLanguageLen = 8

// Labels are the words in front of the values in the plain text mail body.
// They are fixed text of the site or of this package, never visitor input,
// and the colon is added by [Labels.Body]. A label must not contain a line
// break. [Labels.Body] uses the built in English label for a field that is
// empty or holds a CR or LF.
type Labels struct {
	Name    string
	Email   string
	Message string
}

// The built in label sets. German is the formal catalogue where it matters
// and here the three words need no register.
var (
	labelsEN = Labels{Name: "Name", Email: "Email", Message: "Message"}
	labelsDE = Labels{Name: "Name", Email: "E-Mail", Message: "Nachricht"}
)

// LabelsFor returns the built in labels for a language code: "en" and "de".
// A region or script after a hyphen is ignored ("de-AT" is "de") and the case
// does not matter. Any other value, an empty one and one with a line break
// included, gets the English labels. The value is never part of the result.
func LabelsFor(lang string) Labels {
	primary, _, _ := strings.Cut(strings.ToLower(lang), "-")
	if primary == "de" {
		return labelsDE
	}
	return labelsEN
}

// Body writes the plain text body: the name and the address on one line each,
// a blank line, the message label and the message. The labels are fixed
// strings, so only the three values come from the visitor. [Validate]
// refuses a line break in the name and the address, and the message follows
// its own label and a blank line, so it cannot become a label of the body.
func (l Labels) Body(r Request) string {
	return safeLabel(l.Name, labelsEN.Name) + ": " + r.Name + "\n" +
		safeLabel(l.Email, labelsEN.Email) + ": " + r.Email + "\n\n" +
		safeLabel(l.Message, labelsEN.Message) + ":\n" + r.Message + "\n"
}

// safeLabel returns the label, or the built in English fallback when the
// label is empty or holds a CR or LF.
func safeLabel(label, fallback string) string {
	if label == "" || strings.ContainsAny(label, "\r\n") {
		return fallback
	}
	return label
}

// cleanLanguage keeps a language hint that has the shape of a language tag:
// ASCII letters and hyphens, at most [MaxLanguageLen] bytes. Anything else
// becomes the empty string, which selects the English labels. It is a hint
// and never an error.
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
