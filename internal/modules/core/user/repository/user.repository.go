package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/user/domain"
	"venturo-skeleton-go/internal/modules/core/user/dto"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

// Create creates a new user
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	return r.createWith(ctx, r.db, user)
}

// CreateTx creates a new user inside an existing transaction. Use this when
// the caller needs to atomically create the user together with related rows
// (e.g. core.company_users) so a partial state cannot leak on failure.
func (r *UserRepository) CreateTx(ctx context.Context, tx pgx.Tx, user *domain.User) error {
	return r.createWith(ctx, tx, user)
}

// dbExec is the minimal subset of *pgxpool.Pool / pgx.Tx that we need for
// shared write helpers. Both types satisfy this implicitly.
type dbExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (r *UserRepository) createWith(ctx context.Context, q dbExec, user *domain.User) error {
	query := `
		INSERT INTO core.users (
			id, email, username, password_hash, full_name, phone, avatar_url,
			is_active, is_email_verified, created_at, created_by, updated_at, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
	`

	_, err := q.Exec(ctx, query,
		user.ID, user.Email, user.Username, user.PasswordHash,
		user.FullName, user.Phone, user.AvatarURL,
		user.IsActive, user.IsEmailVerified,
		user.CreatedAt, user.CreatedBy, user.UpdatedAt, user.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to create user", logger.Err(err))
		return err
	}

	return nil
}

// BeginTx exposes the underlying pool's transaction starter to higher layers
// (e.g. UserService) that need to coordinate cross-repository writes.
func (r *UserRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return r.db.Begin(ctx)
}

// CreateExternal inserts a user whose authentication is delegated to an
// external identity provider (Firebase / Google for now). password_hash
// is forced to NULL — the user can later add a local password through a
// separate flow, but until then SignIn with email/password must fail.
//
// Callers are responsible for ensuring email/username uniqueness *before*
// invoking this; the partial unique indexes will reject duplicates
// otherwise.
func (r *UserRepository) CreateExternal(ctx context.Context, user *domain.User) error {
	query := `
		INSERT INTO core.users (
			id, email, username, password_hash, full_name, phone, avatar_url,
			is_active, is_email_verified, email_verified_at,
			created_at, created_by, updated_at, updated_by
		) VALUES (
			$1, $2, $3, NULL, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
	`

	_, err := r.db.Exec(ctx, query,
		user.ID, user.Email, user.Username,
		user.FullName, user.Phone, user.AvatarURL,
		user.IsActive, user.IsEmailVerified, user.EmailVerifiedAt,
		user.CreatedAt, user.CreatedBy, user.UpdatedAt, user.UpdatedBy,
	)
	if err != nil {
		logger.Error("Failed to create external user", logger.Err(err))
		return err
	}

	return nil
}

// FindByID finds a user by ID
func (r *UserRepository) FindByID(ctx context.Context, id string) (*domain.User, error) {
	query := `
		SELECT id, email, username, COALESCE(password_hash, '') AS password_hash, full_name, phone, avatar_url,
		       is_active, is_email_verified, email_verified_at,
		       failed_login_count, locked_until, last_login_at,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.users
		WHERE id = $1 AND deleted_at IS NULL
	`

	var user domain.User
	err := r.db.QueryRow(ctx, query, id).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&user.FullName, &user.Phone, &user.AvatarURL,
		&user.IsActive, &user.IsEmailVerified, &user.EmailVerifiedAt,
		&user.FailedLoginCount, &user.LockedUntil, &user.LastLoginAt,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
		&user.DeletedAt, &user.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, derrors.WrapErrorf(err, derrors.ErrorCodeCustomNotFound, "User %s not found", id)
		}
		logger.Error("Failed to find user by ID", logger.Err(err))
		return nil, derrors.WrapErrorf(err, derrors.ErrorCodeUnknown, "query user")
	}

	return &user, nil
}

