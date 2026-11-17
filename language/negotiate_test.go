// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package language_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/language"
)

func TestNegotiate(t *testing.T) {
	t.Parallel()
	langs := []string{"de", "en"}

	tests := []struct {
		name   string
		header string
		want   string
		wantOK bool
	}{
		{"empty", "", "", false},
		{"single", "en", "en", true},
		{"upper case", "EN", "en", true},
		{"region", "de-AT", "de", true},
		{"region and script", "en-Latn-US", "en", true},
		{"first wins at equal q", "en,de", "en", true},
		{"q orders", "de;q=0.4,en;q=0.8", "en", true},
		{"q with spaces around the semicolon", "de ; q=0.4 , en ; q=0.8", "en", true},
		{"q key in capitals", "de;Q=0.4,en;Q=0.8", "en", true},
		{"other parameters ignored", "en;level=1;q=0.2,de;q=0.1", "en", true},
		{"q zero excludes the only match", "en;q=0", "", false},
		{"q zero point zero excludes", "en;q=0.000, de;q=0.001", "de", true},
		{"q three decimals", "en;q=0.999,de;q=0.998", "en", true},
		{"q one with zeros", "en;q=1.000", "en", true},
		{"q one point one dropped", "en;q=1.1", "", false},
		{"q four decimals dropped", "en;q=0.1234", "", false},
		{"q negative dropped", "en;q=-1", "", false},
		{"q text dropped", "en;q=abc", "", false},
		{"q empty dropped", "en;q=", "", false},
		{"q without value dropped", "en;q", "", false},
		{"q trailing dot dropped", "en;q=1.", "", false},
		{"q leading dot dropped", "en;q=.5", "", false},
		{"q exponent dropped", "en;q=1e0", "", false},
		{"q two dots dropped", "en;q=0..5", "", false},
		{"q plus sign dropped", "en;q=+1", "", false},
		{"q non ASCII digit dropped", "en;q=0.٥", "", false},
		{"duplicate q dropped", "en;q=1;q=0.5, de;q=0.1", "de", true},
		{"bad q dropped, rest counts", "de;q=abc, en;q=0.5", "en", true},
		{"wildcard with bad q and named bad q", "de;q=abc, *;q=2", "", false},
		{"wildcard alone takes the first language", "*", "de", true},
		{"wildcard q zero", "*;q=0", "", false},
		{"wildcard skips named zero", "de;q=0, *", "en", true},
		{"wildcard does not override a named lower q", "en;q=0.1, *;q=0.9", "de", true},
		{"wildcard lower than named", "*;q=0.1, en;q=0.2", "en", true},
		{"wildcard with region is no wildcard", "*-x", "", false},
		{"unknown language", "fr, es;q=0.5", "", false},
		{"only separators", ";;,,", "", false},
		{"only a semicolon q", ";q=1", "", false},
		{"only spaces", "   ", "", false},
		{"only commas", ",,,,,,", "", false},
		{"duplicate entries keep the best q", "de;q=0.1,en;q=0.5,de;q=0.9", "de", true},
		{"duplicate zero then non zero", "de;q=0,de;q=0.5,en;q=0.4", "de", true},
		{"primary subtag only", "deu", "", false},
		{"tag with garbage", "d\x00e", "", false},
		{"prefix is not a match", "dee", "", false},
		{"entry cap, 32nd entry read", strings.Repeat("fr,", 31) + "en", "en", true},
		{"entry cap, 33rd entry ignored", strings.Repeat("fr,", 32) + "en", "", false},
		{"empty entries count toward the cap", strings.Repeat(",", 32) + "en", "", false},
		{"header at the length cap", strings.Repeat(" ", 4096-2) + "en", "en", true},
		{"header over the length cap", strings.Repeat(" ", 4096-1) + "en", "", false},
		{"long q digits", "en;q=0." + strings.Repeat("0", 4000), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := language.Negotiate(tt.header, langs)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("Negotiate(%.60q) = %q, %v; want %q, %v", tt.header, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestNegotiate_noLanguages(t *testing.T) {
	t.Parallel()
	for _, langs := range [][]string{nil, {}} {
		if got, ok := language.Negotiate("en", langs); ok || got != "" {
			t.Fatalf("Negotiate with no languages = %q, %v", got, ok)
		}
	}
}

func TestNegotiate_wildcardFollowsListOrder(t *testing.T) {
	t.Parallel()
	got, ok := language.Negotiate("*", []string{"fr", "en", "de"})
	if !ok || got != "fr" {
		t.Fatalf("got %q, %v", got, ok)
	}
	got, ok = language.Negotiate("fr;q=0, *", []string{"fr", "en", "de"})
	if !ok || got != "en" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func FuzzNegotiate(f *testing.F) {
	for _, seed := range []string{
		"", "en", "de;q=abc, *;q=2", "*", ";;,,", "en;q=1.0001", "de;q=0, en",
		"en-US,de;q=0.5", "\x00;q=\xff", strings.Repeat("en,", 2000),
	} {
		f.Add(seed)
	}
	langs := []string{"de", "en"}
	f.Fuzz(func(t *testing.T, header string) {
		got, ok := language.Negotiate(header, langs)
		if ok && !slices.Contains(langs, got) {
			t.Fatalf("result %q is not a configured language", got)
		}
		if !ok && got != "" {
			t.Fatalf("result %q with ok false", got)
		}
	})
}
