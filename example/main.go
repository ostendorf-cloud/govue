// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Command example wires every package in this module into one running
// server. This repository ships no binary and this is not an exception to
// that: nothing imports a main package, and this one is never released.
// Its purpose is to be the one place the canonical order of a middleware
// chain is actually assembled and kept correct, rather than living only in
// a document that can drift from the packages it describes. See
// (*app).handler for the two chains and why each position is forced, and
// docs/concepts/web-kit.md for the reasoning.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ostendorf-cloud/web-kit/httpserver"
	"github.com/ostendorf-cloud/web-kit/logging"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return httpserver.Run(ctx, httpserver.Config{
		Addr:    cfg.addr,
		Handler: a.handler(),
		Logger:  logger,
		// Failing the readiness probe before the drain delay starts is
		// what lets a load balancer stop sending new requests before the
		// server stops accepting connections at all.
		BeforeShutdown: a.health.Drain,
	})
}
