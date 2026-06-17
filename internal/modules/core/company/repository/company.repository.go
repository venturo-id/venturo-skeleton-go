package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/company/domain"
	"venturo-skeleton-go/internal/modules/core/company/dto"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type CompanyRepository struct {
	db *pgxpool.Pool
}

func NewCompanyRepository(db *pgxpool.Pool) *CompanyRepository {
	return &CompanyRepository{db: db}
}

// Create creates a new company
func (r *CompanyRepository) Create(ctx context.Context, company *domain.Company) error {
	query := `
		INSERT INTO core.companies (
			id, client_id, parent_id, name, type, logo_url, owner_id, sort, is_active,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err := r.db.Exec(ctx, query,
		company.ID, company.ClientID, company.ParentID, company.Name, company.Type,
		company.LogoURL, company.OwnerID,
		company.Sort, company.IsActive,
		company.CreatedAt, company.CreatedBy, company.UpdatedAt, company.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to create company", logger.Err(err))
		return err
	}

	return nil
}

// FindByID finds a company by ID
func (r *CompanyRepository) FindByID(ctx context.Context, id string) (*domain.Company, error) {
	query := `
		SELECT id, client_id, parent_id, name, type, logo_url, owner_id, sort, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.companies
		WHERE id = $1 AND deleted_at IS NULL
	`

	var company domain.Company
	err := r.db.QueryRow(ctx, query, id).Scan(
		&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
		&company.LogoURL, &company.OwnerID,
		&company.Sort, &company.IsActive,
		&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
		&company.DeletedAt, &company.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, derrors.WrapErrorf(err, derrors.ErrorCodeCustomNotFound, "Company %s not found", id)
		}
		logger.Error("Failed to find company by ID", logger.Err(err))
		return nil, derrors.WrapErrorf(err, derrors.ErrorCodeUnknown, "query company")
	}

	return &company, nil
}

// FindAll finds all companies with pagination and filtering
func (r *CompanyRepository) FindAll(ctx context.Context, params *dto.CompanyQueryParams) ([]domain.Company, int64, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "c.deleted_at IS NULL")

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf(
			"c.name ILIKE $%d",
			argIndex,
		))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	if params.ParentID != nil {
		conditions = append(conditions, fmt.Sprintf("c.parent_id = $%d", argIndex))
		args = append(args, *params.ParentID)
		argIndex++
	}

	if params.Type != nil {
		conditions = append(conditions, fmt.Sprintf("c.type = $%d", argIndex))
		args = append(args, *params.Type)
		argIndex++
	}

	if params.IsActive != nil {
		conditions = append(conditions, fmt.Sprintf("c.is_active = $%d", argIndex))
		args = append(args, *params.IsActive)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM core.companies c WHERE %s", whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count companies", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT c.id, c.client_id, c.parent_id, c.name, c.type, c.logo_url, c.owner_id,
		       c.sort, c.is_active,
		       c.created_at, c.created_by, c.updated_at, c.updated_by, c.deleted_at, c.deleted_by,
		       u.full_name
		FROM core.companies c
		LEFT JOIN core.users u ON u.id = c.owner_id
		WHERE %s
		ORDER BY c.sort ASC, c.name ASC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find companies", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var company domain.Company
		err := rows.Scan(
			&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
			&company.LogoURL, &company.OwnerID,
			&company.Sort, &company.IsActive,
			&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
			&company.DeletedAt, &company.DeletedBy,
			&company.OwnerName,
		)
		if err != nil {
			logger.Error("Failed to scan company", logger.Err(err))
			return nil, 0, err
		}
		companies = append(companies, company)
	}

	return companies, total, nil
}

// FindAllScoped finds companies scoped to user's companies and their descendants (using recursive CTE)
func (r *CompanyRepository) FindAllScoped(ctx context.Context, params *dto.CompanyQueryParams, companyIDs []string) ([]domain.Company, int64, error) {
	if len(companyIDs) == 0 {
		return []domain.Company{}, 0, nil
	}

	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "c.deleted_at IS NULL")

	// Scope: company itself + all descendants via recursive CTE
	conditions = append(conditions, fmt.Sprintf(
		`c.id IN (
			WITH RECURSIVE company_tree AS (
				SELECT id FROM core.companies WHERE id = ANY($%d) AND deleted_at IS NULL
				UNION ALL
				SELECT ch.id FROM core.companies ch
				JOIN company_tree ct ON ch.parent_id = ct.id
				WHERE ch.deleted_at IS NULL
			)
			SELECT id FROM company_tree
		)`,
		argIndex,
	))
	args = append(args, companyIDs)
	argIndex++

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf(
			"c.name ILIKE $%d",
			argIndex,
		))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	if params.ParentID != nil {
		conditions = append(conditions, fmt.Sprintf("c.parent_id = $%d", argIndex))
		args = append(args, *params.ParentID)
		argIndex++
	}

	if params.Type != nil {
		conditions = append(conditions, fmt.Sprintf("c.type = $%d", argIndex))
		args = append(args, *params.Type)
		argIndex++
	}

	if params.IsActive != nil {
		conditions = append(conditions, fmt.Sprintf("c.is_active = $%d", argIndex))
		args = append(args, *params.IsActive)
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM core.companies c WHERE %s", whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count scoped companies", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT c.id, c.client_id, c.parent_id, c.name, c.type, c.logo_url, c.owner_id,
		       c.sort, c.is_active,
		       c.created_at, c.created_by, c.updated_at, c.updated_by, c.deleted_at, c.deleted_by,
		       u.full_name
		FROM core.companies c
		LEFT JOIN core.users u ON u.id = c.owner_id
		WHERE %s
		ORDER BY c.sort ASC, c.name ASC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find scoped companies", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var company domain.Company
		err := rows.Scan(
			&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
			&company.LogoURL, &company.OwnerID,
			&company.Sort, &company.IsActive,
			&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
			&company.DeletedAt, &company.DeletedBy,
			&company.OwnerName,
		)
		if err != nil {
			logger.Error("Failed to scan scoped company", logger.Err(err))
			return nil, 0, err
		}
		companies = append(companies, company)
	}

	return companies, total, nil
}

// Update updates a company
func (r *CompanyRepository) Update(ctx context.Context, company *domain.Company) error {
	query := `
		UPDATE core.companies
		SET name = $2, type = $3, logo_url = $4,
		    owner_id = $5, sort = $6, is_active = $7,
		    updated_at = $8, updated_by = $9
		WHERE id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query,
		company.ID, company.Name, company.Type, company.LogoURL,
		company.OwnerID, company.Sort, company.IsActive,
		company.UpdatedAt, company.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to update company", logger.Err(err))
		return err
	}

	return nil
}

