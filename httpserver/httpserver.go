// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package httpserver runs an [http.Server] with explicit timeouts and a
// graceful shutdown sequence.
//
// Every timeout is set explicitly, because the zero value means none: a
// single slow or idle client would otherwise hold a connection, and the
// handler goroutine serving it, open forever.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Defaults applied by Config.applyDefaults to any zero-value field. An app
// that needs a different value sets the field explicitly.
const (
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 15 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
	DefaultMaxHeaderBytes    = 64 << 10
	// DefaultDrainDelay is the gap between running BeforeShutdown and closing
	// the listener, so a load balancer has time to notice a failing readiness
	// check and stop sending new requests before they are refused.
	DefaultDrainDelay = 2 * time.Second
	// DefaultShutdownTimeout bounds how long requests already in flight may
	// take to finish once shutdown starts, drain delay not included.
	DefaultShutdownTimeout = 20 * time.Second
)

// Config describes the server to run. Handler and Logger are always
// required. Addr is required only for Run; a caller using Serve already has
// a listener. Every other field has a default applied when left at its zero
// value.
type Config struct {
	Addr    string
	Handler http.Handler
	Logger  *slog.Logger

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int

	// ShutdownTimeout bounds the call to http.Server.Shutdown.
	ShutdownTimeout time.Duration
	// DrainDelay is waited after BeforeShutdown runs and before the server
	// stops accepting new connections.
	DrainDelay time.Duration
	// BeforeShutdown, when non-nil, runs once before the drain delay starts.
	// It gives the caller a place to fail its own readiness check.
	BeforeShutdown func()
}

// applyDefaults fills every zero-value field with its documented default.
func (c *Config) applyDefaults() {
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = DefaultReadTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.MaxHeaderBytes == 0 {
		c.MaxHeaderBytes = DefaultMaxHeaderBytes
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = DefaultShutdownTimeout
	}
	if c.DrainDelay == 0 {
		c.DrainDelay = DefaultDrainDelay
	}
}

// validate reports every missing required field at once. needAddr is false
// for Serve, which takes a listener instead of an address.
func (c Config) validate(needAddr bool) error {
	var problems []error
	if needAddr && c.Addr == "" {
		problems = append(problems, errors.New("addr is required"))
	}
	if c.Handler == nil {
		problems = append(problems, errors.New("handler is required"))
	}
	if c.Logger == nil {
		problems = append(problems, errors.New("logger is required"))
	}
	return errors.Join(problems...)
}

// Run opens a TCP listener on cfg.Addr and serves on it until ctx is
// cancelled. It is the entry point for an app that does not already have a
// listener; Serve is for one that does.
func Run(ctx context.Context, cfg Config) error {
	cfg.applyDefaults()
	if err := cfg.validate(true); err != nil {
		return fmt.Errorf("http server: %w", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("http server: listen: %w", err)
	}
	return Serve(ctx, cfg, listener)
}

// Serve runs an HTTP server on listener until ctx is cancelled, then shuts it
// down: run BeforeShutdown, wait the drain delay, stop accepting new
// connections and let requests already in flight finish within
// ShutdownTimeout. It returns nil for a clean shutdown; a shutdown that ran
// out of time, or a serve failure, is returned as an error.
func Serve(ctx context.Context, cfg Config, listener net.Listener) error {
	cfg.applyDefaults()
	if err := cfg.validate(false); err != nil {
		return fmt.Errorf("http server: %w", err)
	}

	srv := &http.Server{
		Handler:           cfg.Handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		// The server's own error log belongs in the structured log, not on
		// stderr.
		ErrorLog: slog.NewLogLogger(cfg.Logger.Handler(), slog.LevelWarn),
		// Nothing in this package answers a bare OPTIONS *.
		DisableGeneralOptionsHandler: true,
		// A request's context must not be cancelled the instant shutdown
		// starts; it is cancelled instead by Shutdown itself, once the
		// drain delay has passed.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	serveErr := make(chan error, 1)
	go func() {
		cfg.Logger.Info("listening", "addr", listener.Addr().String())
		serveErr <- srv.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	if cfg.BeforeShutdown != nil {
		cfg.BeforeShutdown()
	}

	cfg.Logger.Info("shutting down", "drain_delay", cfg.DrainDelay.String())
	select {
	case <-time.After(cfg.DrainDelay):
	case err := <-serveErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server: shutdown: %w", err)
	}

	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		cfg.Logger.Warn("did not close cleanly", "error", err)
		return err
	}
	cfg.Logger.Info("stopped")
	return nil
}