// IsUserVisibleToScope reports whether userID is visible to a non-super-admin
// caller whose tenant scope is anchored at scopeCompanyID (and who is
// identified as callerUserID for the self-created fallback).
//
// Visibility rules mirror FindAll: membership in scopeCompanyID or any of its
// descendants, OR the user was created by the caller.
func (r *UserRepository) IsUserVisibleToScope(ctx context.Context, userID, scopeCompanyID, callerUserID string) (bool, error) {
	query := `
		WITH RECURSIVE visible_companies AS (
			SELECT id FROM core.companies
			 WHERE id = $2 AND deleted_at IS NULL
			UNION ALL
			SELECT c.id FROM core.companies c
			  JOIN visible_companies v ON c.parent_id = v.id
			 WHERE c.deleted_at IS NULL
		)
		SELECT EXISTS (
			SELECT 1 FROM core.users u
			 WHERE u.id = $1
			   AND u.deleted_at IS NULL
			   AND (
				EXISTS (
					SELECT 1 FROM core.company_users cu
					 WHERE cu.user_id = u.id
					   AND cu.deleted_at IS NULL
					   AND cu.company_id IN (SELECT id FROM visible_companies)
				)
				OR u.created_by = $3
			   )
		)
	`

	var visible bool
	if err := r.db.QueryRow(ctx, query, userID, scopeCompanyID, callerUserID).Scan(&visible); err != nil {
		logger.Error("Failed to check user visibility", logger.Err(err))
		return false, err
	}
	return visible, nil
}

// FindByEmail finds a user by email
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	query := `
		SELECT id, email, username, COALESCE(password_hash, '') AS password_hash, full_name, phone, avatar_url,
		       is_active, is_email_verified, email_verified_at,
		       failed_login_count, locked_until, last_login_at,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.users
		WHERE email = $1 AND deleted_at IS NULL
	`

	var user domain.User
	err := r.db.QueryRow(ctx, query, email).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&user.FullName, &user.Phone, &user.AvatarURL,
		&user.IsActive, &user.IsEmailVerified, &user.EmailVerifiedAt,
		&user.FailedLoginCount, &user.LockedUntil, &user.LastLoginAt,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
		&user.DeletedAt, &user.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find user by email", logger.Err(err))
		return nil, err
	}

	return &user, nil
}

// FindByUsername finds a user by username
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	query := `
		SELECT id, email, username, COALESCE(password_hash, '') AS password_hash, full_name, phone, avatar_url,
		       is_active, is_email_verified, email_verified_at,
		       failed_login_count, locked_until, last_login_at,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.users
		WHERE username = $1 AND deleted_at IS NULL
	`

	var user domain.User
	err := r.db.QueryRow(ctx, query, username).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&user.FullName, &user.Phone, &user.AvatarURL,
		&user.IsActive, &user.IsEmailVerified, &user.EmailVerifiedAt,
		&user.FailedLoginCount, &user.LockedUntil, &user.LastLoginAt,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
		&user.DeletedAt, &user.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find user by username", logger.Err(err))
		return nil, err
	}

	return &user, nil
}

// FindByEmailOrUsername finds a user by email or username
func (r *UserRepository) FindByEmailOrUsername(ctx context.Context, login string) (*domain.User, error) {
	query := `
		SELECT id, email, username, COALESCE(password_hash, '') AS password_hash, full_name, phone, avatar_url,
		       is_active, is_email_verified, email_verified_at,
		       failed_login_count, locked_until, last_login_at,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.users
		WHERE (email = $1 OR username = $1) AND deleted_at IS NULL
	`

	var user domain.User
	err := r.db.QueryRow(ctx, query, login).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&user.FullName, &user.Phone, &user.AvatarURL,
		&user.IsActive, &user.IsEmailVerified, &user.EmailVerifiedAt,
		&user.FailedLoginCount, &user.LockedUntil, &user.LastLoginAt,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
		&user.DeletedAt, &user.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find user by email or username", logger.Err(err))
		return nil, err
	}

	return &user, nil
}

