// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/logging"
)

// decodeEnvelope reads the error envelope out of a response.
func decodeEnvelope(t *testing.T, body []byte) payload {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("response is not the error shape: %v: %s", err, body)
	}
	return env.Error
}

func TestWriteError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantRetry   string
	}{
		{"invalid", Invalid("field id must be a uuid"), http.StatusBadRequest, CodeInvalidArgument, "field id must be a uuid", ""},
		{"unauthenticated", Unauthenticated("please log in"), http.StatusUnauthorized, CodeUnauthenticated, "please log in", ""},
		{"permission denied", PermissionDenied("not your list"), http.StatusForbidden, CodePermissionDenied, "not your list", ""},
		{"not found", NotFound("no such wish"), http.StatusNotFound, CodeNotFound, "no such wish", ""},
		{"conflict", Conflict("already reserved"), http.StatusConflict, CodeConflict, "already reserved", ""},
		{"too many requests", TooManyRequests(20 * time.Second), http.StatusTooManyRequests, CodeTooManyRequests, "too many requests, please wait a moment", "20"},
		{"sub second wait is at least one", TooManyRequests(100 * time.Millisecond), http.StatusTooManyRequests, CodeTooManyRequests, "too many requests, please wait a moment", "1"},
		// Every 5xx collapses into the internal shape, a 503 included: its
		// code would tell a client nothing it can act on.
		{"unavailable collapses to internal", Unavailable("database unreachable"), http.StatusServiceUnavailable, CodeInternal, internalMessage, ""},
		{"internal keeps nothing of the cause", Internal(errors.New(`pq: relation "wishes" does not exist`)), http.StatusInternalServerError, CodeInternal, internalMessage, ""},
		{"a plain error is internal", errors.New("boom"), http.StatusInternalServerError, CodeInternal, internalMessage, ""},
		{"a wrapped api error is found", fmt.Errorf("load list: %w", NotFound("no such list")), http.StatusNotFound, CodeNotFound, "no such list", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
			WriteError(rec, req, tc.err)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("content type = %q", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("cache control = %q, want no-store", got)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.wantRetry {
				t.Errorf("Retry-After = %q, want %q", got, tc.wantRetry)
			}

			body := decodeEnvelope(t, rec.Body.Bytes())
			if body.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Code, tc.wantCode)
			}
			if body.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMessage)
			}
			// A 5xx never leaks the cause, whatever the handler put in it.
			if tc.wantStatus >= http.StatusInternalServerError {
				if strings.Contains(rec.Body.String(), "relation") || strings.Contains(rec.Body.String(), "boom") {
					t.Errorf("cause leaked into the response: %s", rec.Body.String())
				}
			}
		})
	}
}

func TestWriteErrorExposedSurvivesCollapse(t *testing.T) {
	t.Parallel()

	err := Unavailable("the contact form is down, please write to help@example.com instead").
		WithDetails(map[string]any{"retry": "later"}).
		Exposed()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	WriteError(rec, req, err)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	body := decodeEnvelope(t, rec.Body.Bytes())
	if body.Code != CodeUnavailable {
		t.Errorf("code = %q, want %q", body.Code, CodeUnavailable)
	}
	wantMessage := "the contact form is down, please write to help@example.com instead"
	if body.Message != wantMessage {
		t.Errorf("message = %q, want %q", body.Message, wantMessage)
	}
	if got := body.Details["retry"]; got != "later" {
		t.Errorf(`details["retry"] = %v, want "later"`, got)
	}
}

func TestWriteErrorExposedDoesNotLeakTheCauseOfAnInternalError(t *testing.T) {
	t.Parallel()

	err := Internal(errors.New("some sensitive db failure detail")).Exposed()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	WriteError(rec, req, err)

	body := decodeEnvelope(t, rec.Body.Bytes())
	if body.Message != internalMessage {
		t.Errorf("message = %q, want %q", body.Message, internalMessage)
	}
	// Internal's message is always the fixed constant, never the cause, so
	// even a mistaken Exposed call on an Internal error cannot send it.
	if strings.Contains(rec.Body.String(), "sensitive db failure detail") {
		t.Errorf("cause leaked into the response: %s", rec.Body.String())
	}
}

func TestWriteErrorCarriesTheRequestID(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req = req.WithContext(logging.WithRequestID(req.Context(), "req-42"))

	WriteError(rec, req, Internal(errors.New("boom")))

	if got := decodeEnvelope(t, rec.Body.Bytes()).RequestID; got != "req-42" {
		t.Errorf("request_id = %q, want req-42", got)
	}
}

func TestWriteErrorWithoutRequestIDLeavesItEmpty(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)

	WriteError(rec, req, Invalid("bad"))

	if got := decodeEnvelope(t, rec.Body.Bytes()).RequestID; got != "" {
		t.Errorf("request_id = %q, want empty", got)
	}
}

