package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	authService "timebox-be/internal/modules/core/auth/service"
	"timebox-be/internal/modules/core/user/dto"
	"timebox-be/internal/modules/core/user/service"
	"timebox-be/internal/shared/request"
	"timebox-be/internal/shared/response"
)

// Error codes of a refused avatar upload.
const (
	CodeInvalidImage         = "invalid_image"
	CodeStorageNotConfigured = "storage_not_configured"
)

type UserHandler struct {
	userService *service.UserService
}

func NewUserHandler(userService *service.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

func (h *UserHandler) GetAll(c *gin.Context) {
	var params dto.UserQueryParams
	if !request.BindQuery(c, &params) {
		return
	}

	users, total, err := h.userService.List(c.Request.Context(), &params)
	if err != nil {
		h.handleError(c, err, "Failed to get users")
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Users retrieved successfully",
		users, params.Page, params.Limit, total)
}

func (h *UserHandler) GetByID(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	result, err := h.userService.GetByID(c.Request.Context(), id)
	if err != nil {
		h.handleError(c, err, "Failed to get user")
		return
	}

	response.Success(c, http.StatusOK, "User retrieved successfully", result)
}

func (h *UserHandler) Create(c *gin.Context) {
	var req dto.CreateUserRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.userService.Create(c.Request.Context(), &req, middleware.MustGetUserID(c), middleware.Caller(c))
	if err != nil {
		h.handleError(c, err, "Failed to create user")
		return
	}

	response.Success(c, http.StatusCreated, "User created successfully", result)
}

func (h *UserHandler) Update(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	var req dto.UpdateUserRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.userService.Update(c.Request.Context(), id, &req, middleware.MustGetUserID(c), middleware.Caller(c))
	if err != nil {
		h.handleError(c, err, "Failed to update user")
		return
	}

	response.Success(c, http.StatusOK, "User updated successfully", result)
}

func (h *UserHandler) Delete(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.userService.Delete(c.Request.Context(), id, middleware.MustGetUserID(c), middleware.Caller(c)); err != nil {
		h.handleError(c, err, "Failed to delete user")
		return
	}

	response.Success(c, http.StatusOK, "User deleted successfully", nil)
}

func (h *UserHandler) GetMe(c *gin.Context) {
	result, err := h.userService.GetByID(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.handleError(c, err, "Failed to get user")
		return
	}

	response.Success(c, http.StatusOK, "User retrieved successfully", result)
}

func (h *UserHandler) UpdateMe(c *gin.Context) {
	var req dto.UpdateMeRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.userService.UpdateMe(c.Request.Context(), middleware.MustGetUserID(c), &req)
	if err != nil {
		h.handleError(c, err, "Failed to update user")
		return
	}

	response.Success(c, http.StatusOK, "User updated successfully", result)
}

func (h *UserHandler) ChangePassword(c *gin.Context) {
	var req dto.ChangePasswordRequest
	if !request.BindJSON(c, &req) {
		return
	}

	if err := h.userService.ChangePassword(c.Request.Context(), middleware.MustGetUserID(c), &req); err != nil {
		h.handleError(c, err, "Failed to change password")
		return
	}

	response.Success(c, http.StatusOK, "Password changed successfully. Please sign in again.", nil)
}

// UploadAvatar replaces the caller's avatar with the image in the
// multipart field "file".
func (h *UserHandler) UploadAvatar(c *gin.Context) {
	data, ok := readAvatarFile(c)
	if !ok {
		return
	}

	result, err := h.userService.UploadAvatar(c.Request.Context(), middleware.MustGetUserID(c), data)
	if err != nil {
		h.handleError(c, err, "Failed to upload avatar")
		return
	}

	response.Success(c, http.StatusOK, "Avatar updated successfully", result)
}

// RemoveAvatar clears the caller's avatar.
func (h *UserHandler) RemoveAvatar(c *gin.Context) {
	result, err := h.userService.RemoveAvatar(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.handleError(c, err, "Failed to remove avatar")
		return
	}

	response.Success(c, http.StatusOK, "Avatar removed successfully", result)
}

// readAvatarFile reads the multipart field "file" whole; the body limit
// bounds it. It writes the error response itself and returns false: 413
// past the body limit, 400 when the field is missing or the body is not a
// multipart form.
func readAvatarFile(c *gin.Context) ([]byte, bool) {
	header, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			response.ErrorCode(c, http.StatusRequestEntityTooLarge, response.CodePayloadTooLarge, "Request body too large",
				fmt.Sprintf("limit is %d bytes", tooLarge.Limit))
		case errors.Is(err, http.ErrMissingFile), errors.Is(err, http.ErrNotMultipart):
			response.ValidationError(c, http.StatusBadRequest, "Invalid request payload",
				map[string][]string{"file": {"is required"}})
		default:
			response.Error(c, http.StatusBadRequest, "Invalid request payload", "Body or parameters could not be parsed")
		}
		return nil, false
	}

	file, err := header.Open()
	if err != nil {
		response.InternalError(c, "Failed to read the uploaded file", err)
		return nil, false
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		response.InternalError(c, "Failed to read the uploaded file", err)
		return nil, false
	}
	return data, true
}

