package errs

import (
	"errors"
	"net/http"
)

// General application error codes defined by the HTTP contract.
const (
	ErrInvalidParam        = 10001
	ErrInternal            = 10002
	ErrNotFound            = 10003
	ErrConflict            = 10004
	ErrTooManyRequests     = 10005
	ErrIdempotencyMismatch = 10006
	ErrUpstreamTimeout     = 10007
	ErrVersionConflict     = 10409
)

// AppError describes a public error while retaining a private underlying cause.
type AppError struct {
	Code       int
	HTTPStatus int
	Message    string
	Details    map[string]any
	Cause      error
}

// New constructs an application error using a safe public message.
func New(code int, httpStatus int, msg string) *AppError {
	return &AppError{Code: code, HTTPStatus: httpStatus, Message: msg}
}

// Error returns the safe public message.
func (e *AppError) Error() string { return e.Message }

// Unwrap exposes the underlying cause to errors.Is and errors.As.
func (e *AppError) Unwrap() error { return e.Cause }

// From finds a wrapped AppError or converts an unknown error into an internal error.
func From(err error) *AppError {
	var app *AppError
	if errors.As(err, &app) && app != nil {
		return app
	}
	return &AppError{
		Code: ErrInternal, HTTPStatus: http.StatusInternalServerError,
		Message: "内部错误", Cause: err,
	}
}

// Invalid wraps a validation failure without exposing its raw contents.
func Invalid(err error) *AppError {
	return &AppError{
		Code: ErrInvalidParam, HTTPStatus: http.StatusBadRequest,
		Message: "参数校验失败", Cause: err,
	}
}
