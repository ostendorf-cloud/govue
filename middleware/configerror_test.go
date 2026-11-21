// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"errors"
	"testing"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

// An origin or a scheme that may carry a credential stays out of a startup
// error. The sentinel stays, so a caller can still match the refusal.
func TestOriginErrorsKeepACredentialOut(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		build func() error
		want  string
	}{
		{"cors origin with userinfo", func() error {
			_, err := NewCORS(CORSConfig{Origins: []string{"https://user:s3cr3tTOKEN@app.example.test"}})
			return err
		}, "middleware: origin"},
		{"cors origin with a query token", func() error {
			_, err := NewCORS(CORSConfig{Origins: []string{"https://app.example.test?token=s3cr3tTOKEN"}})
			return err
		}, "middleware: origin"},
		{"app origin with userinfo", func() error {
			_, err := NewAppOrigins([]string{"capacitor://user:s3cr3tTOKEN@localhost"}, "capacitor")
			return err
		}, "app origin"},
		{"app origin with a fragment token", func() error {
			_, err := NewAppOrigins([]string{"https://localhost#s3cr3tTOKEN"})
			return err
		}, "app origin"},
		{"app scheme with userinfo", func() error {
			_, err := NewAppOrigins([]string{"https://localhost"}, "user:s3cr3tTOKEN@capacitor")
			return err
		}, "app scheme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.build()
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", tc.want)
			if !errors.Is(err, errInvalidOrigin) {
				t.Errorf("error %v is not errInvalidOrigin", err)
			}
		})
	}
}
