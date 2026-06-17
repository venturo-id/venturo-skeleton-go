package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	branchDomain "venturo-skeleton-go/internal/modules/core/branch/domain"
	companyDomain "venturo-skeleton-go/internal/modules/core/company/domain"
	companyDto "venturo-skeleton-go/internal/modules/core/company/dto"
	roleDomain "venturo-skeleton-go/internal/modules/core/role/domain"
	"venturo-skeleton-go/internal/modules/core/user/domain"
	"venturo-skeleton-go/internal/modules/core/user/dto"
	"venturo-skeleton-go/internal/modules/core/user/repository"
	"venturo-skeleton-go/pkg/crypto"
	"venturo-skeleton-go/pkg/derrors"
	jwtpkg "venturo-skeleton-go/pkg/jwt"
	"venturo-skeleton-go/pkg/logger"
)

var (
	ErrUserNotFound          = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "user not found")
	ErrEmailAlreadyExists    = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Email already exists")
	ErrUsernameAlreadyExists = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Username already exists")
	ErrInvalidPassword       = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "invalid password")
	ErrRoleNotAllowed        = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Role is not allowed for caller")
	ErrCompaniesRequired     = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "User must be assigned to at least one company")
)

// TenantScope limits user visibility for non-super-admin callers. Pass nil to
// bypass tenant filtering (super admin). See
// UserRepository.FindAll / IsUserVisibleToScope for the exact visibility rules.
type TenantScope struct {
	CompanyID    string // anchor: caller's current company (root of the visible subtree)
	CallerUserID string // used for the self-created fallback
}

// CompanySyncer syncs user-company memberships and retrieves company data (implemented by company service)
type CompanySyncer interface {
	SyncUserCompanies(ctx context.Context, userID string, req *companyDto.SyncUserCompaniesRequest, updatedBy string) error
	AssignUserToCompaniesTx(ctx context.Context, tx pgx.Tx, userID string, req *companyDto.SyncUserCompaniesRequest, createdBy string) error
	GetUserCompaniesWithDetails(ctx context.Context, userID string) ([]companyDomain.UserCompanyDetail, error)
}

// BranchSyncer syncs user-branch access and retrieves branch detail data
// (implemented by branch/service.UserBranchService). Kept as a narrow
// interface so the user service does not import the branch service package.
type BranchSyncer interface {
	SyncUserBranches(ctx context.Context, userID string, branchIDs []string, updatedBy string, scopeCompanyID *string) error
	AssignUserToBranchesTx(ctx context.Context, tx pgx.Tx, userID string, branchIDs []string, createdBy string, scopeCompanyID *string) error
	GetUserBranchesWithDetails(ctx context.Context, userID string) ([]branchDomain.UserBranchDetail, error)
}

// RoleLookup reads a role by ID. Kept as a narrow interface so the user
// service does not pull in the full role service package.
type RoleLookup interface {
	FindByID(ctx context.Context, id string) (*roleDomain.Role, error)
}

type UserService struct {
	userRepo      *repository.UserRepository
	companySyncer CompanySyncer
	branchSyncer  BranchSyncer
	roleLookup    RoleLookup
}

func NewUserService(userRepo *repository.UserRepository) *UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

// SetCompanySyncer sets the company syncer dependency (wired from router)
func (s *UserService) SetCompanySyncer(syncer CompanySyncer) {
	s.companySyncer = syncer
}

// SetBranchSyncer sets the branch access syncer dependency (wired from router)
func (s *UserService) SetBranchSyncer(syncer BranchSyncer) {
	s.branchSyncer = syncer
}

// SetRoleLookup sets the role lookup dependency (wired from router). Required
// when tenant scope enforcement needs to validate role_id against caller's
// scope — see validateRoleForScope.
func (s *UserService) SetRoleLookup(lookup RoleLookup) {
	s.roleLookup = lookup
}

