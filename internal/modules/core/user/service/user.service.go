package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	roleDomain "venturo-skeleton-go-simple/internal/modules/core/role/domain"
	"venturo-skeleton-go-simple/internal/modules/core/user/domain"
	"venturo-skeleton-go-simple/internal/modules/core/user/dto"
	"venturo-skeleton-go-simple/internal/modules/core/user/repository"
	"venturo-skeleton-go-simple/pkg/crypto"
	jwtpkg "venturo-skeleton-go-simple/pkg/jwt"
	"venturo-skeleton-go-simple/pkg/logger"
)

var (
	ErrUserNotFound           = errors.New("user not found")
	ErrEmailAlreadyExists     = errors.New("email already exists")
	ErrUsernameAlreadyExists  = errors.New("username already exists")
	ErrInvalidPassword        = errors.New("invalid password")
	ErrRoleNotFound           = errors.New("role not found")
	ErrRoleNotAllowed         = errors.New("role is not allowed for caller")
	ErrCannotManageSuperAdmin = errors.New("only a super admin can manage a super admin user")
)

// RoleLookup reads roles and manages user role assignments (implemented by
// role repository). Kept as a narrow interface so the user service does not
// pull in the full role package.
type RoleLookup interface {
	FindByID(ctx context.Context, id string) (*roleDomain.Role, error)
	GetUserRoles(ctx context.Context, userID string) ([]roleDomain.Role, error)
	AssignRoleToUserTx(ctx context.Context, tx pgx.Tx, userID, roleID, createdBy string) error
	ReplaceUserRoles(ctx context.Context, userID, roleID, createdBy string) error
}

// PermissionCacheInvalidator drops a user's cached permission set after
// their role assignment changes. Implemented by *authz.Service.
type PermissionCacheInvalidator interface {
	InvalidateUser(ctx context.Context, userID string) error
}

type UserService struct {
	userRepo    *repository.UserRepository
	roleLookup  RoleLookup
	invalidator PermissionCacheInvalidator // optional; nil is a no-op
}

func NewUserService(userRepo *repository.UserRepository) *UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

// SetRoleLookup sets the role lookup dependency (wired from router). Required
// whenever a request carries role_id.
func (s *UserService) SetRoleLookup(lookup RoleLookup) {
	s.roleLookup = lookup
}

// SetPermissionCacheInvalidator wires the Redis-backed authz cache so role
// changes made through this service take effect on the next request.
func (s *UserService) SetPermissionCacheInvalidator(inv PermissionCacheInvalidator) {
	s.invalidator = inv
}

// invalidateUser is a nil-safe wrapper — a failed invalidation shouldn't
// fail the write that triggered it; TTL will eventually heal the cache.
func (s *UserService) invalidateUser(ctx context.Context, userID string) {
	if s.invalidator == nil {
		return
	}
	if err := s.invalidator.InvalidateUser(ctx, userID); err != nil {
		logger.Warn("authz cache invalidation failed",
			logger.String("user_id", userID),
			logger.Err(err))
	}
}

// validateRole ensures roleID points to an existing role and that a
// non-super-admin caller is not handing out the super_admin role. Nil roleID
// is allowed — it means "no role change".
func (s *UserService) validateRole(ctx context.Context, roleID *string, callerIsSuperAdmin bool) error {
	if roleID == nil {
		return nil
	}
	if s.roleLookup == nil {
		return errors.New("role lookup not configured")
	}
	role, err := s.roleLookup.FindByID(ctx, *roleID)
	if err != nil {
		return err
	}
	if role == nil {
		return ErrRoleNotFound
	}
	if role.Code == jwtpkg.RoleSuperAdmin && !callerIsSuperAdmin {
		return ErrRoleNotAllowed
	}
	return nil
}