// SoftDelete soft deletes a company
func (r *CompanyRepository) SoftDelete(ctx context.Context, id, deletedBy string) error {
	query := `
		UPDATE core.companies
		SET deleted_at = $2, deleted_by = $3, updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NULL
	`

	now := time.Now()
	_, err := r.db.Exec(ctx, query, id, now, deletedBy)
	if err != nil {
		logger.Error("Failed to soft delete company", logger.Err(err))
		return err
	}

	return nil
}

// FindDeletedByID finds a soft-deleted company by ID
func (r *CompanyRepository) FindDeletedByID(ctx context.Context, id string) (*domain.Company, error) {
	query := `
		SELECT id, client_id, parent_id, name, type, logo_url, owner_id, sort, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.companies
		WHERE id = $1 AND deleted_at IS NOT NULL
	`

	var company domain.Company
	err := r.db.QueryRow(ctx, query, id).Scan(
		&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
		&company.LogoURL, &company.OwnerID,
		&company.Sort, &company.IsActive,
		&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
		&company.DeletedAt, &company.DeletedBy,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find deleted company by ID", logger.Err(err))
		return nil, err
	}

	return &company, nil
}

// FindAllDeleted finds all soft-deleted companies with pagination
func (r *CompanyRepository) FindAllDeleted(ctx context.Context, params *dto.CompanyTrashQueryParams) ([]domain.Company, int64, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	conditions = append(conditions, "c.deleted_at IS NOT NULL")

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf("c.name ILIKE $%d", argIndex))
		args = append(args, "%"+params.Search+"%")
		argIndex++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM core.companies c WHERE %s", whereClause)
	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count deleted companies", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT c.id, c.client_id, c.parent_id, c.name, c.type, c.logo_url, c.owner_id,
		       c.sort, c.is_active,
		       c.created_at, c.created_by, c.updated_at, c.updated_by, c.deleted_at, c.deleted_by,
		       u.full_name
		FROM core.companies c
		LEFT JOIN core.users u ON u.id = c.owner_id
		WHERE %s
		ORDER BY c.deleted_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find deleted companies", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var company domain.Company
		err := rows.Scan(
			&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
			&company.LogoURL, &company.OwnerID,
			&company.Sort, &company.IsActive,
			&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
			&company.DeletedAt, &company.DeletedBy,
			&company.OwnerName,
		)
		if err != nil {
			logger.Error("Failed to scan deleted company", logger.Err(err))
			return nil, 0, err
		}
		companies = append(companies, company)
	}

	return companies, total, nil
}

