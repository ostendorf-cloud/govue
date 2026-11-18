// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/appassoc"
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
	addr      string
	adminAddr string
	logLevel  slog.Level

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

	// siteDir is the output directory of the prerender step. Empty means the
	// example serves no language pages and no redirect at /.
	siteDir string

	// assoc is the content of the app association files. It is empty unless
	// APPLE_APP_ID or ANDROID_PACKAGE is set, and an empty one mounts no
	// association file.
	assoc appassoc.Config

	// now is the clock of the session and device stores. It is not read
	// from the environment. Nil is time.Now, and tests replace it.
	now func() time.Time
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
		// Separate from addr on purpose: see (*app).adminHandler and
		// docs/concepts/govue.md for why liveness, readiness and metrics
		// live on their own listener, reached only inside the cluster,
		// never through this application's Ingress. Set to "" to disable
		// it and fall back to the single listener httpserver.Run serves,
		// which is what an application that does not want this gets by
		// simply not setting ADMIN_ADDR.
		adminAddr: l.String("ADMIN_ADDR", ":8081"),
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

	cfg.siteDir = l.String("SITE_DIR", "")

	cfg.assoc, err = loadAssoc(l)
	fail(err)

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return config{}, fmt.Errorf("example: load config: %w", err)
	}
	return cfg, nil
}

// loadAssoc reads the app association settings. Both files are off by
// default. The example takes one app per platform, a product with more fills
// appassoc.Config itself. The values are checked by appassoc.New in newApp.
//
//	APPLE_APP_ID            TeamID.BundleID, turns the Apple file on
//	APPLE_APP_PATHS         comma separated path patterns the app opens
//	APPLE_APP_EXCLUDE_PATHS comma separated patterns it does not open
//	APPLE_WEBCREDENTIALS    true lists the app under webcredentials
//	ANDROID_PACKAGE         application id, turns the Android file on
//	ANDROID_CERT_SHA256     comma separated SHA-256 certificate fingerprints
//	ANDROID_LOGIN_CREDS     true adds get_login_creds
func loadAssoc(l *envconf.Loader) (appassoc.Config, error) {
	var cfg appassoc.Config

	if id := l.String("APPLE_APP_ID", ""); id != "" {
		team, bundle, ok := strings.Cut(id, ".")
		if !ok {
			return appassoc.Config{}, errors.New("APPLE_APP_ID: must be TeamID.BundleID")
		}
		appleID := appassoc.AppleID{TeamID: team, BundleID: bundle}
		cfg.Apple.AppLinks = []appassoc.AppleApp{{
			AppleID:      appleID,
			Paths:        l.List("APPLE_APP_PATHS"),
			ExcludePaths: l.List("APPLE_APP_EXCLUDE_PATHS"),
		}}
		if l.Bool("APPLE_WEBCREDENTIALS", false) {
			cfg.Apple.WebCredentials = []appassoc.AppleID{appleID}
		}
	}

	if pkg := l.String("ANDROID_PACKAGE", ""); pkg != "" {
		app := appassoc.AndroidApp{PackageName: pkg, Fingerprints: l.List("ANDROID_CERT_SHA256")}
		if l.Bool("ANDROID_LOGIN_CREDS", false) {
			app.Relations = []appassoc.Relation{appassoc.RelationHandleAllURLs, appassoc.RelationGetLoginCreds}
		}
		cfg.Android.Apps = []appassoc.AndroidApp{app}
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
