// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// safetyNet bounds a select that should resolve almost instantly. A bug
// turns a hang into a timeout instead.
const safetyNet = 10 * time.Second

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// memListener is a net.Listener backed by net.Pipe pairs instead of a real
// socket, so the tests need no bound port and run inside a sandbox that may
// not allow one.
type memListener struct {
	pending chan net.Conn
	closed  chan struct{}
	once    sync.Once
}

func newMemListener() *memListener {
	return &memListener{pending: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.pending:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return fakeAddr{} }

// isClosed reports whether Close has already run, without blocking.
func (l *memListener) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// dial creates one pipe pair, hands the server end to a pending Accept and
// returns the client end to the caller.
func (l *memListener) dial(ctx context.Context) (net.Conn, error) {
	serverSide, clientSide := net.Pipe()
	select {
	case l.pending <- serverSide:
		return clientSide, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type fakeAddr struct{}

func (fakeAddr) Network() string { return "memory" }
func (fakeAddr) String() string  { return "memory" }

// httpClientFor builds a client that dials through the listener's pipes,
// with keep-alives disabled so a finished request leaves no idle connection
// for a shutdown to wait on.
func httpClientFor(l *memListener) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return l.dial(ctx) },
			DisableKeepAlives: true,
		},
	}
}

func getBody(t *testing.T, c *http.Client, path string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://memory"+path, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestRunRejectsAnEmptyConfig(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), Config{})
	if err == nil {
		t.Fatal("Run accepted an empty config")
	}
	for _, want := range []string{"addr", "handler", "logger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestServeRejectsAnEmptyConfigWithoutDemandingAnAddr(t *testing.T) {
	t.Parallel()

	listener := newMemListener()
	defer func() { _ = listener.Close() }()

	err := Serve(context.Background(), Config{}, listener)
	if err == nil {
		t.Fatal("Serve accepted an empty config")
	}
	for _, want := range []string{"handler", "logger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "addr") {
		t.Errorf("error %q demands an addr, which a listener makes unnecessary", err)
	}
}

func TestRunWrapsAListenFailure(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), Config{
		Addr:    "127.0.0.1:99999",
		Logger:  discardLogger(),
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("Run = %v, want a wrapped listen error", err)
	}
}

func TestServeAnswersARequestAndShutsDownCleanly(t *testing.T) {
	t.Parallel()

	listener := newMemListener()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	beforeShutdownCalled := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, Config{
			Logger: discardLogger(),
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("hello"))
			}),
			DrainDelay:     10 * time.Millisecond,
			BeforeShutdown: func() { close(beforeShutdownCalled) },
		}, listener)
	}()

	if body := getBody(t, httpClientFor(listener), "/"); body != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}

	stop()

	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("Serve = %v, want nil for a clean shutdown", err)
		}
	case <-time.After(safetyNet):
		t.Fatal("Serve did not return after the context was cancelled")
	}

	select {
	case <-beforeShutdownCalled:
	default:
		t.Error("BeforeShutdown was never called")
	}
}

func TestServeLetsARequestInFlightFinish(t *testing.T) {
	t.Parallel()

	listener := newMemListener()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	requestStarted := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, Config{
			Logger: discardLogger(),
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/slow" {
					return
				}
				close(requestStarted)
				// Longer than the drain delay below, so a correct shutdown
				// has to wait for this handler instead of cutting it off.
				time.Sleep(150 * time.Millisecond)
				_, _ = w.Write([]byte("finished"))
			}),
			DrainDelay: 10 * time.Millisecond,
		}, listener)
	}()

	client := httpClientFor(listener)
	answer := make(chan string, 1)
	go func() { answer <- getBody(t, client, "/slow") }()

	<-requestStarted
	stop()

	select {
	case body := <-answer:
		if body != "finished" {
			t.Errorf("body = %q, want the handler's full answer", body)
		}
	case <-time.After(safetyNet):
		t.Fatal("the request in flight was never answered")
	}

	if err := <-serveDone; err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
}

