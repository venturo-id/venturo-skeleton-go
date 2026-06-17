package response

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

// production gates whether internal error detail may reach the response body.
// Set once at startup via Configure.
var production bool

// Configure sets the renderer's environment. Call once during router bootstrap
// with cfg.Server.Env.
func Configure(env string) {
	production = env == "production"
}

type httpCase struct {
	status  int
	message string
}

// cases maps a domain error code to its HTTP status and generic message.
// Adding a new code is a one-line change here (see docs/errors.md).
var cases = map[derrors.ErrorCode]httpCase{
	derrors.ErrorCodeNotFound:           {http.StatusNotFound, "Data Not Found"},
	derrors.ErrorCodeInvalidArgument:    {http.StatusBadRequest, "Bad Request"},
	derrors.ErrorCodeDuplicate:          {http.StatusBadRequest, "Duplicate data"},
	derrors.ErrorCodeTokenExpired:       {http.StatusBadRequest, "Token is expired"},
	derrors.ErrorCodeUnauthorized:       {http.StatusUnauthorized, "Unauthorized"},
	derrors.ErrorCodeForbidden:          {http.StatusForbidden, "Forbidden"},
	derrors.ErrorCodeRequestTimeout:     {http.StatusRequestTimeout, "Request Timeout"},
	derrors.ErrorCodeAlreadyExists:      {http.StatusConflict, "Already exists"},
	derrors.ErrorCodeConflict:           {http.StatusConflict, "Conflict"},
	derrors.ErrorCodeServiceUnavailable: {http.StatusServiceUnavailable, "Service unavailable"},
}

// RenderError is the single bridge from an error to an HTTP response. It maps a
// *derrors.Error code to {status, message}, always logs the underlying cause for
// the audit trail, and never leaks internal detail to the body in production.
//
// Any non-*derrors.Error is treated as an unexpected internal failure: logged in
// full, rendered as a generic 500.
func RenderError(c *gin.Context, err error) {
	var ierr *derrors.Error
	if !errors.As(err, &ierr) {
		// Not a domain error → generic 500, detail only to the log.
		logger.Error("unhandled error", logger.Err(err))
		Error(c, http.StatusInternalServerError, "Internal server error", "")
		return
	}

	// Always record the real cause for the audit trail.
	logger.Error("request error", logger.Err(err))

	switch ierr.Code() {
	case derrors.ErrorCodeCustomBadRequest:
		Error(c, http.StatusBadRequest, ierr.Message(), "")
	case derrors.ErrorCodeCustomNotFound:
		Error(c, http.StatusNotFound, ierr.Message(), "")
	case derrors.ErrorCodeCustomAlreadyExists:
		Error(c, http.StatusConflict, ierr.Message(), "")
	case derrors.ErrorCodeCustomForbidden:
		Error(c, http.StatusForbidden, ierr.Message(), "")
	case derrors.ErrorCodeUnknown, derrors.ErrorCodeCustomInternalServer:
		detail := ""
		if !production { // dev/staging may see detail; production may not.
			detail = ierr.Error()
		}
		Error(c, http.StatusInternalServerError, "Internal server error", detail)
	default:
		hc, ok := cases[ierr.Code()]
		if !ok {
			Error(c, http.StatusInternalServerError, "Internal server error", "")
			return
		}
		Error(c, hc.status, hc.message, "")
	}
}
