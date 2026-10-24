package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/database"
	roleDomain "timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/modules/core/user/domain"
	"timebox-be/internal/modules/core/user/dto"
	"timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/avatar"
	"timebox-be/pkg/crypto"
	jwtpkg "timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/validator"
)

var (
	ErrUserNotFound           = errors.New("user not found")
	ErrEmailAlreadyExists     = errors.New("email already exists")
	ErrUsernameAlreadyExists  = errors.New("username already exists")
	ErrRoleNotFound           = errors.New("role not found")
	ErrRoleNotAllowed         = errors.New("role is not allowed for caller")
	ErrCannotManageSuperAdmin = errors.New("only a super admin can manage a super admin user")
	ErrCannotManageHigherUser = errors.New("target user holds permissions the caller does not")
	ErrCannotChangeOwnRole    = errors.New("cannot change your own role")
	// ErrEmailUnverified: a role only goes to an account whose email is
	// proven, or anyone who registered with someone else's address could
	// be handed that person's access. An admin who knows the person can
	// vouch with is_email_verified=true in the same request.
	ErrEmailUnverified      = errors.New("the user's email is not verified")
	ErrCannotVerifyOwnEmail = errors.New("cannot change the verification of your own email")
	// ErrChangeOwnEmail: your own address changes through POST
	// /account/email, which asks for the password again and proves the new
	// address with a link. Here a session alone — perhaps a stolen access
	// token — would move the account, roles and all, to an unverified
	// address its new owner could then claim.
	ErrChangeOwnEmail = errors.New("change your own email through /account/email")
	// ErrCannotRemoveSelf: deactivating or deleting your own account here
	// needs nothing but a session, and would leave the system without its
	// last super admin; POST /account/delete asks for the password again.
	ErrCannotRemoveSelf = errors.New("cannot deactivate or delete your own account")
	// ErrStorageNotConfigured: avatars live in R2, and this instance has
	// no bucket (or no public URL for it).
	ErrStorageNotConfigured = errors.New("avatar storage is not configured")
	// ErrInvalidImage: the upload is not a JPEG, PNG or WebP image within
	// avatar.MaxSourceSide pixels a side.
	ErrInvalidImage = avatar.ErrInvalidImage
)

// RoleLookup reads roles and manages user role assignments (implemented by
// the role repository). Kept as a narrow interface so the user service does
// not pull in the full role package. Its writes join the transaction
// carried by ctx (see database.WithTx).
type RoleLookup interface {
	FindByID(ctx context.Context, id string) (*roleDomain.Role, error)
	GetUserRoles(ctx context.Context, userID string) ([]roleDomain.Role, error)
	GetRoleNamesByUserIDs(ctx context.Context, userIDs []string) (map[string]string, error)
	AssignRoleToUser(ctx context.Context, userID, roleID, createdBy string) error
	ReplaceUserRoles(ctx context.Context, userID, roleID, createdBy string) error
}

// PermissionCacheInvalidator drops a user's cached authz state after their
// role assignment or active status changes. Implemented by *authz.Service.
type PermissionCacheInvalidator interface {
	InvalidateUser(ctx context.Context, userID string) error
}

// Credentials is the auth service's part in managing users: it ends a
// user's sessions, access tokens included, and changes the caller's own
// password — under the sign-in lock, so its errors are the auth
// service's (ErrIncorrectPassword, ErrNoPassword, ErrPasswordLocked).
type Credentials interface {
	RevokeAllSessions(ctx context.Context, userID string) error
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error
}

type UserService struct {
	db          database.TxBeginner
	userRepo    *repository.UserRepository
	roleLookup  RoleLookup
	invalidator PermissionCacheInvalidator
	credentials Credentials
	audit       audit.Recorder
	avatars     *avatar.Store // nil: no storage, avatars cannot change
}

// NewUserService wires the user service. Every dependency is required but
// avatars, which is nil when R2 is not configured.
func NewUserService(
	db database.TxBeginner,
	userRepo *repository.UserRepository,
	roleLookup RoleLookup,
	invalidator PermissionCacheInvalidator,
	credentials Credentials,
	recorder audit.Recorder,
	avatars *avatar.Store,
) *UserService {
	return &UserService{
		db:          db,
		userRepo:    userRepo,
		roleLookup:  roleLookup,
		invalidator: invalidator,
		credentials: credentials,
		audit:       recorder,
		avatars:     avatars,
	}
}

// invalidateUser drops the cached authz state. A failure is logged, not
// returned — the write that triggered it has already happened, and the
// TTL eventually heals the cache.
func (s *UserService) invalidateUser(ctx context.Context, userID string) {
	if err := s.invalidator.InvalidateUser(ctx, userID); err != nil {
		logger.Warn("authz cache invalidation failed",
			logger.String("user_id", userID),
			logger.Err(err))
	}
}

