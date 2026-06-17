package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/user/dto"
	"venturo-skeleton-go/internal/modules/core/user/service"
	"venturo-skeleton-go/internal/shared/response"
)

type UserHandler struct {
	userService     *service.UserService
	companyVerifier middleware.CompanyContextVerifier
}

func NewUserHandler(userService *service.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

// SetCompanyVerifier wires the live company-membership verifier so that
// resolveTenantScope can reject stale JWTs (caller no longer member of the
// company in their JWT, or the company was soft-deleted/deactivated).
func (h *UserHandler) SetCompanyVerifier(v middleware.CompanyContextVerifier) {
	h.companyVerifier = v
}

// errStaleCompanyContext signals that the caller's JWT references a company
// that is no longer valid (deleted/deactivated) or that the caller has lost
// membership of. Mapped to 403 in handlers.
var errStaleCompanyContext = errors.New("stale company context")

// writeScopeError renders a 403 with an appropriate message based on which
// failure mode resolveTenantScope returned.
func writeScopeError(c *gin.Context, err error) {
	if errors.Is(err, errStaleCompanyContext) {
		response.Error(c, http.StatusForbidden, "Company context is no longer valid for this user", "stale_company_context")
		return
	}
	response.Error(c, http.StatusForbidden, "Company context required", "")
}

// resolveTenantScope returns the tenant scope that must be applied to the
// current caller, or nil for super admins (who see everything). Returns an
// error when a non-super-admin caller has no company context — the caller
// must have switched into a company before hitting these endpoints.
//
// When a company verifier is wired (production), it also re-checks against
// the database that the company still exists, is active, and that the caller
// is still a member of it.
func (h *UserHandler) resolveTenantScope(c *gin.Context) (*service.TenantScope, error) {
	claims, _ := middleware.GetUserFromContext(c)
	if claims != nil && claims.HasRole("super_admin") {
		return nil, nil
	}
	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		return nil, errors.New("company context required")
	}
	userID := middleware.MustGetUserID(c)
	if h.companyVerifier != nil {
		if err := h.companyVerifier.VerifyMembership(c.Request.Context(), userID, companyID); err != nil {
			return nil, errStaleCompanyContext
		}
	}
	return &service.TenantScope{
		CompanyID:    companyID,
		CallerUserID: userID,
	}, nil
}

func (h *UserHandler) GetAll(c *gin.Context) {
	var params dto.UserQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	scope, err := h.resolveTenantScope(c)
	if err != nil {
		writeScopeError(c, err)
		return
	}

	ctx := c.Request.Context()
	result, err := h.userService.GetAll(ctx, &params, scope)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Users retrieved successfully",
		result.Users, result.Page, result.Limit, result.Total)
}

func (h *UserHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	scope, err := h.resolveTenantScope(c)
	if err != nil {
		writeScopeError(c, err)
		return
	}

	ctx := c.Request.Context()
	result, err := h.userService.GetByID(ctx, id, scope)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User retrieved successfully", result)
}

func (h *UserHandler) Create(c *gin.Context) {
	var req dto.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	// Enforce tenant scope: non-super-admin users can only assign the new
	// user to their current company. Ignore any company_ids sent in the body
	// to prevent adding users to tenants the caller does not own.
	scope, err := h.resolveTenantScope(c)
	if err != nil {
		writeScopeError(c, err)
		return
	}
	if scope != nil {
		req.CompanyIDs = []string{scope.CompanyID}
	}

	createdBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.userService.Create(ctx, &req, createdBy, scope)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "User created successfully", result)
}

func (h *UserHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	var req dto.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	// Non-super-admin cannot modify a user's company memberships through this
	// endpoint. SyncUserCompanies replaces the full set, so accepting it here
	// would let a company admin evict a user from other tenants they don't own.
	scope, err := h.resolveTenantScope(c)
	if err != nil {
		writeScopeError(c, err)
		return
	}
	if scope != nil {
		req.CompanyIDs = nil
	}

	updatedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.userService.Update(ctx, id, &req, updatedBy, scope)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User updated successfully", result)
}

func (h *UserHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	scope, err := h.resolveTenantScope(c)
	if err != nil {
		writeScopeError(c, err)
		return
	}

	deletedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err = h.userService.Delete(ctx, id, deletedBy, scope)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User deleted successfully", nil)
}

func (h *UserHandler) GetMe(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.userService.GetMe(ctx, userID)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User retrieved successfully", result)
}

func (h *UserHandler) UpdateMe(c *gin.Context) {
	var req dto.UpdateMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	userID := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.userService.UpdateMe(ctx, userID, &req)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User updated successfully", result)
}

func (h *UserHandler) ChangePassword(c *gin.Context) {
	var req dto.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	userID := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err := h.userService.ChangePassword(ctx, userID, &req)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Password changed successfully", nil)
}
