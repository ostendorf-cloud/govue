// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/envconf"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/middleware"
)

// frontendCommand is the first argument that runs the example as the
// frontend image of a split site: `example frontend`. Without it the example
// runs as one server with the API, as before.
const frontendCommand = "frontend"

// frontendConfig is what the frontend image reads from the environment. It
// needs no key, no identity provider and no database: it serves the build
// and nothing else.
type frontendConfig struct {
	addr           string
	adminAddr      string
	logLevel       slog.Level
	trustedProxies []netip.Prefix
	allowPlainHTTP bool
	// siteDir is the build: Vite's dist with the prerender written into it.
	siteDir string
}

// loadFrontendConfig reads ADDR, ADMIN_ADDR, LOG_LEVEL, TRUSTED_PROXIES,
// ALLOW_PLAIN_HTTP and SITE_DIR, which is required here.
func loadFrontendConfig(getenv func(string) string) (frontendConfig, error) {
	l := envconf.New(getenv)
	var problems []error
	cfg := frontendConfig{
		addr:      l.String("ADDR", ":8080"),
		adminAddr: l.String("ADMIN_ADDR", ":8081"),
		siteDir:   l.Required("SITE_DIR"),
	}
	level, err := logging.Level(l.String("LOG_LEVEL", "info"))
	if err != nil {
		problems = append(problems, err)
	}
	cfg.logLevel = level
	proxies, err := clientip.ParseCIDRs(l.String("TRUSTED_PROXIES", ""))
	if err != nil {
		problems = append(problems, fmt.Errorf("TRUSTED_PROXIES: %w", err))
	}
	cfg.trustedProxies = proxies
	cfg.allowPlainHTTP = l.Bool("ALLOW_PLAIN_HTTP", false)
	if cfg.adminAddr == "" {
		problems = append(problems, errors.New("ADMIN_ADDR: must not be empty, the probes and the metrics live there"))
	}
	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return frontendConfig{}, fmt.Errorf("example: load frontend config: %w", err)
	}
	return cfg, nil
}

// newFrontend builds the frontend server from the build in cfg.siteDir.
// frontend.New reads the whole build at startup, so the directory is closed
// again before this returns.
func newFrontend(cfg frontendConfig, logger *slog.Logger) (*frontend.Server, error) {
	// OpenRoot, not DirFS: a symbolic link out of the directory is refused.
	root, err := os.OpenRoot(cfg.siteDir)
	if err != nil {
		return nil, siteDirFailure(err)
	}
	defer func() { _ = root.Close() }()

	security := middleware.DefaultSecurity()
	if cfg.allowPlainHTTP {
		// A browser that reached this server over plain HTTP must never be
		// told to upgrade every future visit to HTTPS.
		security.HSTS = 0
	}
	s, err := frontend.New(frontend.Config{
		Build:      root.FS(),
		Language:   language.Config{Languages: siteLanguages},
		ThemeColor: themeColor,
		Security:   &security,
		ClientIP:   clientip.NewResolver(cfg.trustedProxies),
		Logger:     logger,
	})
	if err != nil {
		return nil, fmt.Errorf("example: frontend: %w", err)
	}
	return s, nil
}

// runFrontend runs the example as the frontend image until ctx is
// cancelled: the public listener on ADDR, the probes and the metrics on
// ADMIN_ADDR.
func runFrontend(ctx context.Context, getenv func(string) string) error {
	cfg, err := loadFrontendConfig(getenv)
	if err != nil {
		return err
	}
	logger := logging.New(os.Stdout, cfg.logLevel)
	s, err := newFrontend(cfg, logger)
	if err != nil {
		return err
	}
	return s.Run(ctx, cfg.addr, cfg.adminAddr)
}
