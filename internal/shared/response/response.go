package response

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"timebox-be/pkg/logger"

	"github.com/gin-gonic/gin"
)

// Generic error codes, one per status, for errors that need no code of
// their own. Clients branch on Response.Code, never on Message.
const (
	CodeValidationFailed   = "validation_failed"
	CodeBadRequest         = "bad_request"
	CodeUnauthorized       = "unauthorized"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodePayloadTooLarge    = "payload_too_large"
	CodeRateLimited        = "rate_limited"
	CodeInternalError      = "internal_error"
	CodeServiceUnavailable = "service_unavailable"
)

// Pagination represents pagination metadata
type Pagination struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// Meta represents response metadata
type Meta struct {
	Pagination *Pagination `json:"pagination,omitempty"`
}

// Response represents standard API response structure
type Response struct {
	Data    interface{} `json:"data"`
	Message string      `json:"message"`
	// Code is set on every error: a stable, machine-readable reason
	// (e.g. "invalid_code", "session_ended").
	Code   string              `json:"code,omitempty"`
	Meta   *Meta               `json:"meta,omitempty"`
	Errors map[string][]string `json:"errors,omitempty"`
	// RetryAfter is set on a 429: whole seconds until the request may be
	// repeated, the same value as the Retry-After header.
	RetryAfter int `json:"retry_after,omitempty"`
}

// Success sends a success response without pagination
func Success(c *gin.Context, statusCode int, message string, data interface{}) {
	c.JSON(statusCode, Response{
		Data:    data,
		Message: message,
	})
}

// SuccessWithPagination sends a success response with pagination metadata
func SuccessWithPagination(c *gin.Context, statusCode int, message string, data interface{}, page, limit int, total int64) {
	totalPages := int(total) / limit
	if int(total)%limit > 0 {
		totalPages++
	}

	c.JSON(statusCode, Response{
		Data:    data,
		Message: message,
		Meta: &Meta{
			Pagination: &Pagination{
				Page:       page,
				Limit:      limit,
				Total:      total,
				TotalPages: totalPages,
			},
		},
	})
}

// Error sends an error response with the generic code of statusCode.
func Error(c *gin.Context, statusCode int, message string, errorDetail string) {
	Fail(c, statusCode, DefaultCode(statusCode), message, errorDetail, nil)
}

// ErrorCode sends an error response with its own code.
func ErrorCode(c *gin.Context, statusCode int, code, message, errorDetail string) {
	Fail(c, statusCode, code, message, errorDetail, nil)
}

// Fail sends an error response. data, when not nil, is what the client
// needs to act on the error (e.g. the attempts left for a code).
func Fail(c *gin.Context, statusCode int, code, message, errorDetail string, data any) {
	resp := Response{
		Data:    data,
		Message: message,
		Code:    code,
	}

	if errorDetail != "" {
		resp.Errors = map[string][]string{
			"detail": {errorDetail},
		}
	}

	c.JSON(statusCode, resp)
}

// TooManyRequests sends a 429 with code, and retryAfter in whole seconds
// (at least 1) both in the Retry-After header and the body.
func TooManyRequests(c *gin.Context, code, message, errorDetail string, retryAfter time.Duration) {
	seconds := RetrySeconds(retryAfter)
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.JSON(http.StatusTooManyRequests, Response{
		Message:    message,
		Code:       code,
		Errors:     detailErrors(errorDetail),
		RetryAfter: seconds,
	})
}

// RetrySeconds rounds d up to whole seconds, at least 1.
func RetrySeconds(d time.Duration) int {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func detailErrors(detail string) map[string][]string {
	if detail == "" {
		return nil
	}
	return map[string][]string{"detail": {detail}}
}

// DefaultCode is the generic code of an error status.
func DefaultCode(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusServiceUnavailable:
		return CodeServiceUnavailable
	}
	if statusCode >= 500 {
		return CodeInternalError
	}
	return CodeBadRequest
}

// InternalError logs err and sends a 500 carrying only message. The error
// itself never reaches the client: it can contain SQL, hostnames, or other
// internals.
func InternalError(c *gin.Context, message string, err error) {
	logger.Error(message,
		logger.String("method", c.Request.Method),
		logger.String("path", c.FullPath()),
		logger.Err(err),
	)
	Error(c, http.StatusInternalServerError, message, "")
}

// ValidationError sends a validation error response: one list of messages
// per invalid field, named the way the client sent it.
func ValidationError(c *gin.Context, statusCode int, message string, errors map[string][]string) {
	c.JSON(statusCode, Response{
		Data:    nil,
		Message: message,
		Code:    CodeValidationFailed,
		Errors:  errors,
	})
}
