package middleware

import (
	"net/http"
	"time"

	"github.com/ostendorf-cloud/web-kit/clientip"
	"github.com/ostendorf-cloud/web-kit/logging"
)

// Route returns the route pattern r's handler was registered under, for a
// log line or a metric label that must never carry the raw path: a path
// holds ids and tokens the pattern does not.
//
// http.ServeMux sets r.Pattern while it matches, before it calls the final
// handler, so Route only returns the real pattern once routing has actually
// happened on this *http.Request value. A caller further out that never
// reaches the mux, or a request the mux answered with its own 404, gets
// "unmatched" instead of an empty string.
func Route(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

// SkipHealthChecks is the default AccessLog.Skip: it matches the exact,
// well-known, non-sensitive health check paths a load balancer polls
// constantly, so they do not drown the access log. Comparing against a
// literal path here is control flow, not something written to the log, so
// it does not conflict with logging the route pattern only.
func SkipHealthChecks(r *http.Request) bool {
	return r.URL.Path == "/healthz" || r.URL.Path == "/readyz"
}

// AccessLog writes one structured log line per request. Build it with the
// fields it needs and use its Middleware method; the zero value logs every
// request except the default health check paths, with no client address and
// no user agent.
//
// AccessLog must wrap the mux directly, with nothing that replaces the
// request's context sitting between the two: http.ServeMux records the
// matched pattern on the *http.Request it was handed, and
// (*http.Request).WithContext returns a different *http.Request value, so a
// middleware that calls it between AccessLog and the mux would leave
// AccessLog holding a value the mux never touched. RequestID and Recover
// both call WithContext, so they belong outside AccessLog in the chain, for
// example Chain(mux, Recover(logger), RequestID(logger),
// accessLog.Middleware).
type AccessLog struct {
	// ClientIP resolves the client address to log. Nil omits the client
	// attribute entirely, rather than logging the proxy's own address.
	ClientIP *clientip.Resolver
	// UserAgent logs r.UserAgent() when true. It is personal data, so a
	// request is never logged with it unless an application opts in.
	UserAgent bool
	// Skip reports whether a request is excluded from the access log. Nil
	// defaults to SkipHealthChecks.
	Skip func(*http.Request) bool
	// Now returns the current time. It defaults to time.Now and exists so
	// tests can control the clock.
	Now func() time.Time
}

// Middleware returns the access log handler. It defers before calling next,
// so the one log line for a request is written even when a handler further
// in panics: the panic is observed, not recovered, the line is written with
// the status forced to 500, and the same panic is then raised again
// unchanged for an outer Recover to answer.
func (a AccessLog) Middleware(next http.Handler) http.Handler {
	skip := a.Skip
	if skip == nil {
		skip = SkipHealthChecks
	}
	now := a.Now
	if now == nil {
		now = time.Now
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skip(r) {
			next.ServeHTTP(w, r)
			return
		}

		start := now()
		rw := &statusWriter{ResponseWriter: w}

		defer func() {
			status := rw.statusCode()
			problem := recover()
			if problem != nil {
				status = http.StatusInternalServerError
			}
			a.log(r, status, rw.bytes, now().Sub(start))
			if problem != nil {
				panic(problem)
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// log writes the one line for a request that reached this far.
func (a AccessLog) log(r *http.Request, status, bytes int, duration time.Duration) {
	attrs := []any{
		"method", r.Method,
		"route", Route(r),
		"status", status,
		"bytes", bytes,
		"duration_ms", duration.Milliseconds(),
	}
	if a.ClientIP != nil {
		attrs = append(attrs, "client", a.ClientIP.Resolve(r).String())
	}
	if a.UserAgent {
		attrs = append(attrs, "user_agent", r.UserAgent())
	}
	logging.FromContext(r.Context()).Info("request handled", attrs...)
}

// statusWriter wraps a http.ResponseWriter to capture the status code and
// the number of bytes written, defaulting the status to 200 whether the
// handler wrote without calling WriteHeader first or wrote nothing at all.
type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

// WriteHeader records status the first time it is called, matching
// net/http's own rule that only the first call has any effect.
func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write implicitly sends a 200 the way the underlying http.ResponseWriter
// does, and this wrapper has to see that happen to record it.
func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap exposes the real http.ResponseWriter so http.ResponseController can
// reach a capability such as Flush or SetWriteDeadline straight through this
// wrapper.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// statusCode is the status to log: whatever was recorded, or 200 if the
// handler never called WriteHeader and never wrote a body either.
func (w *statusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
