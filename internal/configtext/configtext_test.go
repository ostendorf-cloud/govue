// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package configtext_test

import (
	"testing"

	"github.com/ostendorf-cloud/govue/internal/configtext"
)

func TestQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"origin", "https://app.example.test", `"https://app.example.test"`},
		{"empty", "", `""`},
		{"userinfo", "https://user:s3cr3tTOKEN@app.example.test", configtext.Hidden},
		{"query token", "https://app.example.test?token=s3cr3tTOKEN", configtext.Hidden},
		{"fragment token", "https://app.example.test#access_token=s3cr3tTOKEN", configtext.Hidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := configtext.Quote(tc.input); got != tc.want {
				t.Errorf("Quote(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
