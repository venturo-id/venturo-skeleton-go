package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/role/domain"
	"venturo-skeleton-go/internal/modules/core/role/dto"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type RoleRepository struct {
	db *pgxpool.Pool
}

func NewRoleRepository(db *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{db: db}
}

// Create creates a new role
func (r *RoleRepository) Create(ctx context.Context, role *domain.Role) error {
	query := `
		INSERT INTO core.roles (
			id, code, name, description, permissions, is_system, is_active, company_id,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err := r.db.Exec(ctx, query,
		role.ID, role.Code, role.Name, role.Description, role.Permissions,
		role.IsSystem, role.IsActive, role.CompanyID,
		role.CreatedAt, role.CreatedBy, role.UpdatedAt, role.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to create role", logger.Err(err))
		return err
	}

	return nil
}

// FindByID finds a role by ID
func (r *RoleRepository) FindByID(ctx context.Context, id string) (*domain.Role, error) {
	query := `
		SELECT id, code, name, description, permissions, is_system, is_active, company_id,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.roles
		WHERE id = $1 AND deleted_at IS NULL
	`

	var role domain.Role
	err := r.db.QueryRow(ctx, query, id).Scan(
		&role.ID, &role.Code, &role.Name, &role.Description, &role.Permissions,
		&role.IsSystem, &role.IsActive, &role.CompanyID,
		&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
		&role.DeletedAt, &role.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, derrors.WrapErrorf(err, derrors.ErrorCodeCustomNotFound, "Role %s not found", id)
		}
		logger.Error("Failed to find role by ID", logger.Err(err))
		return nil, derrors.WrapErrorf(err, derrors.ErrorCodeUnknown, "query role")
	}

	return &role, nil
}

// FindByCode finds a role by code within a scope (global or company)
func (r *RoleRepository) FindByCode(ctx context.Context, code string, companyID *string) (*domain.Role, error) {
	var query string
	var args []interface{}

	if companyID == nil {
		query = `
			SELECT id, code, name, description, permissions, is_system, is_active, company_id,
			       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
			FROM core.roles
			WHERE code = $1 AND company_id IS NULL AND deleted_at IS NULL
		`
		args = []interface{}{code}
	} else {
		query = `
			SELECT id, code, name, description, permissions, is_system, is_active, company_id,
			       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
			FROM core.roles
			WHERE code = $1 AND company_id = $2 AND deleted_at IS NULL
		`
		args = []interface{}{code, *companyID}
	}

	var role domain.Role
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&role.ID, &role.Code, &role.Name, &role.Description, &role.Permissions,
		&role.IsSystem, &role.IsActive, &role.CompanyID,
		&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
		&role.DeletedAt, &role.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find role by code", logger.Err(err))
		return nil, err
	}

	return &role, nil
}

// FindAll finds all roles with pagination and filtering
func (r *RoleRepository) FindAll(ctx context.Context, params *dto.RoleQueryParams) ([]domain.Role, int64, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "deleted_at IS NULL")

	if params.ExcludeSystem != nil && *params.ExcludeSystem {
		conditions = append(conditions, "is_system = false")
	}

	if len(params.ExcludeCodes) > 0 {
		placeholders := make([]string, len(params.ExcludeCodes))
		for i, code := range params.ExcludeCodes {
			placeholders[i] = fmt.Sprintf("$%d", argIndex)
			args = append(args, code)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("code NOT IN (%s)", strings.Join(placeholders, ", ")))
	}

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf(
			"(code ILIKE $%d OR name ILIKE $%d)",
			argIndex, argIndex,
		))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	// Filter by company
	if params.CompanyID != nil {
		if params.IncludeGlobal != nil && *params.IncludeGlobal {
			// Include both global and company-specific roles
			conditions = append(conditions, fmt.Sprintf(
				"(company_id IS NULL OR company_id = $%d)",
				argIndex,
			))
		} else {
			// Only company-specific roles
			conditions = append(conditions, fmt.Sprintf("company_id = $%d", argIndex))
		}
		args = append(args, *params.CompanyID)
		argIndex++
	} else if params.IncludeGlobal == nil || *params.IncludeGlobal {
		// Only global roles when no company specified
		conditions = append(conditions, "company_id IS NULL")
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM core.roles WHERE %s", whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count roles", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT id, code, name, description, permissions, is_system, is_active, company_id,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.roles
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find roles", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		var role domain.Role
		err := rows.Scan(
			&role.ID, &role.Code, &role.Name, &role.Description, &role.Permissions,
			&role.IsSystem, &role.IsActive, &role.CompanyID,
			&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
			&role.DeletedAt, &role.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan role", logger.Err(err))
			return nil, 0, err
		}
		roles = append(roles, role)
	}

	return roles, total, nil
}

// Update updates a role
func (r *RoleRepository) Update(ctx context.Context, role *domain.Role) error {
	query := `
		UPDATE core.roles
		SET name = $2, description = $3, permissions = $4, is_active = $5,
		    updated_at = $6, updated_by = $7
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query,
		role.ID, role.Name, role.Description, role.Permissions,
		role.IsActive, role.UpdatedAt, role.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to update role", logger.Err(err))
		return err
	}

	return nil
}