// validateRoleForScope ensures a non-super-admin caller can only assign a
// role that is either (a) owned by their current company, or (b) a global
// system role that is NOT super_admin. Pass scope == nil to bypass (super
// admin). Nil roleID is allowed — it means "no role" and is always valid.
func (s *UserService) validateRoleForScope(ctx context.Context, roleID *string, scope *TenantScope) error {
	if roleID == nil || scope == nil {
		return nil
	}
	if s.roleLookup == nil {
		// Fail closed: if the dependency was not wired, refuse to allow a
		// non-super-admin caller to pin an unverified role onto a user.
		return ErrRoleNotAllowed
	}
	// A missing role is a validation failure here (fail-closed 403), not a
	// surfaced NotFound. FindByID now wraps absence as NotFound (unwraps to
	// pgx.ErrNoRows) — treat that as "no such role" and fall through to
	// ErrRoleNotAllowed; only propagate genuine query failures.
	role, err := s.roleLookup.FindByID(ctx, *roleID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if role == nil {
		return ErrRoleNotAllowed
	}
	// Global roles: allowed except super_admin.
	if role.CompanyID == nil {
		if role.Code == jwtpkg.RoleSuperAdmin {
			return ErrRoleNotAllowed
		}
		return nil
	}
	// Tenant-owned role: must match caller's current company exactly.
	if *role.CompanyID != scope.CompanyID {
		return ErrRoleNotAllowed
	}
	return nil
}

// GetAll returns paginated list of users. If scope is non-nil, tenant
// visibility filters are applied; pass nil for super-admin callers.
func (s *UserService) GetAll(ctx context.Context, params *dto.UserQueryParams, scope *TenantScope) (*dto.UserListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	if scope != nil {
		companyID := scope.CompanyID
		callerID := scope.CallerUserID
		params.ScopeCompanyID = &companyID
		params.ScopeCreatedBy = &callerID
	} else {
		params.ScopeCompanyID = nil
		params.ScopeCreatedBy = nil
	}

	users, total, err := s.userRepo.FindAll(ctx, params)
	if err != nil {
		return nil, err
	}

	userResponses := make([]dto.UserResponse, len(users))
	for i, user := range users {
		resp := s.toUserResponse(&user)
		s.enrichWithCompanyData(ctx, &resp, user.ID)
		userResponses[i] = resp
	}

	return &dto.UserListResponse{
		Users: userResponses,
		Total: total,
		Page:  params.Page,
		Limit: params.Limit,
	}, nil
}

// GetByID returns a user by ID. If scope is non-nil, the target user must be
// visible to the caller's tenant scope; otherwise ErrUserNotFound is returned
// (we deliberately do NOT return a 403 — leaking the existence of users in
// other tenants would itself be a minor info-disclosure).
func (s *UserService) GetByID(ctx context.Context, id string, scope *TenantScope) (*dto.UserResponse, error) {
	if scope != nil {
		visible, err := s.userRepo.IsUserVisibleToScope(ctx, id, scope.CompanyID, scope.CallerUserID)
		if err != nil {
			return nil, err
		}
		if !visible {
			return nil, ErrUserNotFound
		}
	}

	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	response := s.toUserResponse(user)
	s.enrichWithCompanyData(ctx, &response, user.ID)
	return &response, nil
}

// Create creates a new user. The insert into core.users AND the assignment
// into core.company_users run inside a single transaction so the flow is
// atomic: if membership assignment fails, the user row is rolled back and
// the caller receives a real error (previously the error was silently
// swallowed and a user could end up with zero memberships — "orphan").
//
// When scope is non-nil (non-super-admin caller), role_id is validated to
// ensure the caller cannot pin a role they have no authority over (e.g. a
// role belonging to another tenant, or the global super_admin role).
func (s *UserService) Create(ctx context.Context, req *dto.CreateUserRequest, createdBy string, scope *TenantScope) (*dto.UserResponse, error) {
	// Policy: every user must belong to at least one company. The handler
	// already injects the caller's current company for non-super-admin, so
	// in practice this only fires for super_admin callers who forgot to
	// pass company_ids in the body.
	if len(req.CompanyIDs) == 0 {
		return nil, ErrCompaniesRequired
	}

	if err := s.validateRoleForScope(ctx, req.RoleID, scope); err != nil {
		return nil, err
	}

	// Check email uniqueness
	exists, err := s.userRepo.EmailExists(ctx, req.Email, nil)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailAlreadyExists
	}

	// Check username uniqueness
	exists, err = s.userRepo.UsernameExists(ctx, req.Username, nil)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUsernameAlreadyExists
	}

	// Hash password
	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		logger.Error("Failed to hash password", logger.Err(err))
		return nil, err
	}

	now := time.Now()
	user := &domain.User{
		ID:              uuid.New().String(),
		Email:           req.Email,
		Username:        req.Username,
		PasswordHash:    passwordHash,
		FullName:        req.FullName,
		Phone:           req.Phone,
		IsActive:        true,
		IsEmailVerified: true,
		CreatedAt:       now,
		CreatedBy:       &createdBy,
		UpdatedAt:       now,
		UpdatedBy:       &createdBy,
	}

	tx, err := s.userRepo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	// Rollback is a no-op after a successful Commit, so it's always safe to
	// defer it unconditionally.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.userRepo.CreateTx(ctx, tx, user); err != nil {
		return nil, err
	}

	// Assign company memberships in the SAME transaction so user + memberships
	// commit or roll back together.
	if len(req.CompanyIDs) > 0 {
		if s.companySyncer == nil {
			return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company syncer not configured")
		}
		syncReq := &companyDto.SyncUserCompaniesRequest{
			CompanyIDs: req.CompanyIDs,
			RoleID:     req.RoleID,
		}
		if err := s.companySyncer.AssignUserToCompaniesTx(ctx, tx, user.ID, syncReq, createdBy); err != nil {
			return nil, err
		}
	}

	// Assign branch access in the same transaction. Branch validation enforces
	// that every branch exists and (for non-super-admin) belongs to the
	// caller's scope company — any violation rolls the user row back.
	if len(req.BranchIDs) > 0 {
		if s.branchSyncer == nil {
			return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "branch syncer not configured")
		}
		var scopeCompanyID *string
		if scope != nil {
			c := scope.CompanyID
			scopeCompanyID = &c
		}
		if err := s.branchSyncer.AssignUserToBranchesTx(ctx, tx, user.ID, req.BranchIDs, createdBy, scopeCompanyID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	response := s.toUserResponse(user)
	return &response, nil
}