func TestServeSurfacesAListenerFailure(t *testing.T) {
	t.Parallel()

	listener := newMemListener()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := Serve(context.Background(), Config{
		Logger:  discardLogger(),
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}, listener)
	if err == nil {
		t.Fatal("Serve returned nil for an already closed listener")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Serve = %v, want the real listener error, not ErrServerClosed", err)
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
		want time.Duration
		get  func(Config) time.Duration
	}{
		{"read header timeout", Config{}, DefaultReadHeaderTimeout, func(c Config) time.Duration { return c.ReadHeaderTimeout }},
		{"read timeout", Config{}, DefaultReadTimeout, func(c Config) time.Duration { return c.ReadTimeout }},
		{"write timeout", Config{}, DefaultWriteTimeout, func(c Config) time.Duration { return c.WriteTimeout }},
		{"idle timeout", Config{}, DefaultIdleTimeout, func(c Config) time.Duration { return c.IdleTimeout }},
		{"shutdown timeout", Config{}, DefaultShutdownTimeout, func(c Config) time.Duration { return c.ShutdownTimeout }},
		{"drain delay", Config{}, DefaultDrainDelay, func(c Config) time.Duration { return c.DrainDelay }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg
			cfg.applyDefaults()
			if got := tt.get(cfg); got != tt.want {
				t.Errorf("default = %s, want %s", got, tt.want)
			}
		})
	}

	t.Run("max header bytes", func(t *testing.T) {
		t.Parallel()
		cfg := Config{}
		cfg.applyDefaults()
		if cfg.MaxHeaderBytes != DefaultMaxHeaderBytes {
			t.Errorf("MaxHeaderBytes = %d, want %d", cfg.MaxHeaderBytes, DefaultMaxHeaderBytes)
		}
	})

	t.Run("an explicit value is left alone", func(t *testing.T) {
		t.Parallel()
		cfg := Config{ReadTimeout: time.Second}
		cfg.applyDefaults()
		if cfg.ReadTimeout != time.Second {
			t.Errorf("ReadTimeout = %s, want the configured value unchanged", cfg.ReadTimeout)
		}
	})
}

// echoHandler answers every request with its own name in the body, so a
// test can tell which of the two listeners actually served a response.
func echoHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(name))
	})
}

func TestRunAdminWithNoAdminAddrBehavesLikeRun(t *testing.T) {
	t.Parallel()

	// Both calls fail at Config validation, with no network touched at all,
	// which is what lets this test run in a sandbox with no socket: the
	// comparison is only meaningful because neither side ever reaches
	// net.Listen.
	runErr := Run(context.Background(), Config{})
	runAdminErr := RunAdmin(context.Background(), Config{}, AdminConfig{})

	if runErr == nil || runAdminErr == nil {
		t.Fatalf("Run = %v, RunAdmin = %v, want both to reject an empty config", runErr, runAdminErr)
	}
	if runErr.Error() != runAdminErr.Error() {
		t.Errorf("RunAdmin with no admin address = %q, want exactly Run's own error %q", runAdminErr, runErr)
	}
}

func TestRunAdminWrapsAPrimaryListenFailureWithoutTouchingAdmin(t *testing.T) {
	t.Parallel()

	// 99999 is not a valid port, so this fails at address parsing before any
	// real socket is touched, the same trick TestRunWrapsAListenFailure
	// uses for Run. admin.Addr is equally invalid, proving it is never
	// reached: RunAdmin fails on the primary address first.
	err := RunAdmin(context.Background(), Config{
		Addr:    "127.0.0.1:99999",
		Logger:  discardLogger(),
		Handler: echoHandler("primary"),
	}, AdminConfig{
		Addr:    "127.0.0.1:99998",
		Handler: echoHandler("admin"),
	})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("RunAdmin = %v, want a wrapped listen error", err)
	}
	if strings.Contains(err.Error(), "admin") {
		t.Errorf("RunAdmin = %v, named the admin listener, which was never reached", err)
	}
}

