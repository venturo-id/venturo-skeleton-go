package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/modules/core/role/dto"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
)

type RoleRepository struct {
	db *pgxpool.Pool
}

func NewRoleRepository(db *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{db: db}
}

// q runs statements on the transaction carried by ctx, if any.
func (r *RoleRepository) q(ctx context.Context) database.DBTX {
	return database.Querier(ctx, r.db)
}

// roleColumns matches scanRole; alias "r" is used where roles are joined.
const roleColumns = `
	r.id, r.code, r.name, r.description, r.permissions, r.is_system, r.is_active,
	r.created_at, r.created_by, r.updated_at, r.updated_by, r.deleted_at, r.deleted_by`

func scanRole(row pgx.Row) (*domain.Role, error) {
	var role domain.Role
	err := row.Scan(
		&role.ID, &role.Code, &role.Name, &role.Description, &role.Permissions,
		&role.IsSystem, &role.IsActive,
		&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
		&role.DeletedAt, &role.DeletedBy,
	)
	if err != nil {
		return nil, err
	}
	return &role, nil
}

func scanRoles(rows pgx.Rows) ([]domain.Role, error) {
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *role)
	}
	return roles, rows.Err()
}

// Create creates a new role. A duplicate code fails with a unique
// violation (see database.UniqueViolation).
func (r *RoleRepository) Create(ctx context.Context, role *domain.Role) error {
	query := `
		INSERT INTO core.roles (
			id, code, name, description, permissions, is_system, is_active,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := r.q(ctx).Exec(ctx, query,
		role.ID, role.Code, role.Name, role.Description, role.Permissions,
		role.IsSystem, role.IsActive,
		role.CreatedAt, role.CreatedBy, role.UpdatedAt, role.UpdatedBy,
	)
	if err != nil {
		if _, dup := database.UniqueViolation(err); !dup {
			logger.Error("Failed to create role", logger.Err(err))
		}
		return err
	}

	return nil
}

// findOne returns the live role matching column = value, or nil.
func (r *RoleRepository) findOne(ctx context.Context, column, value string) (*domain.Role, error) {
	query := `SELECT ` + roleColumns + ` FROM core.roles r WHERE r.` + column + ` = $1 AND r.deleted_at IS NULL`

	role, err := scanRole(r.q(ctx).QueryRow(ctx, query, value))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find role", logger.String("by", column), logger.Err(err))
		return nil, err
	}
	return role, nil
}

// FindByID finds a role by ID
func (r *RoleRepository) FindByID(ctx context.Context, id string) (*domain.Role, error) {
	return r.findOne(ctx, "id", id)
}

// FindByCode finds a role by code
func (r *RoleRepository) FindByCode(ctx context.Context, code string) (*domain.Role, error) {
	return r.findOne(ctx, "code", code)
}

// FindAll returns one page of live roles, newest first.
func (r *RoleRepository) FindAll(ctx context.Context, params *dto.RoleQueryParams) ([]domain.Role, int64, error) {
	conditions := []string{"r.deleted_at IS NULL"}
	var args []any

	if len(params.ExcludeCodes) > 0 {
		args = append(args, params.ExcludeCodes)
		conditions = append(conditions, fmt.Sprintf("r.code <> ALL($%d)", len(args)))
	}

	if params.Search != "" {
		args = append(args, "%"+params.Search+"%")
		n := len(args)
		conditions = append(conditions, fmt.Sprintf("(r.code ILIKE $%d OR r.name ILIKE $%d)", n, n))
	}

	whereClause := strings.Join(conditions, " AND ")

	var total int64
	countQuery := "SELECT COUNT(*) FROM core.roles r WHERE " + whereClause
	if err := r.q(ctx).QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		logger.Error("Failed to count roles", logger.Err(err))
		return nil, 0, err
	}

	args = append(args, params.Limit, (params.Page-1)*params.Limit)
	dataQuery := fmt.Sprintf(`
		SELECT %s FROM core.roles r
		WHERE %s
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $%d OFFSET $%d
	`, roleColumns, whereClause, len(args)-1, len(args))

	rows, err := r.q(ctx).Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find roles", logger.Err(err))
		return nil, 0, err
	}
	roles, err := scanRoles(rows)
	if err != nil {
		logger.Error("Failed to scan roles", logger.Err(err))
		return nil, 0, err
	}

	return roles, total, nil
}

// RoleChanges are the fields an update sets; nil keeps the stored value.
type RoleChanges struct {
	Name        *string
	Description *string
	Permissions domain.Permissions
	IsActive    *bool
}

// Update writes only the fields set in changes and returns the role as
// stored now. A field the update leaves alone keeps what a concurrent one
// wrote — renaming a role never puts back permissions narrowed meanwhile.
// Returns database.ErrNotFound when the role does not exist (any more).
func (r *RoleRepository) Update(ctx context.Context, id string, changes RoleChanges, updatedBy string) (*domain.Role, error) {
	query := `
		UPDATE core.roles AS r
		SET name = COALESCE($2, r.name), description = COALESCE($3, r.description),
		    permissions = COALESCE($4, r.permissions), is_active = COALESCE($5, r.is_active),
		    updated_at = $6, updated_by = $7
		WHERE r.id = $1 AND r.deleted_at IS NULL
		RETURNING ` + roleColumns

	// A nil map must reach the database as NULL, not as JSON null.
	var permissions any
	if changes.Permissions != nil {
		permissions = changes.Permissions
	}
	role, err := scanRole(r.q(ctx).QueryRow(ctx, query,
		id, changes.Name, changes.Description, permissions, changes.IsActive, time.Now(), updatedBy,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		logger.Error("Failed to update role", logger.Err(err))
		return nil, err
	}

	return role, nil
}

// SoftDelete soft deletes a role. Returns database.ErrNotFound when the
// role does not exist (any more).
func (r *RoleRepository) SoftDelete(ctx context.Context, id, deletedBy string) error {
	query := `
		UPDATE core.roles
		SET deleted_at = $2, deleted_by = $3, updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, time.Now(), deletedBy)
	if err != nil {
		logger.Error("Failed to soft delete role", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// AssignRoleToUser gives a user a role (no-op if already held). It joins
// the transaction carried by ctx, so it can be made atomic with the
// user's creation.
func (r *RoleRepository) AssignRoleToUser(ctx context.Context, userID, roleID, createdBy string) error {
	query := `
		INSERT INTO core.user_roles (user_id, role_id, created_at, created_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, role_id) DO NOTHING
	`

	if _, err := r.q(ctx).Exec(ctx, query, userID, roleID, time.Now(), createdBy); err != nil {
		logger.Error("Failed to assign role to user", logger.Err(err))
		return err
	}

	return nil
}

// ReplaceUserRoles replaces every role held by a user with a single role.
// The delete and insert run in one transaction (the caller's, if ctx
// carries one) so the user is never left without a role on failure.
func (r *RoleRepository) ReplaceUserRoles(ctx context.Context, userID, roleID, createdBy string) error {
	return database.WithTx(ctx, r.db, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM core.user_roles WHERE user_id = $1`, userID); err != nil {
			logger.Error("Failed to clear user roles", logger.Err(err))
			return err
		}
		return r.AssignRoleToUser(ctx, userID, roleID, createdBy)
	})
}

// RemoveUserRoles takes every role away from a user. It joins the
// transaction carried by ctx.
func (r *RoleRepository) RemoveUserRoles(ctx context.Context, userID string) error {
	if _, err := r.q(ctx).Exec(ctx, `DELETE FROM core.user_roles WHERE user_id = $1`, userID); err != nil {
		logger.Error("Failed to remove user roles", logger.Err(err))
		return err
	}
	return nil
}

// GetUserRoles gets all live roles assigned to a user, active or not, in
// assignment order.
func (r *RoleRepository) GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error) {
	query := `
		SELECT ` + roleColumns + `
		FROM core.roles r
		JOIN core.user_roles ur ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND r.deleted_at IS NULL
		ORDER BY ur.created_at, r.id
	`

	rows, err := r.q(ctx).Query(ctx, query, userID)
	if err != nil {
		logger.Error("Failed to get user roles", logger.Err(err))
		return nil, err
	}
	roles, err := scanRoles(rows)
	if err != nil {
		logger.Error("Failed to scan user roles", logger.Err(err))
		return nil, err
	}

	return roles, nil
}

// GetRoleNamesByUserIDs returns, per user, the name of the first role
// assigned to them. Users without a role are absent from the map. One
// query for a whole page of users.
func (r *RoleRepository) GetRoleNamesByUserIDs(ctx context.Context, userIDs []string) (map[string]string, error) {
	names := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return names, nil
	}

	query := `
		SELECT DISTINCT ON (ur.user_id) ur.user_id, r.name
		FROM core.user_roles ur
		JOIN core.roles r ON r.id = ur.role_id
		WHERE ur.user_id = ANY($1::uuid[]) AND r.deleted_at IS NULL
		ORDER BY ur.user_id, ur.created_at, r.id
	`

	rows, err := r.q(ctx).Query(ctx, query, userIDs)
	if err != nil {
		logger.Error("Failed to get role names", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var userID, name string
		if err := rows.Scan(&userID, &name); err != nil {
			logger.Error("Failed to scan role name", logger.Err(err))
			return nil, err
		}
		names[userID] = name
	}
	return names, rows.Err()
}

// GetUserPermissions returns the permission codes merged from a user's
// active roles (higher level wins), expanded to "resource:action".
func (r *RoleRepository) GetUserPermissions(ctx context.Context, userID string) ([]string, error) {
	roles, err := r.GetUserRoles(ctx, userID)
	if err != nil {
		return nil, err
	}

	merged := make(domain.Permissions)
	for _, role := range roles {
		if role.IsActive {
			merged.Merge(role.Permissions)
		}
	}

	return merged.ToStringList(), nil
}

// GetUserSubject loads the live authorization state for a user: whether
// the account may act at all, whether it holds an active super_admin
// role, its merged permissions, and from when its access tokens are
// valid. Unknown, deactivated, and deleted users come back inactive with
// no permissions. Implements authz.SubjectFetcher.
func (r *RoleRepository) GetUserSubject(ctx context.Context, userID string) (authz.Subject, error) {
	var (
		active           bool
		tokensValidAfter *time.Time
	)
	err := r.q(ctx).QueryRow(ctx,
		`SELECT is_active AND deleted_at IS NULL, tokens_valid_after FROM core.users WHERE id = $1`,
		userID,
	).Scan(&active, &tokensValidAfter)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authz.Subject{Permissions: []string{}}, nil
		}
		logger.Error("Failed to load user status", logger.Err(err))
		return authz.Subject{}, err
	}
	if !active {
		return authz.Subject{Permissions: []string{}}, nil
	}

	roles, err := r.GetUserRoles(ctx, userID)
	if err != nil {
		return authz.Subject{}, err
	}

	subject := authz.Subject{Active: true}
	if tokensValidAfter != nil {
		subject.TokensValidAfter = tokensValidAfter.UnixMilli()
	}
	if subject.EndedSessions, err = r.endedSessions(ctx, userID, tokensValidAfter); err != nil {
		return authz.Subject{}, err
	}
	merged := make(domain.Permissions)
	for _, role := range roles {
		if !role.IsActive {
			continue
		}
		if role.Code == jwt.RoleSuperAdmin {
			subject.SuperAdmin = true
		}
		merged.Merge(role.Permissions)
	}
	subject.Permissions = merged.ToStringList()
	if subject.Permissions == nil {
		subject.Permissions = []string{}
	}

	return subject, nil
}

// maxEndedSessions caps Subject.EndedSessions. Sessions ended all at once
// are already covered by the session epoch and not listed.
const maxEndedSessions = 100

// endedSessions lists the user's sessions that ended — no refresh token of
// theirs is live any more — within the last access token lifetime, so an
// access token of theirs could still be unexpired. A session that ends
// later invalidates the cached subject, so the cached list stays complete.
// Sessions revoked before tokensValidAfter (the session epoch) are left
// out: the epoch already refuses their tokens.
func (r *RoleRepository) endedSessions(ctx context.Context, userID string, tokensValidAfter *time.Time) ([]string, error) {
	query := `
		SELECT t.session_id::text
		FROM core.refresh_tokens t
		WHERE t.user_id = $1 AND t.revoked_at > $2
		  AND ($3::timestamptz IS NULL OR t.revoked_at > $3)
		  AND NOT EXISTS (
		      SELECT 1 FROM core.refresh_tokens live
		      WHERE live.session_id = t.session_id AND live.revoked_at IS NULL
		  )
		GROUP BY t.session_id
		ORDER BY MAX(t.revoked_at) DESC
		LIMIT $4
	`

	since := time.Now().Add(-jwt.GetExpirationTime())
	rows, err := r.q(ctx).Query(ctx, query, userID, since, tokensValidAfter, maxEndedSessions)
	if err != nil {
		logger.Error("Failed to load ended sessions", logger.Err(err))
		return nil, err
	}
	sessions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		logger.Error("Failed to read ended sessions", logger.Err(err))
		return nil, err
	}

	return sessions, nil
}

// GetUserRoleNames returns the codes of a user's active roles.
func (r *RoleRepository) GetUserRoleNames(ctx context.Context, userID string) ([]string, error) {
	roles, err := r.GetUserRoles(ctx, userID)
	if err != nil {
		return nil, err
	}

	codes := make([]string, 0, len(roles))
	for _, role := range roles {
		if role.IsActive {
			codes = append(codes, role.Code)
		}
	}

	return codes, nil
}