// revokeSessions ends a user's sessions. A failure is logged, not
// returned: the write that triggered it has already happened.
func (s *UserService) revokeSessions(ctx context.Context, userID string) {
	if err := s.credentials.RevokeAllSessions(ctx, userID); err != nil {
		logger.Error("Failed to revoke user sessions",
			logger.String("user_id", userID),
			logger.Err(err))
	}
}

// validateRole ensures roleID points to an existing role the caller may
// hand out: never super_admin unless the caller is one, and never a role
// granting permissions the caller does not hold itself — otherwise
// users-management access alone would be a path to any role. Nil roleID
// is allowed — it means "no role change".
func (s *UserService) validateRole(ctx context.Context, roleID *string, caller authz.Subject) error {
	if roleID == nil {
		return nil
	}
	role, err := s.roleLookup.FindByID(ctx, *roleID)
	if err != nil {
		return err
	}
	if role == nil {
		return ErrRoleNotFound
	}
	if role.Code == jwtpkg.RoleSuperAdmin && !caller.SuperAdmin {
		return ErrRoleNotAllowed
	}
	if !caller.Covers(role.Permissions.ToStringList()) {
		return ErrRoleNotAllowed
	}
	return nil
}

// guardTarget stops a caller from modifying or deleting a user who holds
// more than the caller does: a super_admin (unless the caller is one), or
// any permission the caller lacks.
func (s *UserService) guardTarget(ctx context.Context, userID string, caller authz.Subject) error {
	if caller.SuperAdmin {
		return nil
	}
	roles, err := s.roleLookup.GetUserRoles(ctx, userID)
	if err != nil {
		return err
	}
	held := make(roleDomain.Permissions)
	for _, r := range roles {
		if r.Code == jwtpkg.RoleSuperAdmin {
			return ErrCannotManageSuperAdmin
		}
		if r.IsActive {
			held.Merge(r.Permissions)
		}
	}
	if !caller.Covers(held.ToStringList()) {
		return ErrCannotManageHigherUser
	}
	return nil
}

// List returns one page of users with their role names, and the total.
func (s *UserService) List(ctx context.Context, params *dto.UserQueryParams) ([]dto.UserResponse, int64, error) {
	users, total, err := s.userRepo.FindAll(ctx, params)
	if err != nil {
		return nil, 0, err
	}

	ids := make([]string, len(users))
	for i := range users {
		ids[i] = users[i].ID
	}
	roleNames, err := s.roleLookup.GetRoleNamesByUserIDs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]dto.UserResponse, len(users))
	for i := range users {
		responses[i] = toUserResponse(&users[i])
		if name, ok := roleNames[users[i].ID]; ok {
			responses[i].RoleName = &name
		}
	}

	return responses, total, nil
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

	return s.withRole(ctx, user)
}

// Create creates a new user. The insert into core.users AND the role
// assignment into core.user_roles run inside a single transaction so a
// failed assignment rolls the user row back.
//
// The email is stored as verified: the admin creating the account vouches
// for it, so a later Google sign-in with that address links to this user.
func (s *UserService) Create(ctx context.Context, req *dto.CreateUserRequest, createdBy string, caller authz.Subject) (*dto.UserResponse, error) {
	if err := s.validateRole(ctx, req.RoleID, caller); err != nil {
		return nil, err
	}

	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		logger.Error("Failed to hash password", logger.Err(err))
		return nil, err
	}

	now := time.Now()
	user := &domain.User{
		ID:           uuid.New().String(),
		Email:        validator.NormalizeEmail(req.Email),
		Username:     validator.NormalizeUsername(req.Username),
		PasswordHash: passwordHash,
		FullName:     req.FullName,
		Phone:        req.Phone,
		Timezone:     deref(req.Timezone),
		IsActive:     true,
		CreatedAt:    now,
		CreatedBy:    &createdBy,
		UpdatedAt:    now,
		UpdatedBy:    &createdBy,
	}
	user.MarkEmailVerified(true, now)

	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		if err := s.userRepo.Create(ctx, user); err != nil {
			return err
		}
		if req.RoleID != nil {
			return s.roleLookup.AssignRoleToUser(ctx, user.ID, *req.RoleID, createdBy)
		}
		return nil
	})
	if err != nil {
		return nil, translateDuplicate(err)
	}
	s.record(ctx, audit.ActionUserCreated, user.ID, map[string]any{"role_id": req.RoleID})

	return s.withRole(ctx, user)
}

