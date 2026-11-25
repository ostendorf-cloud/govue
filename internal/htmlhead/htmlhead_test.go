// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package htmlhead_test

import (
	"testing"

	"github.com/ostendorf-cloud/govue/internal/htmlhead"
)

func TestAfterHeadTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", -1},
		{"no head", `<html><body><p>x</p></body></html>`, -1},
		{"header element only", `<html><header>x</header></html>`, -1},
		{"head tag", `<html><head><title>t</title></head></html>`, len(`<html><head>`)},
		{
			"head with attributes", `<html><head data-x="1" lang="de"><title>t</title>`,
			len(`<html><head data-x="1" lang="de">`),
		},
		{"head after a newline", "<html><head\n>", len("<html><head\n>")},
		{"uppercase", `<HTML><HEAD><TITLE>t</TITLE>`, len(`<HTML><HEAD>`)},
		{"mixed case", `<Head>`, len(`<Head>`)},
		{"head only in a comment", `<!-- <head> --><body></body>`, -1},
		{"head after a comment", `<!-- <head> --><head>`, len(`<!-- <head> --><head>`)},
		{"unclosed comment", `<!-- <head>`, -1},
		{"unclosed head tag", `<html><head lang="de"`, -1},
		{"head at the very end", `<head`, -1},
		{"non ASCII before the head", "<html lang=\"İ\"><head>", len("<html lang=\"İ\"><head>")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := htmlhead.AfterHeadTag(tt.input); got != tt.want {
				t.Errorf("AfterHeadTag(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
