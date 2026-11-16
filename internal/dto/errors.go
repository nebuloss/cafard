package dto

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Error codes (CONTRACTS.md section 3.2).
const (
	CodeBadRequest      = "bad_request"
	CodeInvalidPattern  = "invalid_pattern"
	CodeUnknownChannel  = "unknown_channel"
	CodeTooLarge        = "too_large"
	CodeUnauthenticated = "unauthenticated"
	CodeForbidden       = "forbidden"
	CodeCrossOrigin     = "cross_origin"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeRateLimited     = "rate_limited"
	CodeBusy            = "busy"
)

// Codes lists every error code.
var Codes = []string{
	CodeBadRequest, CodeInvalidPattern, CodeUnknownChannel, CodeTooLarge,
	CodeUnauthenticated, CodeForbidden, CodeCrossOrigin, CodeNotFound,
	CodeConflict, CodeRateLimited, CodeBusy,
}

// HTTPStatus is the status an error code is sent with; 500 for an
// unknown code.
func HTTPStatus(code string) int {
	switch code {
	case CodeBadRequest, CodeInvalidPattern, CodeUnknownChannel:
		return http.StatusBadRequest
	case CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeForbidden, CodeCrossOrigin:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeBusy:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// ErrorBody is an error as the API and MCP structured content show it.
type ErrorBody struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	RetryAfterS int    `json:"retry_after_s,omitempty"`
}

// Error is the JSON body of an error response.
type Error struct {
	Error ErrorBody `json:"error"`
}

// APIError is an error that carries its API code. Message is a full
// sentence for a person.
type APIError struct {
	Code        string
	Message     string
	RetryAfterS int
}

// Errorf builds an APIError with a formatted message.
func Errorf(code, format string, args ...any) *APIError {
	return &APIError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Error implements error.
func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// RetryAfter sets RetryAfterS and returns e.
func (e *APIError) RetryAfter(seconds int) *APIError {
	e.RetryAfterS = seconds
	return e
}

// Body is the error's ErrorBody.
func (e *APIError) Body() ErrorBody {
	return ErrorBody{Code: e.Code, Message: e.Message, RetryAfterS: e.RetryAfterS}
}

// WriteError writes err as an Error body, with its status and, when it has
// one, a Retry-After header.
func WriteError(w http.ResponseWriter, err *APIError) {
	if err == nil {
		err = &APIError{Code: "internal", Message: "Something went wrong on the server."}
	}
	if err.RetryAfterS > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(err.RetryAfterS))
	}
	WriteJSON(w, HTTPStatus(err.Code), Error{Error: err.Body()})
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "cannot encode the response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
