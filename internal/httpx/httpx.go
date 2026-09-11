// Package httpx contains small helpers shared by all HTTP handlers:
// a typed API error, an error-returning handler adapter and JSON encoding/decoding.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// Error is an API error that is rendered as a JSON body.
//
//	{"error": {"code": "unsupported_provider", "message": "Unsupported provider: https://vimeo.com"}}
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// WithDetails returns a copy of the error carrying extra machine-readable details.
func (e *Error) WithDetails(d any) *Error {
	cp := *e
	cp.Details = d
	return &cp
}

// NewError builds an Error.
func NewError(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func BadRequest(code, format string, args ...any) *Error {
	return NewError(http.StatusBadRequest, code, format, args...)
}
func Unauthorized(format string, args ...any) *Error {
	return NewError(http.StatusUnauthorized, "unauthorized", format, args...)
}
func Forbidden(format string, args ...any) *Error {
	return NewError(http.StatusForbidden, "forbidden", format, args...)
}
func NotFound(code, format string, args ...any) *Error {
	return NewError(http.StatusNotFound, code, format, args...)
}
func Conflict(code, format string, args ...any) *Error {
	return NewError(http.StatusConflict, code, format, args...)
}
func UnprocessableEntity(code, format string, args ...any) *Error {
	return NewError(http.StatusUnprocessableEntity, code, format, args...)
}
func BadGateway(code, format string, args ...any) *Error {
	return NewError(http.StatusBadGateway, code, format, args...)
}

// HandlerFunc is an http.Handler whose function can return an error.
// *Error values are rendered as-is; anything else becomes a 500.
type HandlerFunc func(http.ResponseWriter, *http.Request) error

func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f(w, r)
	if err == nil {
		return
	}
	WriteError(w, r, err)
}

// WriteError renders err as a JSON error response.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		slog.ErrorContext(r.Context(), "unhandled error", "err", err, "method", r.Method, "path", r.URL.Path)
		apiErr = NewError(http.StatusInternalServerError, "internal_error", "internal server error")
	}
	JSON(w, apiErr.Status, map[string]any{"error": apiErr})
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("write json response", "err", err)
	}
}

// NoContent writes a 204 response.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

const maxBodyBytes = 1 << 20 // 1 MiB

// Decode reads a JSON request body into v. It limits the body size and
// rejects trailing garbage. An empty body is allowed when allowEmpty is true.
func Decode(r *http.Request, v any, allowEmpty bool) error {
	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) && allowEmpty {
			return nil
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return NewError(http.StatusRequestEntityTooLarge, "body_too_large", "request body must be at most %d bytes", maxBodyBytes)
		}
		return BadRequest("invalid_json", "invalid JSON body: %v", err)
	}
	if dec.More() {
		return BadRequest("invalid_json", "invalid JSON body: unexpected data after JSON value")
	}
	return nil
}
