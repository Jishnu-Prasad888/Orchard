package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, testLogger(), "req-1", http.StatusNotFound, CodeNotFound, "The requested resource was not found.")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q", ct)
	}

	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Error.Code != CodeNotFound {
		t.Errorf("code = %q", env.Error.Code)
	}
	if env.Error.RequestID != "req-1" {
		t.Errorf("request_id = %q", env.Error.RequestID)
	}
	if env.Error.Message == "" {
		t.Error("message is empty")
	}
}

func TestWriteErrorf(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorf(rec, testLogger(), "", http.StatusBadRequest, CodeBadRequest, "invalid value for %s: %q", "limit", "-1")

	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(env.Error.Message, "limit") {
		t.Errorf("message = %q", env.Error.Message)
	}
}

func TestWriteDomainErrorKnown(t *testing.T) {
	err := errorWithStatus{status: http.StatusConflict, msg: "already exists"}
	rec := httptest.NewRecorder()
	writeDomainError(rec, testLogger(), "rid", err)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Error.Code != CodeConflict {
		t.Errorf("code = %q, want CONFLICT", env.Error.Code)
	}
	if env.Error.Message != "already exists" {
		t.Errorf("message = %q", env.Error.Message)
	}
}

func TestWriteDomainErrorUnknown(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDomainError(rec, testLogger(), "rid", errors.New("secret internal detail"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Error.Message == "secret internal detail" {
		t.Error("internal error message leaked to the client")
	}
}

type errorWithStatus struct {
	status int
	msg    string
}

func (e errorWithStatus) Error() string { return e.msg }
func (e errorWithStatus) Status() int   { return e.status }

func TestCodeForStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, CodeBadRequest},
		{http.StatusUnauthorized, CodeUnauthorized},
		{http.StatusForbidden, CodeForbidden},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		{http.StatusConflict, CodeConflict},
		{http.StatusTooManyRequests, CodeRateLimited},
		{http.StatusRequestEntityTooLarge, CodePayloadTooLarge},
		{http.StatusUnsupportedMediaType, CodeUnsupportedMediaType},
		{http.StatusServiceUnavailable, CodeServiceUnavailable},
		{302, CodeInternal},
	}
	for _, tc := range cases {
		if got := codeForStatus(tc.status); got != tc.want {
			t.Errorf("codeForStatus(%d) = %q, want %q", tc.status, got, tc.want)
		}
	}
}