func TestRunAdminRejectsAnInvalidAdminConfigWithoutTouchingTheNetwork(t *testing.T) {
	t.Parallel()

	// admin.Addr is set but admin.Handler is not, which RunAdmin has to
	// catch before it ever calls net.Listen for either address: cfg.Addr
	// below is not a real address at all, and would fail if it were ever
	// dialled.
	err := RunAdmin(context.Background(), Config{
		Addr:    "this is not a valid address",
		Logger:  discardLogger(),
		Handler: echoHandler("primary"),
	}, AdminConfig{
		Addr: "127.0.0.1:99998",
	})
	if err == nil {
		t.Fatal("RunAdmin accepted an admin config with no handler")
	}
	for _, want := range []string{"admin", "handler"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestRunAdminClosesThePrimaryListenerWhenTheAdminListenFails(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	wantErr := errors.New("admin: address already in use")

	var calls int
	listen := func(_ context.Context, _ string) (net.Listener, error) {
		calls++
		if calls == 1 {
			return primary, nil
		}
		return nil, wantErr
	}

	err := runAdmin(context.Background(), Config{
		Addr:    "primary.invalid:0",
		Logger:  discardLogger(),
		Handler: echoHandler("primary"),
	}, AdminConfig{
		Addr:    "admin.invalid:0",
		Handler: echoHandler("admin"),
	}, listen)

	if err == nil {
		t.Fatal("runAdmin accepted a listen failure on the admin address")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("runAdmin = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Errorf("runAdmin = %v, does not name the admin listener", err)
	}
	if !primary.isClosed() {
		t.Error("runAdmin left the primary listener open after the admin listen failed")
	}
}

func TestServeAdminAnswersOnBothListenersAndShutsDownCleanly(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	admin := newMemListener()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var beforeShutdownCalls atomic.Int32
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeAdmin(ctx, Config{
			Logger:         discardLogger(),
			Handler:        echoHandler("primary"),
			DrainDelay:     10 * time.Millisecond,
			BeforeShutdown: func() { beforeShutdownCalls.Add(1) },
		}, primary, AdminConfig{
			Handler: echoHandler("admin"),
		}, admin)
	}()

	if body := getBody(t, httpClientFor(primary), "/"); body != "primary" {
		t.Errorf("primary body = %q, want %q", body, "primary")
	}
	if body := getBody(t, httpClientFor(admin), "/metrics"); body != "admin" {
		t.Errorf("admin body = %q, want %q", body, "admin")
	}

	stop()

	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("ServeAdmin = %v, want nil for a clean shutdown", err)
		}
	case <-time.After(safetyNet):
		t.Fatal("ServeAdmin did not return after the context was cancelled")
	}

	if got := beforeShutdownCalls.Load(); got != 1 {
		t.Errorf("BeforeShutdown was called %d times, want exactly 1", got)
	}
}

func TestServeAdminDefaultsTheAdminLoggerToThePrimarys(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	admin := newMemListener()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	serveDone := make(chan error, 1)
	go func() {
		// admin.Logger is left nil on purpose: Serve would reject that as a
		// missing required field, so a nil Logger reaching the admin server
		// unchanged would fail this test as soon as it tries to log
		// anything, rather than silently falling back to the primary's.
		serveDone <- ServeAdmin(ctx, Config{
			Logger:     discardLogger(),
			Handler:    echoHandler("primary"),
			DrainDelay: 10 * time.Millisecond,
		}, primary, AdminConfig{
			Handler: echoHandler("admin"),
		}, admin)
	}()

	if body := getBody(t, httpClientFor(admin), "/metrics"); body != "admin" {
		t.Errorf("admin body = %q, want %q", body, "admin")
	}

	stop()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("ServeAdmin = %v, want nil", err)
		}
	case <-time.After(safetyNet):
		t.Fatal("ServeAdmin did not return after the context was cancelled")
	}
}

func TestServeAdminRejectsAnInvalidAdminConfig(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	defer func() { _ = primary.Close() }()
	admin := newMemListener()
	defer func() { _ = admin.Close() }()

	err := ServeAdmin(context.Background(), Config{
		Logger:  discardLogger(),
		Handler: echoHandler("primary"),
	}, primary, AdminConfig{}, admin)
	if err == nil {
		t.Fatal("ServeAdmin accepted an admin config with no handler")
	}
	for _, want := range []string{"admin", "handler"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestServeAdminSurfacesAPrimaryFailureAndStopsAdmin(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	if err := primary.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	admin := newMemListener()
	defer func() { _ = admin.Close() }()

	done := make(chan error, 1)
	go func() {
		done <- ServeAdmin(context.Background(), Config{
			Logger:  discardLogger(),
			Handler: echoHandler("primary"),
		}, primary, AdminConfig{
			Handler: echoHandler("admin"),
		}, admin)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ServeAdmin returned nil for an already closed primary listener")
		}
		if errors.Is(err, http.ErrServerClosed) {
			t.Errorf("ServeAdmin = %v, want the real listener error, not ErrServerClosed", err)
		}
	case <-time.After(safetyNet):
		t.Fatal("ServeAdmin did not return once the primary listener failed: the admin goroutine leaked")
	}
}

func TestServeAdminSurfacesAnAdminFailureAndStopsPrimary(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	defer func() { _ = primary.Close() }()
	admin := newMemListener()
	if err := admin.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- ServeAdmin(context.Background(), Config{
			Logger:  discardLogger(),
			Handler: echoHandler("primary"),
		}, primary, AdminConfig{
			Handler: echoHandler("admin"),
		}, admin)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ServeAdmin returned nil for an already closed admin listener")
		}
		if errors.Is(err, http.ErrServerClosed) {
			t.Errorf("ServeAdmin = %v, want the real listener error, not ErrServerClosed", err)
		}
	case <-time.After(safetyNet):
		t.Fatal("ServeAdmin did not return once the admin listener failed: the primary goroutine leaked")
	}
}