// UpdatePermissions updates only the permissions of a role
func (r *RoleRepository) UpdatePermissions(ctx context.Context, id string, permissions domain.Permissions, updatedBy string) error {
	query := `
		UPDATE core.roles
		SET permissions = $2, updated_at = $3, updated_by = $4
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, id, permissions, time.Now(), updatedBy)
	if err != nil {
		logger.Error("Failed to update role permissions", logger.Err(err))
		return err
	}

	return nil
}

// SoftDelete soft deletes a role
func (r *RoleRepository) SoftDelete(ctx context.Context, id, deletedBy string) error {
	query := `
		UPDATE core.roles
		SET deleted_at = $2, deleted_by = $3, updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NULL
	`

	now := time.Now()
	_, err := r.db.Exec(ctx, query, id, now, deletedBy)
	if err != nil {
		logger.Error("Failed to soft delete role", logger.Err(err))
		return err
	}

	return nil
}

// AssignRoleToUser assigns a role to a user
func (r *RoleRepository) AssignRoleToUser(ctx context.Context, userID, roleID string, companyID *string, createdBy string) error {
	query := `
		INSERT INTO core.user_roles (user_id, role_id, company_id, created_at, created_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, role_id) DO NOTHING
	`

	_, err := r.db.Exec(ctx, query, userID, roleID, companyID, time.Now(), createdBy)
	if err != nil {
		logger.Error("Failed to assign role to user", logger.Err(err))
		return err
	}

	return nil
}

// RemoveRoleFromUser removes a role from a user
func (r *RoleRepository) RemoveRoleFromUser(ctx context.Context, userID, roleID string, companyID *string) error {
	var query string
	var args []interface{}

	if companyID == nil {
		query = `DELETE FROM core.user_roles WHERE user_id = $1 AND role_id = $2 AND company_id IS NULL`
		args = []interface{}{userID, roleID}
	} else {
		query = `DELETE FROM core.user_roles WHERE user_id = $1 AND role_id = $2 AND company_id = $3`
		args = []interface{}{userID, roleID, *companyID}
	}

	_, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		logger.Error("Failed to remove role from user", logger.Err(err))
		return err
	}

	return nil
}

// GetUserRoles gets all roles for a user.
// Reads from both core.user_roles and core.company_users.role_id to cover both assignment methods.
func (r *RoleRepository) GetUserRoles(ctx context.Context, userID string, companyID *string) ([]domain.Role, error) {
	var query string
	var args []interface{}

	if companyID == nil {
		// Get global roles only (from user_roles)
		query = `
			SELECT DISTINCT r.id, r.code, r.name, r.description, r.permissions, r.is_system, r.is_active, r.company_id,
			       r.created_at, r.created_by, r.updated_at, r.updated_by, r.deleted_at, r.deleted_by
			FROM core.roles r
			JOIN core.user_roles ur ON r.id = ur.role_id
			WHERE ur.user_id = $1 AND ur.company_id IS NULL AND r.deleted_at IS NULL
		`
		args = []interface{}{userID}
	} else {
		// Get roles from user_roles + company_users.role_id
		query = `
			SELECT DISTINCT r.id, r.code, r.name, r.description, r.permissions, r.is_system, r.is_active, r.company_id,
			       r.created_at, r.created_by, r.updated_at, r.updated_by, r.deleted_at, r.deleted_by
			FROM core.roles r
			WHERE r.deleted_at IS NULL AND (
				r.id IN (
					SELECT ur.role_id FROM core.user_roles ur
					WHERE ur.user_id = $1 AND (ur.company_id IS NULL OR ur.company_id = $2)
				)
				OR r.id IN (
					SELECT cu.role_id FROM core.company_users cu
					WHERE cu.user_id = $1 AND cu.company_id = $2 AND cu.role_id IS NOT NULL
					AND cu.is_active = true AND cu.deleted_at IS NULL
				)
			)
		`
		args = []interface{}{userID, *companyID}
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		logger.Error("Failed to get user roles", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		var role domain.Role
		err := rows.Scan(
			&role.ID, &role.Code, &role.Name, &role.Description, &role.Permissions,
			&role.IsSystem, &role.IsActive, &role.CompanyID,
			&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
			&role.DeletedAt, &role.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan role", logger.Err(err))
			return nil, err
		}
		roles = append(roles, role)
	}

	return roles, nil
}

// GetUserPermissions gets all permissions for a user (merged from all roles, expanded to actions)
func (r *RoleRepository) GetUserPermissions(ctx context.Context, userID string, companyID *string) ([]string, error) {
	roles, err := r.GetUserRoles(ctx, userID, companyID)
	if err != nil {
		return nil, err
	}

	// Merge all permissions (higher level wins)
	merged := make(domain.Permissions)
	for _, role := range roles {
		if !role.IsActive {
			continue
		}
		merged.Merge(role.Permissions)
	}

	// Expand levels to action-based list for JWT claims
	return merged.ToStringList(), nil
}

// GetUserRoleNames gets all role codes for a user
func (r *RoleRepository) GetUserRoleNames(ctx context.Context, userID string, companyID *string) ([]string, error) {
	roles, err := r.GetUserRoles(ctx, userID, companyID)
	if err != nil {
		return nil, err
	}

	codes := make([]string, len(roles))
	for i, role := range roles {
		codes[i] = role.Code
	}

	return codes, nil
}

// CodeExists checks if a role code already exists within a scope
func (r *RoleRepository) CodeExists(ctx context.Context, code string, companyID *string, excludeID *string) (bool, error) {
	var query string
	var args []interface{}
	argIndex := 1

	if companyID == nil {
		query = `SELECT EXISTS(SELECT 1 FROM core.roles WHERE code = $1 AND company_id IS NULL AND deleted_at IS NULL`
		args = []interface{}{code}
		argIndex = 2
	} else {
		query = `SELECT EXISTS(SELECT 1 FROM core.roles WHERE code = $1 AND company_id = $2 AND deleted_at IS NULL`
		args = []interface{}{code, *companyID}
		argIndex = 3
	}

	if excludeID != nil {
		query += fmt.Sprintf(` AND id != $%d`, argIndex)
		args = append(args, *excludeID)
	}
	query += `)`

	var exists bool
	err := r.db.QueryRow(ctx, query, args...).Scan(&exists)
	if err != nil {
		logger.Error("Failed to check role code existence", logger.Err(err))
		return false, err
	}

	return exists, nil
}
