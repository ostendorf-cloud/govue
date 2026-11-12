package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/ostendorf-cloud/web-kit/logging"
)

// RequestIDHeader is the response header a request id is sent back on. A
// client that logs its own requests can tie them to the server's log through
// this value.
const RequestIDHeader = "X-Request-Id"

// requestIDBytes is how many random bytes the id is generated from. Encoded
// as hex, that is a 16 character id: short enough for a log line and a
// response header, and with enough entropy that two requests never collide
// by chance.
const requestIDBytes = 8

// RequestID returns a middleware that gives every request its own id. It
// generates the id itself and ignores any value the client sent on
// RequestIDHeader: a request id is this server's own correlation key, not
// something a caller gets to pick or spoof.
//
// The id is set on the response header and carried, together with a logger
// scoped to it, through the request context via the logging package. A
// handler further in the chain reads either back with logging.RequestID and
// logging.FromContext.
func RequestID(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := newRequestID()

			w.Header().Set(RequestIDHeader, id)

			ctx := logging.WithRequestID(r.Context(), id)
			ctx = logging.NewContext(ctx, logger.With("request_id", id))

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// newRequestID returns a fresh, random request id. A read failure from
// crypto/rand, which does not happen on a supported platform, falls back to
// an all-zero id rather than panicking: a request still gets a stable,
// if degenerate, correlation key instead of failing outright.
func newRequestID() string {
	buf := make([]byte, requestIDBytes)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
