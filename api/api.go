// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package api is the wire contract of a JSON API: one error shape, strict
// decoding and the handler adapter that keeps both in one place.
//
// Handlers return an error instead of writing one, so no handler hand writes
// error JSON and every failure carries the request id the log line has.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/ostendorf-cloud/web-kit/logging"
)

// Codes are stable and machine readable: a client may branch on them, so a
// code is never renamed to improve its wording.
const (
	CodeInvalidArgument      = "invalid_argument"
	CodeUnauthenticated      = "unauthenticated"
	CodePermissionDenied     = "permission_denied"
	CodeNotFound             = "not_found"
	CodeConflict             = "conflict"
	CodeTooManyRequests      = "too_many_requests"
	CodeRequestTooLarge      = "request_too_large"
	CodeUnsupportedMediaType = "unsupported_media_type"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeUnavailable          = "unavailable"
	CodeInternal             = "internal"
)

// DefaultMaxBytes is the body limit Decode applies. Large uploads get their
// own endpoint with its own limit rather than raising this one.
const DefaultMaxBytes int64 = 1 << 20

// internalMessage is what a 5xx says. The cause is logged, never sent: it
// would carry table names, hostnames and library internals to the client.
const internalMessage = "internal error"

// Error is an API failure with everything the response needs. Handlers
// return it; WriteError turns it into the one error shape.
type Error struct {
	Status  int
	Code    string
	Message string
	// Details carries field errors, so a form can mark the inputs that failed.
	Details map[string]any
	// RetryAfter sets the Retry-After header when it is positive.
	RetryAfter time.Duration

	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches what actually went wrong. It is logged and never sent.
func (e *Error) WithCause(err error) *Error {
	copied := *e
	copied.cause = err
	return &copied
}

// WithDetails attaches the machine readable detail, usually field errors.
func (e *Error) WithDetails(details map[string]any) *Error {
	copied := *e
	copied.Details = details
	return &copied
}

// New builds an error with an explicit status and code, for a case the
// constructors below do not cover.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Invalid is a request the server understood and refuses: a missing field, a
// value out of range. The message is shown to the user, so it names the
// field.
func Invalid(message string) *Error {
	return New(http.StatusBadRequest, CodeInvalidArgument, message)
}

// Unauthenticated is a missing or expired login.
func Unauthenticated(message string) *Error {
	return New(http.StatusUnauthorized, CodeUnauthenticated, message)
}

// PermissionDenied is a login that exists but may not do this.
func PermissionDenied(message string) *Error {
	return New(http.StatusForbidden, CodePermissionDenied, message)
}

// NotFound is also the answer for something the caller may not see, so the
// existence of a resource is not leaked through a 403.
func NotFound(message string) *Error {
	return New(http.StatusNotFound, CodeNotFound, message)
}

// Conflict is a write that lost: a duplicate, or a stale version.
func Conflict(message string) *Error {
	return New(http.StatusConflict, CodeConflict, message)
}

// TooManyRequests carries the wait the limiter reported.
func TooManyRequests(retryAfter time.Duration) *Error {
	err := New(http.StatusTooManyRequests, CodeTooManyRequests, "too many requests, please wait a moment")
	err.RetryAfter = retryAfter
	return err
}

// Unavailable is a dependency that is down, for a caller that may retry.
func Unavailable(message string) *Error {
	return New(http.StatusServiceUnavailable, CodeUnavailable, message)
}

// Internal is a bug or a failure nobody planned for. The cause reaches the
// log and never the client.
func Internal(cause error) *Error {
	return New(http.StatusInternalServerError, CodeInternal, internalMessage).WithCause(cause)
}

// envelope is the single error shape: one object under "error", never a bare
// string and never a different shape per endpoint.
type envelope struct {
	Error payload `json:"error"`
}

type payload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// WriteError writes err in the one error shape. Anything that is not an
// *Error becomes a 500, because an error the server did not classify is a
// bug.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := AsError(err)

	body := payload{
		Code:      apiErr.Code,
		Message:   apiErr.Message,
		Details:   apiErr.Details,
		RequestID: logging.RequestID(r.Context()),
	}
	// Nothing internal is described to the client, whatever the handler put
	// in the message.
	if apiErr.Status >= http.StatusInternalServerError {
		body.Code, body.Message, body.Details = CodeInternal, internalMessage, nil
	}

	if apiErr.RetryAfter > 0 {
		seconds := int(apiErr.RetryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	// A failed write means the client is gone, and there is no second way to
	// tell it: this is already the error answer.
	_ = writeJSON(w, apiErr.Status, envelope{Error: body})
}

// AsError returns err as an *Error, classifying anything unknown as
// internal.
func AsError(err error) *Error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return Internal(err)
}

// HandlerFunc is a handler that returns its failure instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle adapts a HandlerFunc: it writes the error shape and logs the
// failure once, with the cause the client never sees. The logger comes from
// the request context, so the line carries the request id.
func Handle(h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		apiErr := AsError(err)
		logger := logging.FromContext(r.Context()).With("request_id", logging.RequestID(r.Context()))
		attrs := []any{"code", apiErr.Code, "status", apiErr.Status, "error", err.Error()}
		if apiErr.Status >= http.StatusInternalServerError {
			// A 5xx is this server's problem and the only level an operator
			// should be woken by.
			logger.ErrorContext(r.Context(), "request failed", attrs...)
		} else {
			// A 4xx is the caller's problem; the access log already has it.
			logger.DebugContext(r.Context(), "request refused", attrs...)
		}
		WriteError(w, r, apiErr)
	}
}

