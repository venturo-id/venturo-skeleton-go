package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/modules/core/role/dto"
	"timebox-be/internal/modules/core/role/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
)

var (
	ErrRoleNotFound       = errors.New("role not found")
	ErrCodeAlreadyExists  = errors.New("role code already exists")
	ErrCannotModifySystem = errors.New("cannot modify system role")
	ErrCannotDeleteSystem = errors.New("cannot delete system role")
	ErrInvalidPermissions = errors.New("invalid permissions")
	// ErrExceedsCaller: the role grants, or would grant, a permission the
	// caller does not hold. Without this ceiling anyone with roles:create
	// could mint a role with every permission and use it.
	ErrExceedsCaller = errors.New("role grants permissions the caller does not hold")
	// ErrSuperAdminRoleLocked: deactivating super_admin would lock every
	// super admin out at once, with no way back but SQL.
	ErrSuperAdminRoleLocked = errors.New("the super_admin role cannot be deactivated or given permissions")
)

// PermissionCacheInvalidator keeps Redis in sync with role changes.
// Implemented by *authz.Service.
type PermissionCacheInvalidator interface {
	InvalidateAll(ctx context.Context) error
}

type RoleService struct {
	roleRepo    *repository.RoleRepository
	invalidator PermissionCacheInvalidator
	audit       audit.Recorder
}

func NewRoleService(roleRepo *repository.RoleRepository, invalidator PermissionCacheInvalidator, recorder audit.Recorder) *RoleService {
	return &RoleService{
		roleRepo:    roleRepo,
		invalidator: invalidator,
		audit:       recorder,
	}
}

// invalidateAll drops every cached permission set: a role's shape
// changed and enumerating its holders is not worth it. A failure is
// logged, not returned — the write already happened and the TTL heals
// the cache.
func (s *RoleService) invalidateAll(ctx context.Context) {
	if err := s.invalidator.InvalidateAll(ctx); err != nil {
		logger.Warn("authz cache global invalidation failed", logger.Err(err))
	}
}

// PermissionCatalog lists the resources and levels a role can grant.
func (s *RoleService) PermissionCatalog() dto.PermissionCatalogResponse {
	levels := make([]dto.PermissionLevelInfo, 0, len(domain.ValidLevels))
	for _, level := range domain.ValidLevels {
		levels = append(levels, dto.PermissionLevelInfo{
			Level:   string(level),
			Actions: domain.LevelActions[level],
		})
	}
	return dto.PermissionCatalogResponse{
		Resources: authz.Resources(),
		Levels:    levels,
	}
}

// List returns one page of roles and the total count.
func (s *RoleService) List(ctx context.Context, params *dto.RoleQueryParams) ([]dto.RoleResponse, int64, error) {
	roles, total, err := s.roleRepo.FindAll(ctx, params)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]dto.RoleResponse, len(roles))
	for i := range roles {
		responses[i] = toRoleResponse(&roles[i])
	}

	return responses, total, nil
}

// GetByID returns a role by ID
func (s *RoleService) GetByID(ctx context.Context, id string) (*dto.RoleResponse, error) {
	role, err := s.roleRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, ErrRoleNotFound
	}

	response := toRoleResponse(role)
	return &response, nil
}