// Update updates a user. If scope is non-nil, the target user must be
// visible to the caller's tenant scope.
func (s *UserService) Update(ctx context.Context, id string, req *dto.UpdateUserRequest, updatedBy string, scope *TenantScope) (*dto.UserResponse, error) {
	if err := s.validateRoleForScope(ctx, req.RoleID, scope); err != nil {
		return nil, err
	}

	if scope != nil {
		visible, err := s.userRepo.IsUserVisibleToScope(ctx, id, scope.CompanyID, scope.CallerUserID)
		if err != nil {
			return nil, err
		}
		if !visible {
			return nil, ErrUserNotFound
		}
	}

	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	// Check email uniqueness if changed
	if req.Email != nil && *req.Email != user.Email {
		exists, err := s.userRepo.EmailExists(ctx, *req.Email, &id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrEmailAlreadyExists
		}
		user.Email = *req.Email
	}

	// Check username uniqueness if changed
	if req.Username != nil && *req.Username != user.Username {
		exists, err := s.userRepo.UsernameExists(ctx, *req.Username, &id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrUsernameAlreadyExists
		}
		user.Username = *req.Username
	}

	if req.FullName != nil {
		user.FullName = req.FullName
	}
	if req.Phone != nil {
		user.Phone = req.Phone
	}
	if req.AvatarURL != nil {
		user.AvatarURL = req.AvatarURL
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}

	user.UpdatedAt = time.Now()
	user.UpdatedBy = &updatedBy

	err = s.userRepo.Update(ctx, user)
	if err != nil {
		return nil, err
	}

	// Sync company memberships if provided. Errors are now propagated so a
	// partial failure is surfaced to the API caller instead of being silently
	// dropped.
	if req.CompanyIDs != nil && s.companySyncer != nil {
		syncReq := &companyDto.SyncUserCompaniesRequest{
			CompanyIDs: req.CompanyIDs,
			RoleID:     req.RoleID,
		}
		if err := s.companySyncer.SyncUserCompanies(ctx, id, syncReq, updatedBy); err != nil {
			return nil, err
		}
	}

	// Sync branch access if provided. A non-nil pointer means "replace";
	// a nil pointer means "don't touch branch assignments".
	if req.BranchIDs != nil && s.branchSyncer != nil {
		var scopeCompanyID *string
		if scope != nil {
			c := scope.CompanyID
			scopeCompanyID = &c
		}
		if err := s.branchSyncer.SyncUserBranches(ctx, id, *req.BranchIDs, updatedBy, scopeCompanyID); err != nil {
			return nil, err
		}
	}

	response := s.toUserResponse(user)
	return &response, nil
}

