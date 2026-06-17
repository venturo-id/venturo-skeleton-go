package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/branch/domain"
	"venturo-skeleton-go/internal/modules/core/branch/dto"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type BranchRepository struct {
	db *pgxpool.Pool
}

func NewBranchRepository(db *pgxpool.Pool) *BranchRepository {
	return &BranchRepository{db: db}
}

// Create creates a new branch
func (r *BranchRepository) Create(ctx context.Context, branch *domain.Branch) error {
	query := `
		INSERT INTO core.branches (
			id, company_id, code, name, logo_url, sort, is_default, is_active,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err := r.db.Exec(ctx, query,
		branch.ID, branch.CompanyID, branch.Code, branch.Name, branch.LogoURL,
		branch.Sort, branch.IsDefault, branch.IsActive,
		branch.CreatedAt, branch.CreatedBy, branch.UpdatedAt, branch.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to create branch", logger.Err(err))
		return err
	}

	return nil
}

// FindByID finds a branch by ID
func (r *BranchRepository) FindByID(ctx context.Context, id string) (*domain.Branch, error) {
	query := `
		SELECT id, company_id, code, name, logo_url, sort, is_default, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.branches
		WHERE id = $1 AND deleted_at IS NULL
	`

	var branch domain.Branch
	err := r.db.QueryRow(ctx, query, id).Scan(
		&branch.ID, &branch.CompanyID, &branch.Code, &branch.Name, &branch.LogoURL,
		&branch.Sort, &branch.IsDefault, &branch.IsActive,
		&branch.CreatedAt, &branch.CreatedBy, &branch.UpdatedAt, &branch.UpdatedBy,
		&branch.DeletedAt, &branch.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, derrors.WrapErrorf(err, derrors.ErrorCodeCustomNotFound, "Branch %s not found", id)
		}
		logger.Error("Failed to find branch by ID", logger.Err(err))
		return nil, derrors.WrapErrorf(err, derrors.ErrorCodeUnknown, "query branch")
	}

	return &branch, nil
}

// FindAll finds all branches for a company with pagination and filtering
func (r *BranchRepository) FindAll(ctx context.Context, companyID string, params *dto.BranchQueryParams) ([]domain.Branch, int64, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "deleted_at IS NULL")

	conditions = append(conditions, fmt.Sprintf("company_id = $%d", argIndex))
	args = append(args, companyID)
	argIndex++

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(name ILIKE $%d OR code ILIKE $%d)", argIndex, argIndex))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	if params.IsActive != nil {
		conditions = append(conditions, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, *params.IsActive)
		argIndex++
	}

	if params.ScopeBranchIDs != nil {
		conditions = append(conditions, fmt.Sprintf("id = ANY($%d)", argIndex))
		args = append(args, params.ScopeBranchIDs)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM core.branches WHERE %s", whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count branches", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT id, company_id, code, name, logo_url, sort, is_default, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.branches
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find branches", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var branches []domain.Branch
	for rows.Next() {
		var branch domain.Branch
		err := rows.Scan(
			&branch.ID, &branch.CompanyID, &branch.Code, &branch.Name, &branch.LogoURL,
			&branch.Sort, &branch.IsDefault, &branch.IsActive,
			&branch.CreatedAt, &branch.CreatedBy, &branch.UpdatedAt, &branch.UpdatedBy,
			&branch.DeletedAt, &branch.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan branch", logger.Err(err))
			return nil, 0, err
		}
		branches = append(branches, branch)
	}

	return branches, total, nil
}

// FindAllByCompanies lists branches across multiple companies with pagination
// and filtering. Use this when the caller spans multiple tenants (e.g. a user
// listing every branch they have access to across their company memberships).
//
// companyIDs semantics:
//   - nil          → no company filter (caller has cross-tenant visibility, e.g. super_admin)
//   - empty slice  → short-circuit empty result (caller resolved no companies)
//   - non-empty    → restrict to these companies
//
// ScopeBranchIDs is still respected for user_branches narrowing.
func (r *BranchRepository) FindAllByCompanies(ctx context.Context, companyIDs []string, params *dto.BranchQueryParams) ([]domain.Branch, int64, error) {
	if companyIDs != nil && len(companyIDs) == 0 {
		return []domain.Branch{}, 0, nil
	}

	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "deleted_at IS NULL")

	if companyIDs != nil {
		conditions = append(conditions, fmt.Sprintf("company_id = ANY($%d)", argIndex))
		args = append(args, companyIDs)
		argIndex++
	}

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(name ILIKE $%d OR code ILIKE $%d)", argIndex, argIndex))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	if params.IsActive != nil {
		conditions = append(conditions, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, *params.IsActive)
		argIndex++
	}

	if params.ScopeBranchIDs != nil {
		conditions = append(conditions, fmt.Sprintf("id = ANY($%d)", argIndex))
		args = append(args, params.ScopeBranchIDs)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM core.branches WHERE %s", whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count branches by companies", logger.Err(err))
		return nil, 0, err
	}

	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT id, company_id, code, name, logo_url, sort, is_default, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.branches
		WHERE %s
		ORDER BY company_id, sort ASC, created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find branches by companies", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var branches []domain.Branch
	for rows.Next() {
		var branch domain.Branch
		err := rows.Scan(
			&branch.ID, &branch.CompanyID, &branch.Code, &branch.Name, &branch.LogoURL,
			&branch.Sort, &branch.IsDefault, &branch.IsActive,
			&branch.CreatedAt, &branch.CreatedBy, &branch.UpdatedAt, &branch.UpdatedBy,
			&branch.DeletedAt, &branch.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan branch", logger.Err(err))
			return nil, 0, err
		}
		branches = append(branches, branch)
	}

	return branches, total, nil
}

// Update updates a branch
func (r *BranchRepository) Update(ctx context.Context, branch *domain.Branch) error {
	query := `
		UPDATE core.branches
		SET code = $2, name = $3, logo_url = $4, sort = $5, is_default = $6, is_active = $7,
		    updated_at = $8, updated_by = $9
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query,
		branch.ID, branch.Code, branch.Name, branch.LogoURL, branch.Sort,
		branch.IsDefault, branch.IsActive,
		branch.UpdatedAt, branch.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to update branch", logger.Err(err))
		return err
	}

	return nil
}

