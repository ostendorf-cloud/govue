// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package language_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ostendorf-cloud/govue/language"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		cfg       language.Config
		wantCodes []string
		wantDef   string
	}{
		{name: "zero value", wantCodes: []string{"de", "en"}, wantDef: "de"},
		{
			name:      "default first",
			cfg:       language.Config{Languages: []string{"fr", "en", "de"}, Default: "en"},
			wantCodes: []string{"en", "fr", "de"},
			wantDef:   "en",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes, def, err := tt.cfg.Resolve()
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !slices.Equal(codes, tt.wantCodes) || def != tt.wantDef {
				t.Errorf("Resolve = %v, %q, want %v, %q", codes, def, tt.wantCodes, tt.wantDef)
			}
		})
	}
}

func TestResolveRefusesWhatRedirectRefuses(t *testing.T) {
	_, _, err := language.Config{Languages: []string{"de"}, Default: "en"}.Resolve()
	if !errors.Is(err, language.ErrConfig) {
		t.Errorf("Resolve error = %v, want ErrConfig", err)
	}
}

func TestResolveReturnsACopy(t *testing.T) {
	cfg := language.Config{}
	codes, _, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	codes[0] = "xx"
	again, _, _ := cfg.Resolve()
	if again[0] != "de" {
		t.Errorf("a change to the result reached the next call: %v", again)
	}
}