func TestServeAdminLetsARequestInFlightOnTheAdminListenerFinish(t *testing.T) {
	t.Parallel()

	primary := newMemListener()
	admin := newMemListener()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	requestStarted := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeAdmin(ctx, Config{
			Logger:     discardLogger(),
			Handler:    echoHandler("primary"),
			DrainDelay: 10 * time.Millisecond,
		}, primary, AdminConfig{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/slow" {
					return
				}
				close(requestStarted)
				// Longer than the drain delay above, so a correct shutdown
				// has to let this admin-side handler finish rather than
				// cutting it off the moment the primary begins to drain.
				time.Sleep(150 * time.Millisecond)
				_, _ = w.Write([]byte("finished"))
			}),
		}, admin)
	}()

	client := httpClientFor(admin)
	answer := make(chan string, 1)
	go func() { answer <- getBody(t, client, "/slow") }()

	<-requestStarted
	stop()

	select {
	case body := <-answer:
		if body != "finished" {
			t.Errorf("body = %q, want the handler's full answer", body)
		}
	case <-time.After(safetyNet):
		t.Fatal("the request in flight on the admin listener was never answered")
	}

	if err := <-serveDone; err != nil {
		t.Errorf("ServeAdmin = %v, want nil", err)
	}
}

// outlivingHandler answers / immediately, and /slow only after sleeping well
// past the ShutdownTimeout the tests below configure: a request to /slow
// still in flight when shutdown starts is still running once srv.Shutdown's
// own deadline expires, which is what makes Shutdown itself return an error
// instead of the listener merely failing to Serve in the first place.
func outlivingHandler(name string, started chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			time.Sleep(200 * time.Millisecond)
		}
		_, _ = w.Write([]byte(name))
	})
}

func TestServeAdminJoinsErrorsWhenShutdownItselfFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		primarySlow bool
		adminSlow   bool
		wantLabels  []string
	}{
		{"shutdown fails on the primary listener alone", true, false, []string{"primary"}},
		{"shutdown fails on the admin listener alone", false, true, []string{"admin"}},
		{"shutdown fails on both listeners", true, true, []string{"primary", "admin"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			primary := newMemListener()
			admin := newMemListener()
			ctx, stop := context.WithCancel(context.Background())
			defer stop()

			primaryStarted := make(chan struct{})
			adminStarted := make(chan struct{})

			serveDone := make(chan error, 1)
			go func() {
				serveDone <- ServeAdmin(ctx, Config{
					Logger:          discardLogger(),
					Handler:         outlivingHandler("primary", primaryStarted),
					DrainDelay:      5 * time.Millisecond,
					ShutdownTimeout: 20 * time.Millisecond,
				}, primary, AdminConfig{
					Handler: outlivingHandler("admin", adminStarted),
				}, admin)
			}()

			// requestDone collects the slow requests this test starts, so it
			// can wait for both of them to actually finish before it ends,
			// rather than leaving a handler goroutine running past the test.
			var requestDone sync.WaitGroup
			if tt.primarySlow {
				requestDone.Add(1)
				go func() {
					defer requestDone.Done()
					_ = getBody(t, httpClientFor(primary), "/slow")
				}()
				<-primaryStarted
			}
			if tt.adminSlow {
				requestDone.Add(1)
				go func() {
					defer requestDone.Done()
					_ = getBody(t, httpClientFor(admin), "/slow")
				}()
				<-adminStarted
			}

			stop()

			var err error
			select {
			case err = <-serveDone:
			case <-time.After(safetyNet):
				t.Fatal("ServeAdmin did not return once shutdown itself timed out")
			}

			if err == nil {
				t.Fatal("ServeAdmin = nil, want an error naming the listener whose shutdown timed out")
			}
			for _, label := range tt.wantLabels {
				if !strings.Contains(err.Error(), label+":") {
					t.Errorf("error %q does not name %q", err, label)
				}
			}

			joined, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // asserting the concrete shape errors.Join returns is the point of this check
			if !ok {
				t.Fatalf("error %q is not the result of errors.Join", err)
			}
			if got, want := len(joined.Unwrap()), len(tt.wantLabels); got != want {
				t.Errorf("joined error carries %d causes, want %d: %v", got, want, err)
			}

			done := make(chan struct{})
			go func() {
				requestDone.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(safetyNet):
				t.Fatal("a slow request never finished")
			}
		})
	}
}
