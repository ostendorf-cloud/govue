// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package contact

import (
	"strings"
	"testing"
)

func valid() Request {
	return Request{Name: "Ada Lovelace", Email: "ada@example.org", Message: "Hello there."}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	got := Normalize(Request{Name: "  Ada \t", Email: " ada@example.org\n", Message: "\n a\r\nb\rc \n"})
	want := Request{Name: "Ada", Email: "ada@example.org", Message: "a\nb\nc"}
	if got != want {
		t.Fatalf("Normalize = %+v, want %+v", got, want)
	}
	// A control character is not repaired.
	if got := Normalize(Request{Name: "a\x00b"}); got.Name != "a\x00b" {
		t.Fatalf("Normalize repaired the name: %q", got.Name)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mod    func(*Request)
		field  string
		code   string
		wantOK bool
	}{
		{name: "valid", mod: func(*Request) {}, wantOK: true},
		{name: "name required", mod: func(r *Request) { r.Name = "" }, field: FieldName, code: CodeRequired},
		{name: "email required", mod: func(r *Request) { r.Email = "" }, field: FieldEmail, code: CodeRequired},
		{name: "message required", mod: func(r *Request) { r.Message = "" }, field: FieldMessage, code: CodeRequired},
		{name: "name 200 runes of two bytes", mod: func(r *Request) { r.Name = strings.Repeat("ä", 200) }, wantOK: true},
		{name: "name 201 runes", mod: func(r *Request) { r.Name = strings.Repeat("ä", 201) }, field: FieldName, code: CodeTooLong},
		{name: "message 5000 runes", mod: func(r *Request) { r.Message = strings.Repeat("€", 5000) }, wantOK: true},
		{name: "message 5001 runes", mod: func(r *Request) { r.Message = strings.Repeat("€", 5001) }, field: FieldMessage, code: CodeTooLong},
		{
			name: "email 255 runes", field: FieldEmail, code: CodeTooLong,
			mod: func(r *Request) { r.Email = strings.Repeat("a", 243) + "@example.org" },
		},
		{name: "name LF", mod: func(r *Request) { r.Name = "a\nBcc: x@example.org" }, field: FieldName, code: CodeInvalidChars},
		{name: "name CR", mod: func(r *Request) { r.Name = "a\rb" }, field: FieldName, code: CodeInvalidChars},
		{name: "name NUL", mod: func(r *Request) { r.Name = "a\x00b" }, field: FieldName, code: CodeInvalidChars},
		{name: "name tab", mod: func(r *Request) { r.Name = "a\tb" }, field: FieldName, code: CodeInvalidChars},
		{name: "name line separator", mod: func(r *Request) { r.Name = "a b" }, field: FieldName, code: CodeInvalidChars},
		{name: "name bidi override", mod: func(r *Request) { r.Name = "a" + string(rune(0x202e)) + "b" }, field: FieldName, code: CodeInvalidChars},
		{name: "name ZWJ allowed", mod: func(r *Request) { r.Name = "a" + string(rune(0x200d)) + "b" }, wantOK: true},
		{name: "name invalid UTF-8", mod: func(r *Request) { r.Name = "a\xffb" }, field: FieldName, code: CodeInvalidChars},
		{name: "message keeps LF and tab", mod: func(r *Request) { r.Message = "a\n\tb" }, wantOK: true},
		{name: "message NUL", mod: func(r *Request) { r.Message = "a\x00b" }, field: FieldMessage, code: CodeInvalidChars},
		{name: "message ESC", mod: func(r *Request) { r.Message = "a\x1bb" }, field: FieldMessage, code: CodeInvalidChars},
		{name: "email unicode", mod: func(r *Request) { r.Email = "ädä@example.org" }, field: FieldEmail, code: CodeNonASCIIEmail},
		{name: "email unicode domain", mod: func(r *Request) { r.Email = "a@bücher.example" }, field: FieldEmail, code: CodeNonASCIIEmail},
		{name: "email LF", mod: func(r *Request) { r.Email = "a@example.org\nBcc: x@example.org" }, field: FieldEmail, code: CodeInvalidChars},
		{name: "email no at", mod: func(r *Request) { r.Email = "ada.example.org" }, field: FieldEmail, code: CodeInvalid},
		{name: "email display name", mod: func(r *Request) { r.Email = "Ada <ada@example.org>" }, field: FieldEmail, code: CodeInvalid},
		{name: "email list", mod: func(r *Request) { r.Email = "a@example.org, b@example.org" }, field: FieldEmail, code: CodeInvalid},
		{name: "email group", mod: func(r *Request) { r.Email = "g:a@example.org;" }, field: FieldEmail, code: CodeInvalid},
		{name: "email space", mod: func(r *Request) { r.Email = "a b@example.org" }, field: FieldEmail, code: CodeInvalid},
		{name: "email quoted local part", mod: func(r *Request) { r.Email = `"a b"@example.org` }, field: FieldEmail, code: CodeInvalid},
		{name: "email local part 65", mod: func(r *Request) { r.Email = strings.Repeat("a", 65) + "@example.org" }, field: FieldEmail, code: CodeInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := valid()
			tc.mod(&r)
			got := Validate(r)
			if tc.wantOK {
				if got != nil {
					t.Fatalf("Validate = %v, want nil", got)
				}
				return
			}
			if len(got) != 1 || got[tc.field] != tc.code {
				t.Fatalf("Validate = %v, want {%s: %s}", got, tc.field, tc.code)
			}
		})
	}
}

func TestValidateReportsEveryField(t *testing.T) {
	t.Parallel()
	got := Validate(Request{})
	for _, f := range []string{FieldName, FieldEmail, FieldMessage} {
		if got[f] != CodeRequired {
			t.Fatalf("field %s = %q in %v", f, got[f], got)
		}
	}
}

func TestValidateNeverEchoesAValue(t *testing.T) {
	t.Parallel()
	got := Validate(Request{Name: "secret\nname", Email: "secret@bücher", Message: ""})
	for k, v := range got {
		if strings.Contains(k+v, "secret") {
			t.Fatalf("result holds a value: %v", got)
		}
	}
}
