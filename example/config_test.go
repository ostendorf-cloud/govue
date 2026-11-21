// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
	"github.com/ostendorf-cloud/govue/logging"
)

func TestDecodeKeyKeepsTheRawValueOut(t *testing.T) {
	t.Parallel()

	// Built at run time so the constant is not a secret-looking literal. The
	// letters s, k and z are not hex digits.
	secret := strings.Repeat("s", 2) + "ecret" + strings.Repeat("k", 6) + "z"

	for _, tc := range []struct{ name, raw string }{
		{"not hex", secret},
		{"hex then junk", strings.Repeat("ab", 40) + secret},
		{"junk then hex", secret + strings.Repeat("ab", 40)},
		{"odd length", strings.Repeat("a", 63)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := decodeKey("SESSION_KEY", tc.raw)
			if err == nil {
				t.Fatalf("decodeKey = %x, want an error", key)
			}
			for _, form := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(form, secret) || strings.Contains(form, tc.raw) {
					t.Errorf("error carries the raw value: %s", form)
				}
				// The error of hex.DecodeString names the offending byte.
				if strings.Contains(form, "invalid byte") || strings.Contains(form, "U+") {
					t.Errorf("error carries the decoder error: %s", form)
				}
			}
			if !strings.Contains(err.Error(), "SESSION_KEY") {
				t.Errorf("error does not name the variable: %v", err)
			}
			if !strings.Contains(err.Error(), "not valid hex") || !strings.Contains(err.Error(), "32 bytes") {
				t.Errorf("error does not name the expected form and length: %v", err)
			}
		})
	}

	t.Run("a valid key decodes", func(t *testing.T) {
		t.Parallel()

		key, err := decodeKey("SESSION_KEY", strings.Repeat("ab", minKeyBytes))
		if err != nil || len(key) != minKeyBytes {
			t.Fatalf("decodeKey = %d bytes, %v", len(key), err)
		}
	})
}

// A refused ALLOWED_ORIGINS entry is named by the variable and its position,
// never by its value, in every package the list reaches.
func TestNewAppAllowedOriginsKeepsTheValueOut(t *testing.T) {
	t.Parallel()

	env := testEnv(t, 20)
	env["ALLOWED_ORIGINS"] = "https://app.example.test,s3cr3tTOKEN"
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	_, err = newApp(cfg, logging.New(&strings.Builder{}, cfg.logLevel))
	configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "ALLOWED_ORIGINS entry 2")
}

// A wrong TRUSTED_PROXIES entry is named by the variable and its position,
// never by its value.
func TestLoadConfigTrustedProxiesKeepsTheValueOut(t *testing.T) {
	t.Parallel()

	env := testEnv(t, 20)
	env["TRUSTED_PROXIES"] = "10.0.0.0/8, https://user:s3cr3tTOKEN@proxy.example.test"
	_, err := loadConfig(func(key string) string { return env[key] })
	configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "TRUSTED_PROXIES")
	if err != nil && !strings.Contains(err.Error(), "entry 2") {
		t.Errorf("error does not name the position: %v", err)
	}
}
