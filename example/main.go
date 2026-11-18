// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Command example wires every package in this module into one running
// server. This repository ships no binary and this is not an exception to
// that: nothing imports a main package, and this one is never released.
// Its purpose is to be the one place the canonical order of a middleware
// chain is actually assembled and kept correct, rather than living only in
// a document that can drift from the packages it describes. See
// (*app).handler for the two chains and why each position is forced,
// (*app).adminHandler for the second listener health and metrics moved to,
// and docs/concepts/govue.md for the reasoning behind both.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.logLevel)

	a, err := newApp(cfg, logger)
	if err != nil {
		return err
	}
	// The site directory stays open while the server runs, because assets
	// are read per request.
	defer func() {
		if err := a.close(); err != nil {
			logger.Error("close the site directory", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The schema is applied next to the server, not before it: a database
	// that starts after the pod is the normal case, and readiness stays at
	// 503 until it is done. A broken set of migration files ends the loop at
	// once and is logged, readiness then stays at 503.
	if a.db != nil {
		go func() {
			if err := a.db.MigrateUntilReady(ctx, logger, migrationFiles, migrationDir); err != nil && ctx.Err() == nil {
				logger.ErrorContext(ctx, "apply the database schema", "error", err.Error())
			}
		}()
	}

	return httpserver.RunAdmin(ctx, httpserver.Config{
		Addr:    cfg.addr,
		Handler: a.handler(),
		Logger:  logger,
		// Failing the readiness probe before the drain delay starts is
		// what lets a load balancer stop sending new requests before the
		// server stops accepting connections at all. This runs once, for
		// both listeners, however many are running.
		BeforeShutdown: a.health.Drain,
	}, httpserver.AdminConfig{
		Addr:    cfg.adminAddr,
		Handler: a.adminHandler(),
	})
}