// FindAll finds all users with pagination and filtering.
//
// Tenant visibility rules (applied when params.ScopeCompanyID is set, i.e. the
// caller is NOT a super admin):
//
//  1. User has a membership in core.company_users for the scope company itself
//     or any of its descendants (traversed recursively via parent_id), OR
//  2. User was created by the caller (fallback, so freshly-created users that
//     have not yet been attached to a company are still visible to their
//     creator).
//
// The tree walk is one-directional (root → descendants), so a subsidiary admin
// cannot see users of its parent holding company. Super admins pass nil for
// ScopeCompanyID to bypass this filter entirely.
func (r *UserRepository) FindAll(ctx context.Context, params *dto.UserQueryParams) ([]domain.User, int64, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "u.deleted_at IS NULL")

	// Tenant scope: visible_companies = scope company + all descendants.
	// Build as a CTE prefix and reference it from the WHERE clause via EXISTS.
	var ctePrefix string
	if params.ScopeCompanyID != nil {
		ctePrefix = fmt.Sprintf(`
			WITH RECURSIVE visible_companies AS (
				SELECT id FROM core.companies
				 WHERE id = $%d AND deleted_at IS NULL
				UNION ALL
				SELECT c.id FROM core.companies c
				  JOIN visible_companies v ON c.parent_id = v.id
				 WHERE c.deleted_at IS NULL
			)
		`, argIndex)
		args = append(args, *params.ScopeCompanyID)
		argIndex++

		visibilityClause := `EXISTS (
			SELECT 1 FROM core.company_users cu
			 WHERE cu.user_id = u.id
			   AND cu.deleted_at IS NULL
			   AND cu.company_id IN (SELECT id FROM visible_companies)
		)`
		if params.ScopeCreatedBy != nil {
			visibilityClause = fmt.Sprintf("(%s OR u.created_by = $%d)", visibilityClause, argIndex)
			args = append(args, *params.ScopeCreatedBy)
			argIndex++
		}
		conditions = append(conditions, visibilityClause)
	}

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf(
			"(u.email ILIKE $%d OR u.username ILIKE $%d OR u.full_name ILIKE $%d)",
			argIndex, argIndex, argIndex,
		))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	if params.IsActive != nil {
		conditions = append(conditions, fmt.Sprintf("u.is_active = $%d", argIndex))
		args = append(args, *params.IsActive)
		argIndex++
	}

	if params.BranchID != nil {
		// A user matches the branch filter when either:
		//  (a) they have an explicit assignment to this branch, OR
		//  (b) they have no branch assignments at all (unrestricted access —
		//      consistent with BranchScope middleware semantics).
		conditions = append(conditions, fmt.Sprintf(`(
			EXISTS (
				SELECT 1 FROM core.user_branches ub
				 WHERE ub.user_id = u.id
				   AND ub.branch_id = $%d
				   AND ub.deleted_at IS NULL
			)
			OR NOT EXISTS (
				SELECT 1 FROM core.user_branches ub
				 WHERE ub.user_id = u.id
				   AND ub.deleted_at IS NULL
			)
		)`, argIndex))
		args = append(args, *params.BranchID)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("%sSELECT COUNT(*) FROM core.users u WHERE %s", ctePrefix, whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count users", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`%sSELECT u.id, u.email, u.username, COALESCE(u.password_hash, '') AS password_hash, u.full_name, u.phone, u.avatar_url,
		       u.is_active, u.is_email_verified, u.email_verified_at,
		       u.failed_login_count, u.locked_until, u.last_login_at,
		       u.created_at, u.created_by, u.updated_at, u.updated_by, u.deleted_at, u.deleted_by
		FROM core.users u
		WHERE %s
		ORDER BY u.created_at DESC
		LIMIT $%d OFFSET $%d
	`, ctePrefix, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find users", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var user domain.User
		err := rows.Scan(
			&user.ID, &user.Email, &user.Username, &user.PasswordHash,
			&user.FullName, &user.Phone, &user.AvatarURL,
			&user.IsActive, &user.IsEmailVerified, &user.EmailVerifiedAt,
			&user.FailedLoginCount, &user.LockedUntil, &user.LastLoginAt,
			&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
			&user.DeletedAt, &user.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan user", logger.Err(err))
			return nil, 0, err
		}
		users = append(users, user)
	}

	return users, total, nil
}

// Update updates a user
func (r *UserRepository) Update(ctx context.Context, user *domain.User) error {
	query := `
		UPDATE core.users
		SET email = $2, username = $3, full_name = $4, phone = $5, avatar_url = $6,
		    is_active = $7, updated_at = $8, updated_by = $9
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query,
		user.ID, user.Email, user.Username, user.FullName, user.Phone, user.AvatarURL,
		user.IsActive, user.UpdatedAt, user.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to update user", logger.Err(err))
		return err
	}

	return nil
}

