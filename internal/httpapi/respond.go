// Package httpapi implements the Cairn HTTP interface: the JSON/API v1
// routes and the embedded web frontend.
//
// Handlers in this package stay thin. Business logic lives in application
// services and domain packages; this package only translates HTTP into those
// calls and back.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// Error codes returned in the JSON error envelope. They are stable API
// surface; clients should branch on them rather than on HTTP status alone.
const (
	CodeBadRequest           = "BAD_REQUEST"
	CodeUnauthorized         = "UNAUTHORIZED"
	CodeForbidden            = "FORBIDDEN"
	CodeNotFound             = "NOT_FOUND"
	CodeMethodNotAllowed     = "METHOD_NOT_ALLOWED"
	CodeConflict             = "CONFLICT"
	CodeRateLimited          = "RATE_LIMITED"
	CodeInternal             = "INTERNAL"
	CodeServiceUnavailable   = "SERVICE_UNAVAILABLE"
	CodePayloadTooLarge      = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMediaType = "UNSUPPORTED_MEDIA_TYPE"
)

// ErrorResponse is the envelope for every error response. The frontend and
// future native clients parse this shape.
//
//	{"error": {"code": "NOT_FOUND", "message": "...", "details": {}, "request_id": "..."}}
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the inner, machine-readable error object.
type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id"`
}

// requestIDContextKey is the context key for the current request ID.
type requestIDContextKey struct{}

// WithRequestID returns a context carrying the given request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

// RequestIDFrom extracts the request ID from a context, if present.
func RequestIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDContextKey{}).(string)
	return id, ok
}

// writeJSON serializes v as JSON with the given status code. It never fails
// the connection on serialization errors; instead it logs and falls back to a
// minimal error response.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("write json response", "error", err)
	}
}

// writeError writes a JSON error envelope. requestID may be empty; the caller
// should plumb one through when available.
func writeError(w http.ResponseWriter, logger *slog.Logger, requestID string, status int, code, message string) {
	writeJSON(w, logger, status, ErrorResponse{
		Error: ErrorBody{
			Code:      code,
			Message:   message,
			RequestID: requestID,
		},
	})
}

// writeErrorf is writeError with a formatted message.
func writeErrorf(w http.ResponseWriter, logger *slog.Logger, requestID string, status int, code, format string, args ...any) {
	writeError(w, logger, requestID, status, code, fmt.Sprintf(format, args...))
}

// statusError pairs an error with the HTTP status code a handler should
// return for it. Domain errors can implement this to control their mapping.
type statusError interface {
	error
	Status() int
}

// writeDomainError maps an arbitrary error from a service layer into an HTTP
// response, honoring statusError implementations.
func writeDomainError(w http.ResponseWriter, logger *slog.Logger, requestID string, err error) {
	var se statusError
	switch {
	case errors.As(err, &se):
		writeError(w, logger, requestID, se.Status(), codeForStatus(se.Status()), se.Error())
	default:
		logger.Error("unexpected domain error", "error", err)
		writeError(w, logger, requestID, http.StatusInternalServerError, CodeInternal, "Internal server error.")
	}
}

// maxBodyBytes bounds authenticated JSON bodies (login, user creation). Bodies
// larger than this are rejected with 413 before parsing.
const maxBodyBytes = 1 << 20 // 1 MiB

// readJSON decodes a request body of at most maxBodyBytes into dst, returning a
// statusError the caller can hand to writeDomainError.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err := json.NewDecoder(r.Body).Decode(dst)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &statusErr{status: http.StatusRequestEntityTooLarge,
				msg: "Request body too large."}
		}
		if err == io.EOF {
			return &statusErr{status: http.StatusBadRequest,
				msg: "Request body must be a JSON object."}
		}
		return &statusErr{status: http.StatusBadRequest,
			msg: "Request body must be valid JSON."}
	}
	return nil
}

// statusErr is a small error carrying an HTTP status; it implements statusError.
type statusErr struct {
	status int
	msg    string
}

func (e *statusErr) Error() string { return e.msg }
func (e *statusErr) Status() int   { return e.status }

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusUnsupportedMediaType:
		return CodeUnsupportedMediaType
	case http.StatusServiceUnavailable:
		return CodeServiceUnavailable
	default:
		return CodeInternal
	}
}