// Restore restores a soft-deleted company
func (r *CompanyRepository) Restore(ctx context.Context, id, updatedBy string) error {
	query := `
		UPDATE core.companies
		SET deleted_at = NULL, deleted_by = NULL,
		    updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NOT NULL
	`

	now := time.Now()
	_, err := r.db.Exec(ctx, query, id, now, updatedBy)
	if err != nil {
		logger.Error("Failed to restore company", logger.Err(err))
		return err
	}

	return nil
}

// GetChildren gets direct children of a company
func (r *CompanyRepository) GetChildren(ctx context.Context, companyID string) ([]domain.Company, error) {
	query := `
		SELECT id, client_id, parent_id, name, type, logo_url, owner_id, sort, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM core.companies
		WHERE parent_id = $1 AND deleted_at IS NULL
		ORDER BY name ASC
	`

	rows, err := r.db.Query(ctx, query, companyID)
	if err != nil {
		logger.Error("Failed to get children", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var company domain.Company
		err := rows.Scan(
			&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
			&company.LogoURL, &company.OwnerID,
			&company.Sort, &company.IsActive,
			&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
			&company.DeletedAt, &company.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan company", logger.Err(err))
			return nil, err
		}
		companies = append(companies, company)
	}

	return companies, nil
}

// GetAncestors gets all ancestors of a company using recursive CTE
func (r *CompanyRepository) GetAncestors(ctx context.Context, companyID string) ([]domain.Company, error) {
	query := `
		WITH RECURSIVE ancestors AS (
			SELECT c.*
			FROM core.companies c
			WHERE c.id = (SELECT parent_id FROM core.companies WHERE id = $1)
			  AND c.deleted_at IS NULL
			UNION ALL
			SELECT c.*
			FROM core.companies c
			JOIN ancestors a ON c.id = a.parent_id
			WHERE c.deleted_at IS NULL
		)
		SELECT id, client_id, parent_id, name, type, logo_url, owner_id, sort, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM ancestors
		ORDER BY sort ASC, name ASC
	`

	rows, err := r.db.Query(ctx, query, companyID)
	if err != nil {
		logger.Error("Failed to get ancestors", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var company domain.Company
		err := rows.Scan(
			&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
			&company.LogoURL, &company.OwnerID,
			&company.Sort, &company.IsActive,
			&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
			&company.DeletedAt, &company.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan company", logger.Err(err))
			return nil, err
		}
		companies = append(companies, company)
	}

	return companies, nil
}

// GetDescendants gets all descendants of a company using recursive CTE
func (r *CompanyRepository) GetDescendants(ctx context.Context, companyID string) ([]domain.Company, error) {
	query := `
		WITH RECURSIVE descendants AS (
			SELECT c.*
			FROM core.companies c
			WHERE c.parent_id = $1
			  AND c.deleted_at IS NULL
			UNION ALL
			SELECT c.*
			FROM core.companies c
			JOIN descendants d ON c.parent_id = d.id
			WHERE c.deleted_at IS NULL
		)
		SELECT id, client_id, parent_id, name, type, logo_url, owner_id, sort, is_active,
		       created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
		FROM descendants
		ORDER BY sort ASC, name ASC
	`

	rows, err := r.db.Query(ctx, query, companyID)
	if err != nil {
		logger.Error("Failed to get descendants", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var company domain.Company
		err := rows.Scan(
			&company.ID, &company.ClientID, &company.ParentID, &company.Name, &company.Type,
			&company.LogoURL, &company.OwnerID,
			&company.Sort, &company.IsActive,
			&company.CreatedAt, &company.CreatedBy, &company.UpdatedAt, &company.UpdatedBy,
			&company.DeletedAt, &company.DeletedBy,
		)
		if err != nil {
			logger.Error("Failed to scan company", logger.Err(err))
			return nil, err
		}
		companies = append(companies, company)
	}

	return companies, nil
}

// VisibleCompanyIDs returns rootCompanyID together with all descendant ids
// (recursive via parent_id). Used by user-branch scope validation so a
// non-super-admin caller can only assign branches that belong to companies
// they are allowed to operate on.
func (r *CompanyRepository) VisibleCompanyIDs(ctx context.Context, rootCompanyID string) ([]string, error) {
	const query = `
		WITH RECURSIVE visible AS (
			SELECT id FROM core.companies
			 WHERE id = $1 AND deleted_at IS NULL
			UNION ALL
			SELECT c.id FROM core.companies c
			  JOIN visible v ON c.parent_id = v.id
			 WHERE c.deleted_at IS NULL
		)
		SELECT id FROM visible
	`
	rows, err := r.db.Query(ctx, query, rootCompanyID)
	if err != nil {
		logger.Error("Failed to resolve visible company ids", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			logger.Error("Failed to scan visible company id", logger.Err(err))
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}
