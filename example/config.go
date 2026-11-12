// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/envconf"
	"github.com/ostendorf-cloud/govue/logging"
)

// minKeyBytes is the shortest decoded key config accepts for a signing
// secret, matching session.MinKeyBytes: a key shorter than this is a
// deployment mistake, not a value to run with anyway.
const minKeyBytes = 32

// config is every setting this example reads from the environment.
type config struct {
	addr     string
	logLevel slog.Level

	trustedProxies []netip.Prefix

	sessionKey []byte
	csrfKey    []byte

	oidcIssuer       string
	oidcClientID     string
	oidcClientSecret string
	oidcRedirectURL  string

	allowedOrigins []string

	rateLimitBurst int
	rateLimitPer   time.Duration

	allowPlainHTTP bool
}

// loadConfig reads every setting through envconf, so a misconfigured
// deployment reports every mistake on the first start instead of one per
// restart.
func loadConfig(getenv func(string) string) (config, error) {
	l := envconf.New(getenv)

	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	cfg := config{
		addr: l.String("ADDR", ":8080"),
	}

	level, err := logging.Level(l.String("LOG_LEVEL", "info"))
	fail(err)
	cfg.logLevel = level

	proxies, err := clientip.ParseCIDRs(l.String("TRUSTED_PROXIES", ""))
	fail(err)
	cfg.trustedProxies = proxies

	cfg.sessionKey, err = decodeKey("SESSION_KEY", l.Secret("SESSION_KEY"))
	fail(err)
	cfg.csrfKey, err = decodeKey("CSRF_KEY", l.Secret("CSRF_KEY"))
	fail(err)

	cfg.oidcIssuer = l.Required("OIDC_ISSUER")
	cfg.oidcClientID = l.Required("OIDC_CLIENT_ID")
	cfg.oidcClientSecret = l.Required("OIDC_CLIENT_SECRET")
	if redirectURL := l.URL("OIDC_REDIRECT_URL", ""); redirectURL != nil {
		cfg.oidcRedirectURL = redirectURL.String()
	}

	cfg.allowedOrigins = l.List("ALLOWED_ORIGINS")

	cfg.rateLimitBurst = l.Int("RATE_LIMIT_BURST", 20, 0, 100_000)
	cfg.rateLimitPer = l.Duration("RATE_LIMIT_PER", time.Minute, time.Second, time.Hour)

	cfg.allowPlainHTTP = l.Bool("ALLOW_PLAIN_HTTP", false)

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return config{}, fmt.Errorf("example: load config: %w", err)
	}
	return cfg, nil
}

// decodeKey decodes raw as hex and checks its length. An empty raw means
// envconf.Secret already recorded why: a missing variable or an unreadable
// file. Reporting it a second time here would only repeat the same problem
// under a different message.
func decodeKey(name, raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // envconf.Secret already recorded the problem for an empty value
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: must be hex encoded: %w", name, err)
	}
	if len(key) < minKeyBytes {
		return nil, fmt.Errorf("%s: decodes to %d bytes, at least %d are required", name, len(key), minKeyBytes)
	}
	return key, nil
}
