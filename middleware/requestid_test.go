package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/web-kit/logging"
)

func discardLogger() *slog.Logger {
	return logging.New(io.Discard, slog.LevelDebug)
}

func TestRequestIDSetsHeaderAndContext(t *testing.T) {
	var gotID string
	var gotLogger *slog.Logger
	handler := RequestID(discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID = logging.RequestID(r.Context())
		gotLogger = logging.FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	header := rec.Header().Get(RequestIDHeader)
	if header == "" {
		t.Fatal("no request id on the response header")
	}
	if gotID != header {
		t.Errorf("context request id = %q, want %q", gotID, header)
	}
	if gotLogger == nil {
		t.Error("no logger carried through the context")
	}
}

func TestRequestIDIgnoresAClientSuppliedHeader(t *testing.T) {
	const spoofed = "attacker-chosen-id"

	var gotID string
	handler := RequestID(discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID = logging.RequestID(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, spoofed)
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(RequestIDHeader); got == spoofed {
		t.Errorf("response header echoed the client-supplied value %q", spoofed)
	}
	if gotID == spoofed {
		t.Errorf("context carried the client-supplied value %q", spoofed)
	}
}

func TestRequestIDsAreUnique(t *testing.T) {
	handler := RequestID(discardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if first.Header().Get(RequestIDHeader) == second.Header().Get(RequestIDHeader) {
		t.Error("two requests got the same id")
	}
}