func TestWriteErrorKeepsDetails(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", nil)

	WriteError(rec, req, Invalid("two fields are wrong").WithDetails(map[string]any{
		"title": "must not be empty",
		"price": "must be a whole number of cents",
	}))

	details := decodeEnvelope(t, rec.Body.Bytes()).Details
	if details["title"] != "must not be empty" || details["price"] == nil {
		t.Errorf("details = %v", details)
	}
}

func TestWriteErrorDropsDetailsOnA5xx(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", nil)

	WriteError(rec, req, Internal(errors.New("boom")).WithDetails(map[string]any{"table": "wishes"}))

	if details := decodeEnvelope(t, rec.Body.Bytes()).Details; details != nil {
		t.Errorf("details leaked on a 5xx: %v", details)
	}
}

func TestWithCauseDoesNotMutateTheReceiver(t *testing.T) {
	t.Parallel()

	base := Invalid("bad")
	withCause := base.WithCause(errors.New("inner"))

	if base.cause != nil {
		t.Error("the constructor's error was mutated")
	}
	if withCause.cause == nil {
		t.Error("the copy did not take the change")
	}
	if !errors.Is(withCause, withCause.cause) {
		t.Error("the cause is not reachable with errors.Is")
	}
}

func TestWithDetailsDoesNotMutateTheReceiver(t *testing.T) {
	t.Parallel()

	base := Invalid("bad")
	withDetails := base.WithDetails(map[string]any{"a": "b"})

	if base.Details != nil {
		t.Error("the constructor's error was mutated")
	}
	if withDetails.Details == nil {
		t.Error("the copy did not take the change")
	}
}

func TestErrorStringIncludesTheCauseOnlyWhenSet(t *testing.T) {
	t.Parallel()

	withoutCause := Invalid("bad field")
	if !strings.Contains(withoutCause.Error(), "bad field") {
		t.Errorf("Error() = %q, want it to mention the message", withoutCause.Error())
	}

	withCause := Internal(errors.New("disk gone"))
	if !strings.Contains(withCause.Error(), "disk gone") {
		t.Errorf("Error() = %q, want it to mention the cause", withCause.Error())
	}
}

func TestAsError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"already an *Error", NotFound("gone"), http.StatusNotFound, CodeNotFound},
		{"a wrapped *Error is unwrapped", fmt.Errorf("load: %w", Conflict("stale")), http.StatusConflict, CodeConflict},
		{"a plain error is classified as internal", errors.New("boom"), http.StatusInternalServerError, CodeInternal},
		{"nil has nowhere to classify to", nil, http.StatusInternalServerError, CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := AsError(tc.err)
			if got.Status != tc.wantStatus || got.Code != tc.wantCode {
				t.Errorf("AsError(%v) = %s (%d), want %s (%d)", tc.err, got.Code, got.Status, tc.wantCode, tc.wantStatus)
			}
		})
	}
}

func TestHandleLogsAndWrites(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		err       error
		wantLevel string
	}{
		{"server failure is logged at error", Internal(errors.New("disk gone")), "ERROR"},
		{"client failure is logged at debug", Invalid("missing title"), "DEBUG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := logging.New(&buf, slog.LevelDebug)

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", nil)
			req = req.WithContext(logging.NewContext(req.Context(), logger))

			Handle(func(http.ResponseWriter, *http.Request) error { return tc.err })(rec, req)

			var line struct {
				Level string `json:"level"`
			}
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line is not JSON: %v: %s", err, buf.String())
			}
			if line.Level != tc.wantLevel {
				t.Errorf("level = %q, want %q", line.Level, tc.wantLevel)
			}
			if rec.Code != AsError(tc.err).Status {
				t.Errorf("status = %d, want %d", rec.Code, AsError(tc.err).Status)
			}
			if !bytes.Contains(buf.Bytes(), []byte(`"code":"`+AsError(tc.err).Code+`"`)) {
				t.Errorf("log line does not carry the code: %s", buf.String())
			}
		})
	}
}

func TestHandleLogsTheRequestID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	// The logger already carries the request id, the way middleware.RequestID
	// scopes it before a handler ever runs: Handle logs through whatever
	// logger the context carries and never re-adds the key itself.
	logger := logging.New(&buf, slog.LevelDebug).With("request_id", "req-42")

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", nil)
	req = req.WithContext(logging.NewContext(req.Context(), logger))
	req = req.WithContext(logging.WithRequestID(req.Context(), "req-42"))

	Handle(func(http.ResponseWriter, *http.Request) error { return Internal(errors.New("disk gone")) })(rec, req)

	var line struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v: %s", err, buf.String())
	}
	if line.RequestID != "req-42" {
		t.Errorf("log line request_id = %q, want req-42: %s", line.RequestID, buf.String())
	}

	if got := decodeEnvelope(t, rec.Body.Bytes()).RequestID; got != line.RequestID {
		t.Errorf("response request_id = %q, log line request_id = %q, want them to match", got, line.RequestID)
	}
}

