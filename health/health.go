// Package health answers the two probes a load balancer or an orchestrator
// asks of a running process: is it alive, and may it currently receive
// traffic.
//
// The two questions have different failure modes, which is why they are
// separate. Liveness answers whether the process itself is stuck, so it
// checks nothing external: a dependency being down is not a reason to
// restart a process that would come back up and find the same dependency
// still down. Readiness answers whether the process should receive traffic
// right now, so it runs the registered checks and also fails the moment
// draining starts, which is what lets a caller stop sending new requests
// before the process stops answering them at all. This package makes no
// assumption about what calls it or what happens after a probe fails; it
// only answers the two questions honestly.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTimeout bounds a check that was registered without one.
const DefaultTimeout = 2 * time.Second

// Check probes one dependency and reports whether it is usable. Readiness
// calls every registered Check on each request, so a Check stays cheap: a
// ping, not a query.
type Check func(ctx context.Context) error

// namedCheck pairs a registered Check with the name it reports under and the
// timeout it runs with.
type namedCheck struct {
	name    string
	timeout time.Duration
	run     Check
}

// Registry holds the registered readiness checks and the draining flag. The
// zero value is not usable; build one with New.
type Registry struct {
	mu       sync.RWMutex
	checks   []namedCheck
	draining atomic.Bool
}

// New returns an empty Registry. A Registry with no checks still answers
// readiness, based on the draining flag alone.
func New() *Registry {
	return &Registry{}
}

// Add registers a check under name. A timeout that is zero or negative is
// replaced with DefaultTimeout. name shows up in the readiness response, so
// it names the dependency the check talks to: "database", "object storage".
func (r *Registry) Add(name string, timeout time.Duration, check Check) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, namedCheck{name: name, timeout: timeout, run: check})
}

// Drain marks the registry as draining. Readiness answers unready from this
// point on, regardless of what the checks report, which lets a caller stop
// sending new traffic before it stops the process.
func (r *Registry) Drain() {
	r.draining.Store(true)
}

// Draining reports whether Drain has been called.
func (r *Registry) Draining() bool {
	return r.draining.Load()
}

// Live is the liveness handler. It always answers 200: liveness reports that
// the process is up, nothing about whether its dependencies are, and nothing
// about whether it is draining.
func (r *Registry) Live(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, readyBody{Status: "ok"})
}

// Ready is the readiness handler. While draining it answers 503 without
// running any check. Otherwise it runs every registered check in parallel,
// each bounded by its own timeout, and answers 503 if any one of them fails,
// naming the failed checks but never their underlying error text: that text
// belongs in a log line this package does not write.
func (r *Registry) Ready(w http.ResponseWriter, req *http.Request) {
	if r.Draining() {
		writeStatus(w, http.StatusServiceUnavailable, readyBody{Status: "draining"})
		return
	}

	r.mu.RLock()
	checks := make([]namedCheck, len(r.checks))
	copy(checks, r.checks)
	r.mu.RUnlock()

	failures := r.runChecks(req.Context(), checks)
	if len(failures) > 0 {
		writeStatus(w, http.StatusServiceUnavailable, readyBody{Status: "unavailable", Checks: failures})
		return
	}
	writeStatus(w, http.StatusOK, readyBody{Status: "ready"})
}

// runChecks runs every check in checks concurrently, each under its own
// timeout derived from ctx, and returns the name and classification of every
// one that did not pass.
func (r *Registry) runChecks(ctx context.Context, checks []namedCheck) map[string]string {
	failures := make(map[string]string, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range checks {
		wg.Add(1)
		go func(c namedCheck) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()

			err := c.run(checkCtx)
			if err == nil {
				return
			}
			reason := "failed"
			if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
				reason = "timeout"
			}
			mu.Lock()
			failures[c.name] = reason
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	return failures
}

// Handle registers the liveness and readiness endpoints on mux, at GET
// /healthz and GET /readyz.
func (r *Registry) Handle(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", r.Live)
	mux.HandleFunc("GET /readyz", r.Ready)
}

// readyBody is the one response shape both endpoints write. Checks holds
// only the checks that did not pass, by name, each classified as "failed" or
// "timeout": never the error that caused it.
type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// writeStatus writes body as JSON with the given status code. The response
// is never cached, since a cached probe answer would report a state that has
// since changed.
func writeStatus(w http.ResponseWriter, code int, body readyBody) {
	payload, err := json.Marshal(body)
	if err != nil {
		// body holds only strings and a map of strings, so this cannot
		// happen in practice; an empty 500 is still a safer fallback than a
		// panic on a probe endpoint.
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(payload)
}
