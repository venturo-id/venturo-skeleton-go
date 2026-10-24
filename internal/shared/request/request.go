// Package request binds and checks request input in handlers. Each helper
// writes the 400 response itself and returns false, so a handler only has
// to return:
//
//	var req dto.CreateUserRequest
//	if !request.BindJSON(c, &req) {
//		return
//	}
package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	playground "github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"timebox-be/internal/shared/response"
)

// BindJSON decodes and validates the JSON body into obj.
func BindJSON(c *gin.Context, obj any) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		writeBindError(c, "Invalid request payload", err)
		return false
	}
	return true
}

// BindOptionalJSON is BindJSON for endpoints whose body may be empty.
func BindOptionalJSON(c *gin.Context, obj any) bool {
	if c.Request.ContentLength == 0 {
		return true
	}
	err := c.ShouldBindJSON(obj)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	writeBindError(c, "Invalid request payload", err)
	return false
}

// BindQuery decodes and validates the query string into obj.
func BindQuery(c *gin.Context, obj any) bool {
	if err := c.ShouldBindQuery(obj); err != nil {
		writeBindError(c, "Invalid query parameters", err)
		return false
	}
	return true
}

// UUIDParam returns the path parameter name when it is a valid UUID.
// Anything else gets a 400 here, before it can reach a UUID column and
// come back from Postgres as a 500.
func UUIDParam(c *gin.Context, name string) (string, bool) {
	value := c.Param(name)
	parsed, err := uuid.Parse(value)
	if err != nil {
		response.Error(c, http.StatusBadRequest, fmt.Sprintf("Invalid %s", name), "must be a UUID")
		return "", false
	}
	return parsed.String(), true
}

// writeBindError answers 400 with one message per invalid field, named the
// way the client sent it. It never echoes Go type or struct names.
func writeBindError(c *gin.Context, message string, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.Error(c, http.StatusRequestEntityTooLarge, "Request body too large",
			fmt.Sprintf("limit is %d bytes", tooLarge.Limit))
		return
	}

	var validationErrs playground.ValidationErrors
	if errors.As(err, &validationErrs) {
		fields := make(map[string][]string, len(validationErrs))
		for _, fe := range validationErrs {
			fields[fe.Field()] = append(fields[fe.Field()], describe(fe))
		}
		response.ValidationError(c, http.StatusBadRequest, message, fields)
		return
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		response.ValidationError(c, http.StatusBadRequest, message, map[string][]string{
			typeErr.Field: {"has the wrong type"},
		})
		return
	}

	response.Error(c, http.StatusBadRequest, message, "Body or parameters could not be parsed")
}

// describe turns one failed rule into a short sentence.
func describe(fe playground.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4":
		return "must be a UUID"
	case "url":
		return "must be a valid URL"
	case "username":
		return "must be 3-50 letters, digits, dots, underscores or hyphens, starting with a letter or digit"
	case "password":
		return "must be 8-72 bytes with an uppercase letter, a lowercase letter and a number, and not a common password"
	case "oneof":
		return "must be one of: " + fe.Param()
	case "len":
		if isString(fe) {
			return "must be exactly " + fe.Param() + " characters"
		}
		return "must have exactly " + fe.Param() + " item(s)"
	case "numeric":
		return "must contain only digits"
	case "ip|cidr":
		return "must be an IP address or CIDR range"
	case "datetime":
		return "must be an RFC 3339 timestamp, e.g. 2026-12-31T23:59:59Z"
	case "min":
		if isCollection(fe) {
			return "must contain at least " + fe.Param() + " item(s)"
		}
		if isString(fe) {
			return "must be at least " + fe.Param() + " characters"
		}
		return "must be at least " + fe.Param()
	case "max":
		if isCollection(fe) {
			return "must contain at most " + fe.Param() + " item(s)"
		}
		if isString(fe) {
			return "must be at most " + fe.Param() + " characters"
		}
		return "must be at most " + fe.Param()
	default:
		return "is invalid (" + fe.Tag() + ")"
	}
}

func isString(fe playground.FieldError) bool {
	return fe.Kind() == reflect.String
}

func isCollection(fe playground.FieldError) bool {
	switch fe.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return true
	}
	return false
}