func TestHandleWritesNothingExtraOnSuccess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things", nil)
	req = req.WithContext(logging.NewContext(req.Context(), logging.New(&buf, slog.LevelDebug)))

	Handle(func(w http.ResponseWriter, _ *http.Request) error {
		return Write(w, http.StatusOK, map[string]string{"status": "ok"})
	})(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Errorf("body = %s, want exactly what the handler wrote", got)
	}
	if buf.Len() != 0 {
		t.Errorf("success was logged: %s", buf.String())
	}
}

func TestNotFoundHandler(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NotFoundHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := decodeEnvelope(t, rec.Body.Bytes()).Code; got != CodeNotFound {
		t.Errorf("code = %q, want %q", got, CodeNotFound)
	}
}

func TestNoContent(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NoContent(rec)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q, want no-store", got)
	}
}

type thing struct {
	Title string `json:"title"`
	Count int    `json:"count"`
}

func TestDecode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		wantCode    string
		wantStatus  int
		wantTitle   string
	}{
		{name: "object", contentType: "application/json", body: `{"title":"lamp","count":2}`, wantTitle: "lamp"},
		{name: "content type with charset", contentType: "application/json; charset=utf-8", body: `{"title":"lamp"}`, wantTitle: "lamp"},
		{name: "no content type is accepted", contentType: "", body: `{"title":"lamp"}`, wantTitle: "lamp"},
		{name: "form content type is rejected", contentType: "application/x-www-form-urlencoded", body: `title=lamp`, wantCode: CodeUnsupportedMediaType, wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field is rejected", contentType: "application/json", body: `{"title":"lamp","colour":"red"}`, wantCode: CodeInvalidArgument, wantStatus: http.StatusBadRequest},
		{name: "wrong type is rejected", contentType: "application/json", body: `{"count":"two"}`, wantCode: CodeInvalidArgument, wantStatus: http.StatusBadRequest},
		{name: "broken json", contentType: "application/json", body: `{"title":`, wantCode: CodeInvalidArgument, wantStatus: http.StatusBadRequest},
		{name: "empty body", contentType: "application/json", body: ``, wantCode: CodeInvalidArgument, wantStatus: http.StatusBadRequest},
		{name: "two json values in one body", contentType: "application/json", body: `{"title":"a"}{"title":"b"}`, wantCode: CodeInvalidArgument, wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			var got thing
			err := Decode(req, &got)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if got.Title != tc.wantTitle {
					t.Errorf("title = %q, want %q", got.Title, tc.wantTitle)
				}
				return
			}
			if err == nil {
				t.Fatal("Decode accepted a body it should have rejected")
			}
			apiErr := AsError(err)
			if apiErr.Code != tc.wantCode || apiErr.Status != tc.wantStatus {
				t.Errorf("error = %s (%d), want %s (%d)", apiErr.Code, apiErr.Status, tc.wantCode, tc.wantStatus)
			}
		})
	}
}

func TestDecodeLimitRejectsABodyOverTheLimit(t *testing.T) {
	t.Parallel()

	body := `{"title":"` + strings.Repeat("x", 100) + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var got thing
	err := DecodeLimit(req, &got, 32)
	if err == nil {
		t.Fatal("DecodeLimit accepted a body over its limit")
	}
	apiErr := AsError(err)
	if apiErr.Code != CodeRequestTooLarge || apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("error = %s (%d), want %s (%d)", apiErr.Code, apiErr.Status, CodeRequestTooLarge, http.StatusRequestEntityTooLarge)
	}
}

func TestDecodeLimitAcceptsABodyAtTheLimit(t *testing.T) {
	t.Parallel()

	body := `{"title":"ok"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/things", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var got thing
	if err := DecodeLimit(req, &got, int64(len(body))); err != nil {
		t.Fatalf("DecodeLimit: %v", err)
	}
	if got.Title != "ok" {
		t.Errorf("title = %q, want ok", got.Title)
	}
}

func TestWriteEncodesTheValue(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	if err := Write(rec, http.StatusCreated, thing{Title: "lamp", Count: 1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"title":"lamp","count":1}` {
		t.Errorf("body = %s", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
}

func TestWriteReportsAnUnencodableValueWithoutAHalfWrittenBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	err := Write(rec, http.StatusOK, map[string]any{"ch": make(chan int)})
	if err == nil {
		t.Fatal("Write accepted a value JSON cannot encode")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("a truncated body was written: %q", rec.Body.String())
	}
	// The header is only set once encoding has already succeeded, so a
	// failed encode must not have touched the response at all.
	if got := rec.Header().Get("Content-Type"); got != "" {
		t.Errorf("a header was written on a failed encode: %q", got)
	}
}
