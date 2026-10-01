// Package apperr defines typed, client-safe errors that map onto HTTP responses.
package apperr

import (
	"errors"
	"net/http"
)

// Error is an error that is safe to show to API clients.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an Error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// As extracts an *Error from err, if any.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Constructors for the common classes.
func BadRequest(code, msg string) *Error { return New(http.StatusBadRequest, code, msg) }
func NotFound(code, msg string) *Error   { return New(http.StatusNotFound, code, msg) }
func Conflict(code, msg string) *Error   { return New(http.StatusConflict, code, msg) }
func Forbidden(msg string) *Error        { return New(http.StatusForbidden, "forbidden", msg) }
func Unauthorized(msg string) *Error     { return New(http.StatusUnauthorized, "unauthorized", msg) }

// Well-known error codes the clients rely on.
const (
	CodeValidation         = "validation_error"
	CodeDepartmentNotFound = "department_not_found"
	CodeDepartmentPaused   = "department_paused"
	CodeTokenNotFound      = "token_not_found"
	CodeInvalidState       = "invalid_token_state"
	CodeAlreadyServing     = "already_serving"
	CodeQueueEmpty         = "queue_empty"
	CodeDuplicateToken     = "duplicate_active_token"
	CodeSameDepartment     = "same_department"
	CodeRateLimited        = "rate_limited"
	CodeConflict           = "conflict"
)
