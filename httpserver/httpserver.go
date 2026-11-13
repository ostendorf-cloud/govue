// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package httpserver runs an [http.Server] with explicit timeouts and a
// graceful shutdown sequence.
//
// Every timeout is set explicitly, because the zero value means none: a
// single slow or idle client would otherwise hold a connection, and the
// handler goroutine serving it, open forever.
//
// RunAdmin and ServeAdmin run a second, administrative listener alongside
// the first, for traffic that must never share the application's own
// Ingress route: metrics for a scraper, and anything else reached directly
// inside the cluster rather than from outside it. The two listeners are
// stopped by one cancelled context and drained together. See [AdminConfig]
// for what belongs there, and docs/concepts/govue.md for the reasoning.
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
	// check and stop sending new requests before they are refused. ServeAdmin
	// waits it once, for both listeners together.
	DefaultDrainDelay = 2 * time.Second
	// DefaultShutdownTimeout bounds how long requests already in flight may
	// take to finish once shutdown starts, drain delay not included.
	DefaultShutdownTimeout = 20 * time.Second
)

// Config describes the server to run. Handler and Logger are always
// required. Addr is required only for Run and RunAdmin; a caller using
// Serve or ServeAdmin already has a listener. Every other field has a
// default applied when left at its zero value.
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
// for Serve and ServeAdmin, which take a listener instead of an address.
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
// listener and wants exactly one; Serve is for one that already has a
// listener, and RunAdmin is for one that also wants a second, administrative
// listener.
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

	srv := buildServer(ctx, cfg)
	serveErr := runServer(cfg.Logger, srv, listener)

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

	return shutdownServer(ctx, cfg.Logger, cfg.ShutdownTimeout, listener.Addr().String(), srv, serveErr)
}

// AdminConfig describes the second, administrative listener RunAdmin and
// ServeAdmin run alongside the primary one: metrics for a scraper, and
// anything else that must never reach the same listener, and therefore the
// same Ingress, as the application's own traffic. See docs/concepts/govue.md
// for what belongs here and what does not.
//
// Handler is required whenever this is passed to RunAdmin with Addr set, or
// to ServeAdmin at all. Logger defaults to the primary Config's Logger when
// left nil. The per-connection settings below follow Config's own documented
// defaults.
//
// There is deliberately no ShutdownTimeout, DrainDelay or BeforeShutdown
// here: shutdown is driven once, from the primary Config, so the two
// listeners always drain together instead of two independently configured
// steps that could disagree.
type AdminConfig struct {
	Addr    string
	Handler http.Handler
	Logger  *slog.Logger

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// toConfig builds the Config ServeAdmin runs the admin listener with: this
// AdminConfig's own address, handler and per-connection timeouts, falling
// back to primaryLogger when Logger is left nil. Every shutdown-related
// field stays at its zero value here; ServeAdmin fills those from the
// primary Config instead, never from this one.
func (a AdminConfig) toConfig(primaryLogger *slog.Logger) Config {
	logger := a.Logger
	if logger == nil {
		logger = primaryLogger
	}
	return Config{
		Addr:              a.Addr,
		Handler:           a.Handler,
		Logger:            logger,
		ReadHeaderTimeout: a.ReadHeaderTimeout,
		ReadTimeout:       a.ReadTimeout,
		WriteTimeout:      a.WriteTimeout,
		IdleTimeout:       a.IdleTimeout,
		MaxHeaderBytes:    a.MaxHeaderBytes,
	}
}

// RunAdmin opens a TCP listener on cfg.Addr and, when admin.Addr is not
// empty, a second one on admin.Addr, then runs ServeAdmin on both until ctx
// is cancelled.
//
// An empty admin.Addr runs cfg alone, exactly as Run would, including its
// error behaviour: an application that wants no admin listener needs no
// special case to say so, only to leave AdminConfig unset.
//
// Both configs are validated before either listener is opened, and the
// primary listener is closed again if opening the admin one fails, so a
// misconfigured admin listener never leaves the primary one bound with
// nothing serving it.
func RunAdmin(ctx context.Context, cfg Config, admin AdminConfig) error {
	if admin.Addr == "" {
		return Run(ctx, cfg)
	}

	cfg.applyDefaults()
	if err := cfg.validate(true); err != nil {
		return fmt.Errorf("http server: %w", err)
	}
	adminCfg := admin.toConfig(cfg.Logger)
	adminCfg.applyDefaults()
	if err := adminCfg.validate(true); err != nil {
		return fmt.Errorf("http server: admin: %w", err)
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("http server: listen: %w", err)
	}
	adminListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", adminCfg.Addr)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("http server: admin: listen: %w", err)
	}

	return ServeAdmin(ctx, cfg, listener, admin, adminListener)
}

