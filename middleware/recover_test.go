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
)

func TestRecoverRepanicsErrAbortHandlerUnchanged(t *testing.T) {
	handler := Recover(discardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		got := recover()
		if got != http.ErrAbortHandler { //nolint:errorlint // identity check is the point of the test, not errors.Is
			t.Fatalf("recovered %v, want http.ErrAbortHandler unchanged", got)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	t.Fatal("ErrAbortHandler did not propagate past Recover")
}

func TestRecoverAnswersAPIErrorShapeWithRequestID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
		panic("something exploded with a secret: s3cr3t")
	})

	handler := Chain(mux, Recover(logger), RequestID(logger))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not the API error shape: %v (%s)", err, rec.Body.String())
	}
	if body.Error.Code != "internal" {
		t.Errorf("code = %q, want internal", body.Error.Code)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Errorf("the panic value leaked into the response body: %s", rec.Body.String())
	}

	requestID := rec.Header().Get(RequestIDHeader)
	if requestID == "" {
		t.Fatal("no request id on the response")
	}
	if body.Error.RequestID != requestID {
		t.Errorf("body request id = %q, want %q", body.Error.RequestID, requestID)
	}
	if !strings.Contains(logs.String(), requestID) {
		t.Errorf("the panic log line does not carry the request id %q: %s", requestID, logs.String())
	}
	if !strings.Contains(logs.String(), "panic recovered") {
		t.Errorf("no panic log line written: %s", logs.String())
	}
}

func TestRecoverWithoutRequestIDStillAnswers(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(logs.String(), "request_id") {
		t.Errorf("a request id was logged without one ever being set: %s", logs.String())
	}
}