// handleError maps service errors to responses. Anything unexpected is a
// 500 carrying only fallback.
func (h *UserHandler) handleError(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, service.ErrUserNotFound):
		response.Error(c, http.StatusNotFound, "User not found", "")
	case errors.Is(err, service.ErrEmailAlreadyExists):
		response.ErrorCode(c, http.StatusConflict, "email_taken", "Email already exists", "")
	case errors.Is(err, service.ErrUsernameAlreadyExists):
		response.ErrorCode(c, http.StatusConflict, "username_taken", "Username already exists", "")
	case errors.Is(err, authService.ErrIncorrectPassword):
		response.ErrorCode(c, http.StatusBadRequest, "incorrect_password", "Current password is incorrect", "")
	case errors.Is(err, authService.ErrNoPassword):
		response.ErrorCode(c, http.StatusConflict, "no_password", "This account has no password yet",
			"Set one through forgot password")
	case errors.Is(err, authService.ErrPasswordLocked):
		response.ErrorCode(c, http.StatusTooManyRequests, "account_locked", "Too many incorrect passwords",
			"Try again later, or reset your password")
	case errors.Is(err, authService.ErrUserNotActive):
		response.ErrorCode(c, http.StatusUnauthorized, middleware.CodeUserInactive, "User account is not active", "")
	case errors.Is(err, service.ErrRoleNotFound):
		response.Error(c, http.StatusBadRequest, "Invalid role_id", "")
	case errors.Is(err, service.ErrRoleNotAllowed):
		response.Error(c, http.StatusForbidden, "Role not allowed for caller", "")
	case errors.Is(err, service.ErrCannotManageSuperAdmin):
		response.Error(c, http.StatusForbidden, "Cannot manage a super admin user", "")
	case errors.Is(err, service.ErrCannotManageHigherUser):
		response.Error(c, http.StatusForbidden, "Cannot manage a user with permissions you do not hold", "")
	case errors.Is(err, service.ErrCannotChangeOwnRole):
		response.Error(c, http.StatusForbidden, "Cannot change your own role", "")
	case errors.Is(err, service.ErrCannotVerifyOwnEmail):
		response.Error(c, http.StatusForbidden, "Cannot change the verification of your own email", "")
	case errors.Is(err, service.ErrChangeOwnEmail):
		response.Error(c, http.StatusForbidden, "Cannot change your own email here",
			"Use POST /core/v1/account/email, which confirms the new address")
	case errors.Is(err, service.ErrCannotRemoveSelf):
		response.Error(c, http.StatusForbidden, "Cannot deactivate or delete your own account",
			"Use POST /core/v1/account/delete to close your account")
	case errors.Is(err, service.ErrEmailUnverified):
		response.Error(c, http.StatusConflict, "The user's email is not verified",
			"Ask the user to verify their email, or set is_email_verified to true if you can vouch for the address")
	case errors.Is(err, service.ErrInvalidImage):
		response.ErrorCode(c, http.StatusBadRequest, CodeInvalidImage, "The file is not a supported image",
			"Upload a JPEG, PNG or WebP image up to 4096×4096")
	case errors.Is(err, service.ErrStorageNotConfigured):
		response.ErrorCode(c, http.StatusServiceUnavailable, CodeStorageNotConfigured, "Avatar storage is not configured", "")
	default:
		response.InternalError(c, fallback, err)
	}
}