// ServeAdmin runs cfg on listener and admin on adminListener until ctx is
// cancelled, then shuts both down as one unit: cfg.BeforeShutdown runs
// exactly once, both listeners then wait cfg.DrainDelay concurrently rather
// than one after the other, and both get cfg.ShutdownTimeout to let a
// request already in flight finish, the two budgets running at the same
// time rather than stacking.
//
// A failure on either listener before shutdown starts stops the other
// immediately: its *http.Server is closed and its Serve goroutine is waited
// on before ServeAdmin returns with the one error that actually caused the
// stop, so neither listener, and neither goroutine, is left running because
// its sibling died. A failure once shutdown has started, from either
// listener's own Shutdown call or its Serve goroutine's own exit, is
// reported from both sides through errors.Join instead: by then each
// listener is already stopping on its own terms, so there is no longer a
// healthy sibling to cut short, only two independent results to report
// honestly.
func ServeAdmin(ctx context.Context, cfg Config, listener net.Listener, admin AdminConfig, adminListener net.Listener) error {
	cfg.applyDefaults()
	if err := cfg.validate(false); err != nil {
		return fmt.Errorf("http server: %w", err)
	}
	adminCfg := admin.toConfig(cfg.Logger)
	adminCfg.applyDefaults()
	if err := adminCfg.validate(false); err != nil {
		return fmt.Errorf("http server: admin: %w", err)
	}

	srv := buildServer(ctx, cfg)
	adminSrv := buildServer(ctx, adminCfg)

	serveErr := runServer(cfg.Logger, srv, listener)
	adminServeErr := runServer(adminCfg.Logger, adminSrv, adminListener)

	if err := raceServers(ctx.Done(), srv, adminSrv, serveErr, adminServeErr); err != nil {
		return err
	}

	if cfg.BeforeShutdown != nil {
		cfg.BeforeShutdown()
	}
	cfg.Logger.Info("shutting down", "drain_delay", cfg.DrainDelay.String())
	if err := raceServers(afterDelay(cfg.DrainDelay), srv, adminSrv, serveErr, adminServeErr); err != nil {
		return err
	}

	type shutdownResult struct {
		label string
		err   error
	}
	results := make(chan shutdownResult, 2)
	go func() {
		err := shutdownServer(ctx, cfg.Logger, cfg.ShutdownTimeout, listener.Addr().String(), srv, serveErr)
		results <- shutdownResult{"primary", err}
	}()
	go func() {
		err := shutdownServer(ctx, adminCfg.Logger, cfg.ShutdownTimeout, adminListener.Addr().String(), adminSrv, adminServeErr)
		results <- shutdownResult{"admin", err}
	}()

	var errs []error
	for range 2 {
		if r := <-results; r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.label, r.err))
		}
	}
	return errors.Join(errs...)
}

// raceServers waits for signal, which fires once ctx is done or once a drain
// delay has elapsed depending on the caller, or returns early the moment
// either server fails. A failure on one side closes the other's server
// immediately and waits for its Serve goroutine to actually return, instead
// of leaving it running, or leaving it to be waited on forever, because its
// sibling died first. It returns nil when signal fired first.
func raceServers(signal <-chan struct{}, primary, admin *http.Server, primaryErr, adminErr <-chan error) error {
	select {
	case <-signal:
		return nil
	case err := <-primaryErr:
		_ = admin.Close()
		drain(adminErr) // err below is already the real failure
		return err
	case err := <-adminErr:
		_ = primary.Close()
		drain(primaryErr) // err below is already the real failure
		return err
	}
}

// drain waits for errServed's own Serve goroutine to actually return,
// discarding the result: a sibling forced closed with *http.Server.Close
// above always reports http.ErrServerClosed, which is never the error worth
// reporting once the caller already has the one that caused the stop.
func drain(errServed <-chan error) {
	<-errServed //nolint:errcheck // always http.ErrServerClosed from the Close above, never the error worth reporting
}

// afterDelay returns a channel closed once d has elapsed, the <-chan
// struct{} shape raceServers needs; time.After's own <-chan time.Time
// carries a value raceServers has no use for.
func afterDelay(d time.Duration) <-chan struct{} {
	done := make(chan struct{})
	time.AfterFunc(d, func() { close(done) })
	return done
}

// buildServer constructs the *http.Server Serve and ServeAdmin both run,
// from a cfg that has already had its defaults applied. ctx is the caller's
// own context, used unmodified as the base for every request so a request
// in flight is not cancelled the instant shutdown starts: Shutdown cancels
// it instead, once the drain delay has passed.
func buildServer(ctx context.Context, cfg Config) *http.Server {
	return &http.Server{
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
}

// runServer starts srv serving listener in its own goroutine and returns a
// channel that carries its eventual result exactly once, so a caller can
// select on it without ever blocking on srv.Serve directly.
func runServer(logger *slog.Logger, srv *http.Server, listener net.Listener) <-chan error {
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", listener.Addr().String())
		serveErr <- srv.Serve(listener)
	}()
	return serveErr
}

// shutdownServer stops accepting new connections on srv and lets requests
// already in flight finish within timeout, derived from ctx so the shutdown
// call itself is not cancelled the instant ctx is. addr names the listener
// in the log lines below, which matters once ServeAdmin logs two of them.
func shutdownServer(ctx context.Context, logger *slog.Logger, timeout time.Duration, addr string, srv *http.Server, serveErr <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server: shutdown: %w", err)
	}

	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		logger.Warn("did not close cleanly", "addr", addr, "error", err)
		return err
	}
	logger.Info("stopped", "addr", addr)
	return nil
}