// Delete soft deletes a user. If scope is non-nil, the target user must be
// visible to the caller's tenant scope.
func (s *UserService) Delete(ctx context.Context, id, deletedBy string, scope *TenantScope) error {
	if scope != nil {
		visible, err := s.userRepo.IsUserVisibleToScope(ctx, id, scope.CompanyID, scope.CallerUserID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrUserNotFound
		}
	}

	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}

	return s.userRepo.SoftDelete(ctx, id, deletedBy)
}

// GetMe returns current user profile. Tenant scope is never applied — a user
// can always see their own record regardless of company memberships.
func (s *UserService) GetMe(ctx context.Context, userID string) (*dto.UserResponse, error) {
	return s.GetByID(ctx, userID, nil)
}

// UpdateMe updates current user profile
func (s *UserService) UpdateMe(ctx context.Context, userID string, req *dto.UpdateMeRequest) (*dto.UserResponse, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	if req.FullName != nil {
		user.FullName = req.FullName
	}
	if req.Phone != nil {
		user.Phone = req.Phone
	}
	if req.AvatarURL != nil {
		user.AvatarURL = req.AvatarURL
	}

	user.UpdatedAt = time.Now()
	user.UpdatedBy = &userID

	err = s.userRepo.Update(ctx, user)
	if err != nil {
		return nil, err
	}

	response := s.toUserResponse(user)
	return &response, nil
}

// ChangePassword changes user password
func (s *UserService) ChangePassword(ctx context.Context, userID string, req *dto.ChangePasswordRequest) error {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}

	// Verify current password
	if !crypto.ComparePassword(user.PasswordHash, req.CurrentPassword) {
		return ErrInvalidPassword
	}

	// Hash new password
	newPasswordHash, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		logger.Error("Failed to hash new password", logger.Err(err))
		return err
	}

	return s.userRepo.UpdatePassword(ctx, userID, newPasswordHash, userID)
}

// toUserResponse converts domain.User to dto.UserResponse
func (s *UserService) toUserResponse(user *domain.User) dto.UserResponse {
	return dto.UserResponse{
		ID:              user.ID,
		Email:           user.Email,
		Username:        user.Username,
		FullName:        user.FullName,
		Phone:           user.Phone,
		AvatarURL:       user.AvatarURL,
		IsActive:        user.IsActive,
		IsEmailVerified: user.IsEmailVerified,
		LastLoginAt:     user.LastLoginAt,
		CreatedAt:       user.CreatedAt,
		UpdatedAt:       user.UpdatedAt,
	}
}

// enrichWithCompanyData adds company memberships, branch access, and role
// name to user response. Failures in individual lookups are swallowed — the
// base user data is still returned.
func (s *UserService) enrichWithCompanyData(ctx context.Context, resp *dto.UserResponse, userID string) {
	if s.companySyncer != nil {
		if details, err := s.companySyncer.GetUserCompaniesWithDetails(ctx, userID); err == nil {
			companies := make([]dto.UserCompanyItem, len(details))
			for i, d := range details {
				companies[i] = dto.UserCompanyItem{
					CompanyID:   d.CompanyID,
					CompanyName: d.CompanyName,
					IsOwner:     d.OwnerID == userID,
				}
				// Use the first role name found as the user's role
				if resp.RoleName == nil && d.RoleName != nil {
					resp.RoleName = d.RoleName
				}
			}
			resp.Companies = companies
		}
	}

	if s.branchSyncer != nil {
		if details, err := s.branchSyncer.GetUserBranchesWithDetails(ctx, userID); err == nil {
			branches := make([]dto.UserBranchItem, len(details))
			for i, d := range details {
				branches[i] = dto.UserBranchItem{
					BranchID:   d.BranchID,
					BranchCode: d.BranchCode,
					BranchName: d.BranchName,
					CompanyID:  d.CompanyID,
				}
			}
			resp.Branches = branches
		}
	}
}