// SoftDelete soft deletes a branch
func (r *BranchRepository) SoftDelete(ctx context.Context, id, deletedBy string) error {
	query := `
		UPDATE core.branches
		SET deleted_at = $2, deleted_by = $3, updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NULL
	`

	now := time.Now()
	_, err := r.db.Exec(ctx, query, id, now, deletedBy)
	if err != nil {
		logger.Error("Failed to soft delete branch", logger.Err(err))
		return err
	}

	return nil
}

// GenerateNextCode generates the next sequential branch code for a company.
// Uses a transaction-scoped advisory lock per (company, prefix) to serialize
// concurrent generators for the same tenant without blocking other tenants.
// The unique index branches_company_code_key remains the final safety net.
func (r *BranchRepository) GenerateNextCode(ctx context.Context, companyID, prefix string) (string, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	lockKey := fmt.Sprintf("branch_code:%s:%s", companyID, prefix)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", lockKey); err != nil {
		logger.Error("Failed to acquire advisory lock for branch code", logger.Err(err))
		return "", err
	}

	pattern := "^" + prefix + `-(\d+)$`
	var nextNum int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(CAST(SUBSTRING(code FROM $2) AS INT)), 0) + 1
		FROM core.branches
		WHERE company_id = $1
		  AND code ~ $3
		  AND deleted_at IS NULL
	`, companyID, pattern, pattern).Scan(&nextNum)
	if err != nil {
		logger.Error("Failed to compute next branch code", logger.Err(err))
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	width := 2
	if nextNum >= 100 {
		width = len(fmt.Sprintf("%d", nextNum))
	}
	return fmt.Sprintf("%s-%0*d", prefix, width, nextNum), nil
}

// ClearDefault clears the default flag for all branches of a company
func (r *BranchRepository) ClearDefault(ctx context.Context, companyID string) error {
	query := `
		UPDATE core.branches
		SET is_default = FALSE
		WHERE company_id = $1 AND is_default = TRUE AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, companyID)
	if err != nil {
		logger.Error("Failed to clear default branch", logger.Err(err))
		return err
	}

	return nil
}