// Update updates a user. A non-nil req.RoleID replaces the user's roles.
// The user is read and locked, checked, changed and its role replaced in
// one transaction: a change made meanwhile by someone else — a
// deactivation, an email change, a password reset — is never written over
// with what this request read before it.
func (s *UserService) Update(ctx context.Context, id string, req *dto.UpdateUserRequest, updatedBy string, caller authz.Subject) (*dto.UserResponse, error) {
	self := id == updatedBy
	switch {
	case self && req.RoleID != nil:
		// Nobody changes their own role, so no caller can raise itself —
		// and the last super admin cannot demote itself by accident.
		return nil, ErrCannotChangeOwnRole
	case self && req.IsEmailVerified != nil:
		return nil, ErrCannotVerifyOwnEmail
	case self && req.IsActive != nil && !*req.IsActive:
		return nil, ErrCannotRemoveSelf
	}

	if err := s.validateRole(ctx, req.RoleID, caller); err != nil {
		return nil, err
	}

	var (
		user      *domain.User
		wasActive bool
	)
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		var err error
		if user, err = s.userRepo.FindByIDForUpdate(ctx, id); err != nil {
			return err
		}
		if user == nil {
			return ErrUserNotFound
		}
		if err := s.guardTarget(ctx, id, caller); err != nil {
			return err
		}

		now := time.Now()
		if req.Email != nil {
			if email := validator.NormalizeEmail(*req.Email); email != user.Email {
				if self {
					return ErrChangeOwnEmail
				}
				// An admin changing someone else's address vouches for it, as
				// on create.
				user.Email = email
				user.MarkEmailVerified(true, now)
			}
		}
		if req.Username != nil {
			user.Username = validator.NormalizeUsername(*req.Username)
		}
		if req.FullName != nil {
			user.FullName = req.FullName
		}
		if req.Phone != nil {
			user.Phone = req.Phone
		}
		if req.Timezone != nil {
			user.Timezone = *req.Timezone
		}
		if req.IsEmailVerified != nil {
			user.MarkEmailVerified(*req.IsEmailVerified, now)
		}
		if req.RoleID != nil && !user.IsEmailVerified {
			return ErrEmailUnverified
		}
		wasActive = user.IsActive
		if req.IsActive != nil {
			user.IsActive = *req.IsActive
		}

		user.UpdatedAt = now
		user.UpdatedBy = &updatedBy

		if err := s.userRepo.Update(ctx, user); err != nil {
			return err
		}
		if req.RoleID != nil {
			return s.roleLookup.ReplaceUserRoles(ctx, id, *req.RoleID, updatedBy)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, translateDuplicate(err)
	}

	// A deactivated user loses every session; either way a role or status
	// change leaves the cached authz state stale.
	if wasActive && !user.IsActive {
		s.revokeSessions(ctx, id)
	}
	if req.RoleID != nil || wasActive != user.IsActive {
		s.invalidateUser(ctx, id)
	}
	s.record(ctx, audit.ActionUserUpdated, id, map[string]any{"fields": changedFields(req), "role_id": req.RoleID})

	return s.withRole(ctx, user)
}

// Delete soft deletes a user and ends their sessions.
func (s *UserService) Delete(ctx context.Context, id, deletedBy string, caller authz.Subject) error {
	if id == deletedBy {
		return ErrCannotRemoveSelf
	}
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}

	if err := s.guardTarget(ctx, id, caller); err != nil {
		return err
	}

	if err := s.userRepo.SoftDelete(ctx, id, deletedBy); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return ErrUserNotFound
		}
		return err
	}
	s.revokeSessions(ctx, id)
	s.invalidateUser(ctx, id)
	s.record(ctx, audit.ActionUserDeleted, id, nil)
	return nil
}