// Create creates a new role. It may only grant permissions the caller holds.
func (s *RoleService) Create(ctx context.Context, req *dto.CreateRoleRequest, createdBy string, caller authz.Subject) (*dto.RoleResponse, error) {
	if err := validatePermissions(req.Permissions); err != nil {
		return nil, err
	}
	if !caller.Covers(domain.Permissions(req.Permissions).ToStringList()) {
		return nil, ErrExceedsCaller
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	now := time.Now()
	role := &domain.Role{
		ID:          uuid.New().String(),
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		Permissions: domain.Permissions(req.Permissions),
		IsSystem:    false,
		IsActive:    isActive,
		CreatedAt:   now,
		CreatedBy:   &createdBy,
		UpdatedAt:   now,
		UpdatedBy:   &createdBy,
	}

	if err := s.roleRepo.Create(ctx, role); err != nil {
		if _, dup := database.UniqueViolation(err); dup {
			return nil, ErrCodeAlreadyExists
		}
		return nil, err
	}

	logger.Info("Role created",
		logger.String("role_id", role.ID),
		logger.String("code", role.Code))
	s.record(ctx, audit.ActionRoleCreated, role)

	response := toRoleResponse(role)
	return &response, nil
}

// Update updates a role. Super admins may edit system roles. The caller
// must hold every permission the role grants, before and after the edit.
func (s *RoleService) Update(ctx context.Context, id string, req *dto.UpdateRoleRequest, updatedBy string, caller authz.Subject) (*dto.RoleResponse, error) {
	role, err := s.findManageable(ctx, id, caller)
	if err != nil {
		return nil, err
	}
	deactivates := req.IsActive != nil && !*req.IsActive
	if err := checkSuperAdminRole(role, req.Permissions != nil, deactivates); err != nil {
		return nil, err
	}

	changes := repository.RoleChanges{Name: req.Name, Description: req.Description, IsActive: req.IsActive}
	if req.Permissions != nil {
		if err := validatePermissions(req.Permissions); err != nil {
			return nil, err
		}
		if !caller.Covers(domain.Permissions(req.Permissions).ToStringList()) {
			return nil, ErrExceedsCaller
		}
		changes.Permissions = domain.Permissions(req.Permissions)
	}

	return s.save(ctx, role.ID, changes, updatedBy)
}

// UpdatePermissions replaces the permissions of a role. Same rules as
// Update.
func (s *RoleService) UpdatePermissions(ctx context.Context, id string, req *dto.UpdatePermissionsRequest, updatedBy string, caller authz.Subject) (*dto.RoleResponse, error) {
	role, err := s.findManageable(ctx, id, caller)
	if err != nil {
		return nil, err
	}
	if err := checkSuperAdminRole(role, true, false); err != nil {
		return nil, err
	}

	if err := validatePermissions(req.Permissions); err != nil {
		return nil, err
	}
	if !caller.Covers(domain.Permissions(req.Permissions).ToStringList()) {
		return nil, ErrExceedsCaller
	}

	return s.save(ctx, role.ID, repository.RoleChanges{Permissions: domain.Permissions(req.Permissions)}, updatedBy)
}

// save writes the changed fields of a role — only those, so a concurrent
// edit of the others stands — and drops every cached permission set: any
// holder of the role may now be allowed more or less.
func (s *RoleService) save(ctx context.Context, id string, changes repository.RoleChanges, updatedBy string) (*dto.RoleResponse, error) {
	role, err := s.roleRepo.Update(ctx, id, changes, updatedBy)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	s.invalidateAll(ctx)
	s.record(ctx, audit.ActionRoleUpdated, role)

	response := toRoleResponse(role)
	return &response, nil
}

// Delete soft deletes a role. System roles cannot be deleted by anyone,
// and the caller must hold every permission the role grants.
func (s *RoleService) Delete(ctx context.Context, id, deletedBy string, caller authz.Subject) error {
	role, err := s.roleRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if role == nil {
		return ErrRoleNotFound
	}

	if role.IsSystem {
		return ErrCannotDeleteSystem
	}
	if !caller.Covers(role.Permissions.ToStringList()) {
		return ErrExceedsCaller
	}

	if err := s.roleRepo.SoftDelete(ctx, id, deletedBy); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return ErrRoleNotFound
		}
		return err
	}

	// Any user still holding this role now has a stale cached set.
	s.invalidateAll(ctx)
	s.record(ctx, audit.ActionRoleDeleted, role)
	return nil
}

// record writes an audit entry about role, with the permissions it grants
// after the action.
func (s *RoleService) record(ctx context.Context, action string, role *domain.Role) {
	s.audit.Record(ctx, audit.Entry{
		Action: action, TargetType: audit.TargetRole, TargetID: role.ID,
		Details: map[string]any{"code": role.Code, "permissions": map[string]string(role.Permissions), "is_active": role.IsActive},
	})
}

// findManageable loads a role the caller may edit.
func (s *RoleService) findManageable(ctx context.Context, id string, caller authz.Subject) (*domain.Role, error) {
	role, err := s.roleRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, ErrRoleNotFound
	}
	if err := checkManageable(role, caller); err != nil {
		return nil, err
	}
	return role, nil
}

// checkManageable reports whether caller may edit role: system roles are
// super-admin only, and nobody may edit a role that grants more than they
// hold (that would let them rename, disable, or downgrade it).
func checkManageable(role *domain.Role, caller authz.Subject) error {
	if role.IsSystem && !caller.SuperAdmin {
		return ErrCannotModifySystem
	}
	if !caller.Covers(role.Permissions.ToStringList()) {
		return ErrExceedsCaller
	}
	return nil
}

// checkSuperAdminRole refuses the two edits of the super_admin role that
// make no sense: permissions (a super admin bypasses them anyway) and
// deactivation (it would remove every super admin at once).
func checkSuperAdminRole(role *domain.Role, setsPermissions, deactivates bool) error {
	if role.Code == jwt.RoleSuperAdmin && (setsPermissions || deactivates) {
		return ErrSuperAdminRoleLocked
	}
	return nil
}

// validatePermissions checks that every key is a registered resource and
// every value a valid level.
func validatePermissions(permissions map[string]string) error {
	if permissions == nil {
		return fmt.Errorf("%w: permissions are required", ErrInvalidPermissions)
	}

	for resource, level := range permissions {
		if !authz.IsResource(resource) {
			return fmt.Errorf("%w: unknown resource %q", ErrInvalidPermissions, resource)
		}
		if !domain.IsValidLevel(level) {
			return fmt.Errorf("%w: level %q for %q must be viewer, editor or admin", ErrInvalidPermissions, level, resource)
		}
	}

	return nil
}

// toRoleResponse converts domain.Role to dto.RoleResponse
func toRoleResponse(role *domain.Role) dto.RoleResponse {
	permissions := map[string]string(role.Permissions)
	if permissions == nil {
		permissions = map[string]string{}
	}
	return dto.RoleResponse{
		ID:          role.ID,
		Code:        role.Code,
		Name:        role.Name,
		Description: role.Description,
		Permissions: permissions,
		IsSystem:    role.IsSystem,
		IsActive:    role.IsActive,
		CreatedBy:   role.CreatedBy,
		CreatedAt:   role.CreatedAt,
		UpdatedAt:   role.UpdatedAt,
	}
}
