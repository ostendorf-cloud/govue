// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/logging"
)

// withLogger attaches a JSON logger writing to buf to r's context, the way
// RequestID would in a real chain.
func withLogger(t *testing.T, r *http.Request, buf *bytes.Buffer) *http.Request {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return r.WithContext(logging.NewContext(r.Context(), logger))
}

func TestAccessLogLogsTheRoutePatternNeverTheRawPath(t *testing.T) {
	const secret = "acct-f00ba12-secret"

	mux := http.NewServeMux()
	mux.HandleFunc("GET /lists/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var buf bytes.Buffer
	handler := AccessLog{}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/lists/"+secret, nil)
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	logged := buf.String()
	if strings.Contains(logged, secret) {
		t.Errorf("the raw path segment leaked into the log line: %s", logged)
	}

	var line struct {
		Route string `json:"route"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, logged)
	}
	if line.Route != "GET /lists/{id}" {
		t.Errorf("route = %q, want the mux pattern", line.Route)
	}
}

func TestAccessLogSkipsHealthChecksByDefault(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var buf bytes.Buffer
	handler := AccessLog{}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if buf.Len() != 0 {
		t.Errorf("a health check request was logged: %s", buf.String())
	}
}

func TestAccessLogCapturesStatusAndBytes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})

	var buf bytes.Buffer
	handler := AccessLog{}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/things", nil)
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	var line struct {
		Status int `json:"status"`
		Bytes  int `json:"bytes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	if line.Status != http.StatusCreated {
		t.Errorf("status = %d, want 201", line.Status)
	}
	if line.Bytes != len("hello") {
		t.Errorf("bytes = %d, want %d", line.Bytes, len("hello"))
	}
}

func TestAccessLogDefaultsStatusTo200WhenNothingIsWritten(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things", func(http.ResponseWriter, *http.Request) {})

	var buf bytes.Buffer
	handler := AccessLog{}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/things", nil)
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	var line struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	if line.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", line.Status)
	}
}

func TestAccessLogUserAgentOffByDefault(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things", func(http.ResponseWriter, *http.Request) {})

	var buf bytes.Buffer
	handler := AccessLog{}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/things", nil)
	req.Header.Set("User-Agent", "secret-agent/1.0")
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(buf.String(), "secret-agent") {
		t.Errorf("the user agent was logged without opting in: %s", buf.String())
	}
}

func TestAccessLogUserAgentWhenEnabled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things", func(http.ResponseWriter, *http.Request) {})

	var buf bytes.Buffer
	handler := AccessLog{UserAgent: true}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/things", nil)
	req.Header.Set("User-Agent", "secret-agent/1.0")
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	var line struct {
		UserAgent string `json:"user_agent"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	if line.UserAgent != "secret-agent/1.0" {
		t.Errorf("user_agent = %q, want the request's header", line.UserAgent)
	}
}

func TestAccessLogClientOnlyWhenResolverSet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things", func(http.ResponseWriter, *http.Request) {})

	var buf bytes.Buffer
	handler := AccessLog{}.Middleware(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/things", nil)
	req = withLogger(t, req, &buf)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(buf.String(), `"client"`) {
		t.Errorf("a client address was logged without a resolver: %s", buf.String())
	}
}

func TestAccessLogOneLineOn500WhenAHandlerPanics(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := Chain(mux, Recover(logger), RequestID(logger), AccessLog{}.Middleware)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	requestID := rec.Header().Get(RequestIDHeader)
	if requestID == "" {
		t.Fatal("no request id on the response")
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	var accessLines []map[string]any
	for _, l := range lines {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(l), &parsed); err != nil {
			t.Fatalf("log line is not JSON: %v (%s)", err, l)
		}
		if parsed["msg"] == "request handled" {
			accessLines = append(accessLines, parsed)
		}
	}
	if len(accessLines) != 1 {
		t.Fatalf("got %d access log lines, want exactly 1: %s", len(accessLines), logs.String())
	}
	line := accessLines[0]

	gotStatus, _ := line["status"].(float64)
	if int(gotStatus) != http.StatusInternalServerError {
		t.Errorf("access log status = %v, want 500", line["status"])
	}
	if got := line["request_id"]; got != requestID {
		t.Errorf("access log request_id = %v, want %q", got, requestID)
	}

	var panicLine map[string]any
	for _, l := range lines {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(l), &parsed); err != nil {
			continue
		}
		if parsed["msg"] == "panic recovered" {
			panicLine = parsed
		}
	}
	if panicLine == nil {
		t.Fatal("no panic log line found")
	}
	if got := panicLine["request_id"]; got != requestID {
		t.Errorf("panic log request_id = %v, want %q matching the access log line", got, requestID)
	}
}