// UpdateMe updates the caller's own profile fields. Status, email and
// username are left alone, so a self-edit cannot undo an admin's change.
func (s *UserService) UpdateMe(ctx context.Context, userID string, req *dto.UpdateMeRequest) (*dto.UserResponse, error) {
	if err := s.userRepo.UpdateProfile(ctx, userID, req.FullName, req.Phone, req.Timezone); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	fields := []string{}
	for name, set := range map[string]bool{
		"full_name": req.FullName != nil, "phone": req.Phone != nil, "timezone": req.Timezone != nil,
	} {
		if set {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	s.record(ctx, audit.ActionUserUpdated, userID, map[string]any{"fields": fields})

	return s.GetByID(ctx, userID)
}

// UploadAvatar makes the image data the caller's avatar: it is normalized
// (see avatar.Normalize), stored under a new URL, and the picture it
// replaces is deleted. Returns ErrStorageNotConfigured without storage and
// ErrInvalidImage for data that is not a supported image.
func (s *UserService) UploadAvatar(ctx context.Context, userID string, data []byte) (*dto.UserResponse, error) {
	if s.avatars == nil {
		return nil, ErrStorageNotConfigured
	}
	jpeg, err := avatar.Normalize(data)
	if err != nil {
		return nil, err
	}

	avatarURL, err := s.avatars.Put(ctx, userID, jpeg)
	if err != nil {
		return nil, err
	}
	previous, err := s.userRepo.SetAvatar(ctx, userID, &avatarURL)
	if err != nil {
		// Nothing refers to the new picture.
		s.deleteAvatar(ctx, userID, avatarURL)
		if errors.Is(err, database.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if previous != nil {
		s.deleteAvatar(ctx, userID, *previous)
	}
	s.record(ctx, audit.ActionAvatarChanged, userID, nil)

	return s.GetByID(ctx, userID)
}

// RemoveAvatar clears the caller's avatar and deletes the picture. Returns
// ErrStorageNotConfigured without storage.
func (s *UserService) RemoveAvatar(ctx context.Context, userID string) (*dto.UserResponse, error) {
	if s.avatars == nil {
		return nil, ErrStorageNotConfigured
	}

	previous, err := s.userRepo.SetAvatar(ctx, userID, nil)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if previous != nil {
		s.deleteAvatar(ctx, userID, *previous)
		s.record(ctx, audit.ActionAvatarRemoved, userID, nil)
	}

	return s.GetByID(ctx, userID)
}

// deleteAvatar deletes the picture at avatarURL, if it is in the bucket. A
// failure is logged, not returned: the row no longer refers to it, so it
// is only an orphaned object.
func (s *UserService) deleteAvatar(ctx context.Context, userID, avatarURL string) {
	if err := s.avatars.Delete(ctx, avatarURL); err != nil {
		logger.Warn("Failed to delete avatar",
			logger.String("user_id", userID),
			logger.Err(err))
	}
}

// ChangePassword changes the caller's password and ends every session,
// this one included: whoever knew the old password is signed out. The
// auth service does it, because wrong guesses count toward the sign-in
// lock.
func (s *UserService) ChangePassword(ctx context.Context, userID string, req *dto.ChangePasswordRequest) error {
	return s.credentials.ChangePassword(ctx, userID, req.CurrentPassword, req.NewPassword)
}

// withRole converts a user to a response carrying its role name.
func (s *UserService) withRole(ctx context.Context, user *domain.User) (*dto.UserResponse, error) {
	names, err := s.roleLookup.GetRoleNamesByUserIDs(ctx, []string{user.ID})
	if err != nil {
		return nil, err
	}

	response := toUserResponse(user)
	if name, ok := names[user.ID]; ok {
		response.RoleName = &name
	}
	return &response, nil
}

// translateDuplicate maps the repository's duplicate errors to this
// service's.
func translateDuplicate(err error) error {
	switch {
	case errors.Is(err, repository.ErrEmailTaken):
		return ErrEmailAlreadyExists
	case errors.Is(err, repository.ErrUsernameTaken):
		return ErrUsernameAlreadyExists
	}
	return err
}

// toUserResponse converts domain.User to dto.UserResponse
func toUserResponse(user *domain.User) dto.UserResponse {
	return dto.UserResponse{
		ID:              user.ID,
		Email:           user.Email,
		Username:        user.Username,
		FullName:        user.FullName,
		Phone:           user.Phone,
		AvatarURL:       user.AvatarURL,
		Timezone:        user.Timezone,
		IsActive:        user.IsActive,
		IsEmailVerified: user.IsEmailVerified,
		LastLoginAt:     user.LastLoginAt,
		CreatedAt:       user.CreatedAt,
		UpdatedAt:       user.UpdatedAt,
	}
}

// deref returns *p, or "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// record writes an audit entry about the user userID; the actor is the
// caller in ctx.
func (s *UserService) record(ctx context.Context, action, userID string, details map[string]any) {
	s.audit.Record(ctx, audit.Entry{Action: action, TargetType: audit.TargetUser, TargetID: userID, Details: details})
}

// changedFields names the fields an update request sets — the names only:
// the audit log keeps no personal data.
func changedFields(req *dto.UpdateUserRequest) []string {
	fields := []string{}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"email", req.Email != nil},
		{"username", req.Username != nil},
		{"full_name", req.FullName != nil},
		{"phone", req.Phone != nil},
		{"timezone", req.Timezone != nil},
		{"is_active", req.IsActive != nil},
		{"is_email_verified", req.IsEmailVerified != nil},
		{"role_id", req.RoleID != nil},
	} {
		if f.set {
			fields = append(fields, f.name)
		}
	}
	return fields
}
