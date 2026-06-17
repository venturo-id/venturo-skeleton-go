// Package derrors defines the project's typed domain error and its
// machine-readable error codes. A *Error carries a code (mapped to an HTTP
// status by internal/shared/response.RenderError), a user-safe message, and an
// optional wrapped cause so errors.Is / errors.As keep working.
//
// See docs/errors.md for the full standard.
package derrors

import "fmt"

// ErrorCode classifies a domain error. The set is append-only: iota is
// positional, so never reorder or remove existing entries — only append at the
// bottom.
type ErrorCode uint

const (
	ErrorCodeUnknown ErrorCode = iota
	ErrorCodeNotFound
	ErrorCodeInvalidArgument
	ErrorCodeDuplicate
	ErrorCodeForbidden
	ErrorCodeUnauthorized
	ErrorCodeAlreadyExists
	ErrorCodeConflict
	ErrorCodeTokenExpired
	ErrorCodeRequestTimeout
	// passthrough codes — the message is user-facing and surfaced verbatim.
	ErrorCodeCustomBadRequest
	ErrorCodeCustomNotFound
	ErrorCodeCustomInternalServer
	ErrorCodeServiceUnavailable
	ErrorCodeCustomAlreadyExists // passthrough: 409, message surfaced verbatim
	ErrorCodeCustomForbidden     // passthrough: 403, message surfaced verbatim
)

// Error is the project's typed domain error.
type Error struct {
	orig error
	msg  string
	code ErrorCode
}

// Error returns "msg: orig" when wrapping a cause, otherwise just "msg".
func (e *Error) Error() string {
	if e.orig != nil {
		return fmt.Sprintf("%s: %v", e.msg, e.orig)
	}
	return e.msg
}

// Unwrap exposes the wrapped cause for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.orig }

// Message returns only the user-safe message, never the wrapped cause. The HTTP
// renderer uses this for passthrough codes so a wrapped cause (e.g. pgx.ErrNoRows)
// is never disclosed to the client, while Error() still embeds it for the logs.
func (e *Error) Message() string { return e.msg }

// Code returns the machine-readable error code.
func (e *Error) Code() ErrorCode { return e.code }

// NewErrorf builds a *Error with no wrapped cause.
func NewErrorf(code ErrorCode, format string, a ...any) *Error {
	return &Error{code: code, msg: fmt.Sprintf(format, a...)}
}

// WrapErrorf builds a *Error wrapping orig as the cause.
func WrapErrorf(orig error, code ErrorCode, format string, a ...any) *Error {
	return &Error{orig: orig, code: code, msg: fmt.Sprintf(format, a...)}
}