// SoftDelete soft deletes a user
func (r *UserRepository) SoftDelete(ctx context.Context, id, deletedBy string) error {
	query := `
		UPDATE core.users
		SET deleted_at = $2, deleted_by = $3, updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NULL
	`

	now := time.Now()
	_, err := r.db.Exec(ctx, query, id, now, deletedBy)
	if err != nil {
		logger.Error("Failed to soft delete user", logger.Err(err))
		return err
	}

	return nil
}

// UpdatePassword updates user password
func (r *UserRepository) UpdatePassword(ctx context.Context, id, passwordHash, updatedBy string) error {
	query := `
		UPDATE core.users
		SET password_hash = $2, updated_at = $3, updated_by = $4
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, id, passwordHash, time.Now(), updatedBy)
	if err != nil {
		logger.Error("Failed to update password", logger.Err(err))
		return err
	}

	return nil
}

// UpdateLastLogin updates last login timestamp
func (r *UserRepository) UpdateLastLogin(ctx context.Context, id string) error {
	query := `
		UPDATE core.users
		SET last_login_at = $2, failed_login_count = 0, locked_until = NULL
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, id, time.Now())
	if err != nil {
		logger.Error("Failed to update last login", logger.Err(err))
		return err
	}

	return nil
}

// IncrementFailedLogin increments failed login count
func (r *UserRepository) IncrementFailedLogin(ctx context.Context, id string) error {
	query := `
		UPDATE core.users
		SET failed_login_count = failed_login_count + 1
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		logger.Error("Failed to increment failed login count", logger.Err(err))
		return err
	}

	return nil
}

// LockUser locks a user account
func (r *UserRepository) LockUser(ctx context.Context, id string, until time.Time) error {
	query := `
		UPDATE core.users
		SET locked_until = $2
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, id, until)
	if err != nil {
		logger.Error("Failed to lock user", logger.Err(err))
		return err
	}

	return nil
}

// VerifyEmail marks user email as verified
func (r *UserRepository) VerifyEmail(ctx context.Context, id string) error {
	query := `
		UPDATE core.users
		SET is_email_verified = true, email_verified_at = $2
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, id, time.Now())
	if err != nil {
		logger.Error("Failed to verify email", logger.Err(err))
		return err
	}

	return nil
}

// EmailExists checks if email already exists
func (r *UserRepository) EmailExists(ctx context.Context, email string, excludeID *string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM core.users WHERE email = $1 AND deleted_at IS NULL`
	args := []interface{}{email}

	if excludeID != nil {
		query += ` AND id != $2`
		args = append(args, *excludeID)
	}
	query += `)`

	var exists bool
	err := r.db.QueryRow(ctx, query, args...).Scan(&exists)
	if err != nil {
		logger.Error("Failed to check email existence", logger.Err(err))
		return false, err
	}

	return exists, nil
}

// UsernameExists checks if username already exists
func (r *UserRepository) UsernameExists(ctx context.Context, username string, excludeID *string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM core.users WHERE username = $1 AND deleted_at IS NULL`
	args := []interface{}{username}

	if excludeID != nil {
		query += ` AND id != $2`
		args = append(args, *excludeID)
	}
	query += `)`

	var exists bool
	err := r.db.QueryRow(ctx, query, args...).Scan(&exists)
	if err != nil {
		logger.Error("Failed to check username existence", logger.Err(err))
		return false, err
	}

	return exists, nil
}