// NotFoundHandler answers an unknown API path in the error shape, so a
// client that mistypes a path gets JSON rather than the mux' plain text.
func NotFoundHandler() http.Handler {
	return Handle(func(http.ResponseWriter, *http.Request) error {
		return NotFound("unknown endpoint")
	})
}

// Write sends v as JSON with the given status.
func Write(w http.ResponseWriter, status int, v any) error {
	return writeJSON(w, status, v)
}

// NoContent answers a write that has nothing to return.
func NoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	// Encoding first: a failure halfway through would otherwise leave a
	// truncated body behind a 200.
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("api: encode response: %w", err)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("api: write response: %w", err)
	}
	return nil
}

// Decode reads a JSON request body into into, with the default limit.
func Decode(r *http.Request, into any) error {
	return DecodeLimit(r, into, DefaultMaxBytes)
}

// DecodeLimit reads a JSON request body into into, refusing anything longer
// than max, a wrong content type, an unknown field or a second JSON value.
// Unknown fields are refused so a typo in a client is not silently ignored.
func DecodeLimit(r *http.Request, into any, max int64) error {
	if err := requireJSON(r); err != nil {
		return err
	}
	// A nil ResponseWriter is deliberate: it only lets the reader tell the
	// server to stop reading early, and the limit itself holds without it.
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, max))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		return decodeError(err, max)
	}
	// Anything after the first value means the caller sent something else
	// than the one object the endpoint documents.
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Invalid("body must contain a single JSON value")
	}
	return nil
}

// requireJSON refuses a body that does not claim to be JSON. A missing
// header is accepted, because a form post reaching a JSON endpoint is caught
// by the decoder anyway.
func requireJSON(r *http.Request) error {
	raw := r.Header.Get("Content-Type")
	if raw == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || mediaType != "application/json" {
		return New(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "content type must be application/json")
	}
	return nil
}

func decodeError(err error, max int64) error {
	var syntax *json.SyntaxError
	var unmarshalType *json.UnmarshalTypeError
	var tooLarge *http.MaxBytesError

	switch {
	case errors.As(err, &tooLarge):
		return New(http.StatusRequestEntityTooLarge, CodeRequestTooLarge,
			fmt.Sprintf("body must be at most %d bytes", max))
	case errors.As(err, &syntax):
		return Invalid(fmt.Sprintf("body is not valid JSON at byte %d", syntax.Offset))
	case errors.As(err, &unmarshalType):
		message := fmt.Sprintf("field %s must be a %s", unmarshalType.Field, unmarshalType.Type)
		return Invalid(message).WithDetails(map[string]any{unmarshalType.Field: "wrong type"})
	case errors.Is(err, io.EOF):
		return Invalid("body is empty")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return Invalid("body ended in the middle of a JSON value")
	default:
		// DisallowUnknownFields reports a plain error; its text names the
		// field and is safe to show, it comes from the request.
		return Invalid(err.Error())
	}
}