// guardSuperAdminTarget stops a non-super-admin caller from modifying or
// deleting a user who holds the super_admin role.
func (s *UserService) guardSuperAdminTarget(ctx context.Context, userID string, callerIsSuperAdmin bool) error {
	if callerIsSuperAdmin || s.roleLookup == nil {
		return nil
	}
	roles, err := s.roleLookup.GetUserRoles(ctx, userID)
	if err != nil {
		return err
	}
	for _, r := range roles {
		if r.Code == jwtpkg.RoleSuperAdmin {
			return ErrCannotManageSuperAdmin
		}
	}
	return nil
}

// GetAll returns paginated list of users
func (s *UserService) GetAll(ctx context.Context, params *dto.UserQueryParams) (*dto.UserListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	users, total, err := s.userRepo.FindAll(ctx, params)
	if err != nil {
		return nil, err
	}

	userResponses := make([]dto.UserResponse, len(users))
	for i, user := range users {
		resp := s.toUserResponse(&user)
		s.enrichWithRole(ctx, &resp, user.ID)
		userResponses[i] = resp
	}

	return &dto.UserListResponse{
		Users: userResponses,
		Total: total,
		Page:  params.Page,
		Limit: params.Limit,
	}, nil
}

// GetByID returns a user by ID
func (s *UserService) GetByID(ctx context.Context, id string) (*dto.UserResponse, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	response := s.toUserResponse(user)
	s.enrichWithRole(ctx, &response, user.ID)
	return &response, nil
}

// Create creates a new user. The insert into core.users AND the role
// assignment into core.user_roles run inside a single transaction so a
// failed assignment rolls the user row back.
func (s *UserService) Create(ctx context.Context, req *dto.CreateUserRequest, createdBy string, callerIsSuperAdmin bool) (*dto.UserResponse, error) {
	if err := s.validateRole(ctx, req.RoleID, callerIsSuperAdmin); err != nil {
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

	if req.RoleID != nil {
		if err := s.roleLookup.AssignRoleToUserTx(ctx, tx, user.ID, *req.RoleID, createdBy); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	response := s.toUserResponse(user)
	s.enrichWithRole(ctx, &response, user.ID)
	return &response, nil
}

// Update updates a user. A non-nil req.RoleID replaces the user's roles.
func (s *UserService) Update(ctx context.Context, id string, req *dto.UpdateUserRequest, updatedBy string, callerIsSuperAdmin bool) (*dto.UserResponse, error) {
	if err := s.validateRole(ctx, req.RoleID, callerIsSuperAdmin); err != nil {
		return nil, err
	}

	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	if err := s.guardSuperAdminTarget(ctx, id, callerIsSuperAdmin); err != nil {
		return nil, err
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

	if req.RoleID != nil {
		if err := s.roleLookup.ReplaceUserRoles(ctx, id, *req.RoleID, updatedBy); err != nil {
			return nil, err
		}
		s.invalidateUser(ctx, id)
	}

	response := s.toUserResponse(user)
	s.enrichWithRole(ctx, &response, user.ID)
	return &response, nil
}

// Delete soft deletes a user
func (s *UserService) Delete(ctx context.Context, id, deletedBy string, callerIsSuperAdmin bool) error {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}

	if err := s.guardSuperAdminTarget(ctx, id, callerIsSuperAdmin); err != nil {
		return err
	}

	if err := s.userRepo.SoftDelete(ctx, id, deletedBy); err != nil {
		return err
	}
	s.invalidateUser(ctx, id)
	return nil
}

// GetMe returns current user profile
func (s *UserService) GetMe(ctx context.Context, userID string) (*dto.UserResponse, error) {
	return s.GetByID(ctx, userID)
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

// enrichWithRole adds the user's role name to the response. A failed lookup
// is swallowed — the base user data is still returned.
func (s *UserService) enrichWithRole(ctx context.Context, resp *dto.UserResponse, userID string) {
	if s.roleLookup == nil {
		return
	}
	roles, err := s.roleLookup.GetUserRoles(ctx, userID)
	if err != nil || len(roles) == 0 {
		return
	}
	// Use the first assigned role as the user's role
	name := roles[0].Name
	resp.RoleName = &name
}
