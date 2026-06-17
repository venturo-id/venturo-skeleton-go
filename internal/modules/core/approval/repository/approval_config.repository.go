package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/approval/domain"
	"venturo-skeleton-go/internal/modules/core/approval/dto"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type ApprovalConfigRepository struct {
	db *pgxpool.Pool
}

func NewApprovalConfigRepository(db *pgxpool.Pool) *ApprovalConfigRepository {
	return &ApprovalConfigRepository{db: db}
}

const approvalConfigColumns = `
	id, company_id, branch_id, feature_key, is_active,
	created_at, created_by, updated_at, updated_by, deleted_at, deleted_by
`

func scanApprovalConfig(row pgx.Row, c *domain.ApprovalConfig) error {
	return row.Scan(
		&c.ID, &c.CompanyID, &c.BranchID, &c.FeatureKey, &c.IsActive,
		&c.CreatedAt, &c.CreatedBy, &c.UpdatedAt, &c.UpdatedBy,
		&c.DeletedAt, &c.DeletedBy,
	)
}

// ─── CREATE ─────────────────────────────────────────────────────

// Create inserts a config + its levels in one transaction.
func (r *ApprovalConfigRepository) Create(ctx context.Context, config *domain.ApprovalConfig) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.insertConfigTx(ctx, tx, config); err != nil {
		return err
	}
	for i := range config.Levels {
		config.Levels[i].ApprovalConfigID = config.ID
		if err := r.insertLevelTx(ctx, tx, &config.Levels[i]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *ApprovalConfigRepository) insertConfigTx(ctx context.Context, tx pgx.Tx, c *domain.ApprovalConfig) error {
	query := `
		INSERT INTO core.approval_configs (
			id, company_id, branch_id, feature_key, is_active,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := tx.Exec(ctx, query,
		c.ID, c.CompanyID, c.BranchID, c.FeatureKey, c.IsActive,
		c.CreatedAt, c.CreatedBy, c.UpdatedAt, c.UpdatedBy,
	)
	if err != nil {
		logger.Error("Failed to insert approval_config", logger.Err(err))
		return err
	}
	return nil
}

func (r *ApprovalConfigRepository) insertLevelTx(ctx context.Context, tx pgx.Tx, l *domain.ApprovalConfigLevel) error {
	query := `
		INSERT INTO core.approval_config_levels (
			id, approval_config_id, level, role_id, approver_user_ids,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := tx.Exec(ctx, query,
		l.ID, l.ApprovalConfigID, l.Level, l.RoleID, l.ApproverUserIDs,
		l.CreatedAt, l.UpdatedAt,
	)
	if err != nil {
		logger.Error("Failed to insert approval_config_level", logger.Err(err))
		return err
	}
	return nil
}

// ─── READ ───────────────────────────────────────────────────────

// FindByID loads a config (not its levels).
func (r *ApprovalConfigRepository) FindByID(ctx context.Context, id, companyID string) (*domain.ApprovalConfig, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM core.approval_configs
		WHERE id = $1 AND company_id = $2 AND deleted_at IS NULL
	`, approvalConfigColumns)

	var c domain.ApprovalConfig
	err := scanApprovalConfig(r.db.QueryRow(ctx, query, id, companyID), &c)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, derrors.WrapErrorf(err, derrors.ErrorCodeCustomNotFound, "Approval config %s not found", id)
		}
		logger.Error("Failed to find approval_config", logger.Err(err))
		return nil, derrors.WrapErrorf(err, derrors.ErrorCodeUnknown, "query approval config")
	}
	return &c, nil
}

// FindActive resolves the active config for a (company, branch, feature_key)
// tuple. Used at submission time. Returns nil if none exists.
//
// tx may be nil to use the pool.
func (r *ApprovalConfigRepository) FindActive(ctx context.Context, tx pgx.Tx, companyID, branchID, featureKey string) (*domain.ApprovalConfig, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM core.approval_configs
		WHERE company_id = $1
		  AND branch_id  = $2
		  AND feature_key = $3
		  AND is_active  = TRUE
		  AND deleted_at IS NULL
		LIMIT 1
	`, approvalConfigColumns)

	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, query, companyID, branchID, featureKey)
	} else {
		row = r.db.QueryRow(ctx, query, companyID, branchID, featureKey)
	}

	var c domain.ApprovalConfig
	if err := scanApprovalConfig(row, &c); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// FindAll lists configs scoped to a company with optional filters.
func (r *ApprovalConfigRepository) FindAll(ctx context.Context, companyID string, params *dto.ApprovalConfigQueryParams) ([]domain.ApprovalConfig, error) {
	conditions := []string{"company_id = $1", "deleted_at IS NULL"}
	args := []interface{}{companyID}
	idx := 2

	if params.BranchID != "" {
		conditions = append(conditions, fmt.Sprintf("branch_id = $%d", idx))
		args = append(args, params.BranchID)
		idx++
	}
	if params.FeatureKey != "" {
		conditions = append(conditions, fmt.Sprintf("feature_key = $%d", idx))
		args = append(args, params.FeatureKey)
		idx++
	}

	query := fmt.Sprintf(`
		SELECT %s FROM core.approval_configs
		WHERE %s
		ORDER BY feature_key, branch_id
	`, approvalConfigColumns, strings.Join(conditions, " AND "))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.ApprovalConfig
	for rows.Next() {
		var c domain.ApprovalConfig
		if err := scanApprovalConfig(rows, &c); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, nil
}

// FindLevels loads the ordered level list for a config. Accepts optional tx.
func (r *ApprovalConfigRepository) FindLevels(ctx context.Context, tx pgx.Tx, approvalConfigID string) ([]domain.ApprovalConfigLevel, error) {
	query := `
		SELECT id, approval_config_id, level, role_id, approver_user_ids, created_at, updated_at
		FROM core.approval_config_levels
		WHERE approval_config_id = $1
		ORDER BY level ASC
	`
	var rows pgx.Rows
	var err error
	if tx != nil {
		rows, err = tx.Query(ctx, query, approvalConfigID)
	} else {
		rows, err = r.db.Query(ctx, query, approvalConfigID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var levels []domain.ApprovalConfigLevel
	for rows.Next() {
		var l domain.ApprovalConfigLevel
		if err := rows.Scan(
			&l.ID, &l.ApprovalConfigID, &l.Level, &l.RoleID, &l.ApproverUserIDs,
			&l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, err
		}
		levels = append(levels, l)
	}
	return levels, nil
}

// ─── UPDATE ─────────────────────────────────────────────────────

// ReplaceLevels deletes all existing levels for a config and inserts the new
// set. Done in a single transaction.
func (r *ApprovalConfigRepository) Update(ctx context.Context, config *domain.ApprovalConfig, replaceLevels bool) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	updateQuery := `
		UPDATE core.approval_configs
		SET is_active = $2, updated_at = $3, updated_by = $4
		WHERE id = $1 AND deleted_at IS NULL
	`
	if _, err := tx.Exec(ctx, updateQuery,
		config.ID, config.IsActive, config.UpdatedAt, config.UpdatedBy,
	); err != nil {
		logger.Error("Failed to update approval_config", logger.Err(err))
		return err
	}

	if replaceLevels {
		if _, err := tx.Exec(ctx,
			`DELETE FROM core.approval_config_levels WHERE approval_config_id = $1`,
			config.ID,
		); err != nil {
			return err
		}
		for i := range config.Levels {
			config.Levels[i].ApprovalConfigID = config.ID
			if err := r.insertLevelTx(ctx, tx, &config.Levels[i]); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// SoftDelete soft-deletes a config. Levels are left intact so past requests
// can still reference their origin.
func (r *ApprovalConfigRepository) SoftDelete(ctx context.Context, id, companyID, deletedBy string) error {
	query := `
		UPDATE core.approval_configs
		SET deleted_at = $3, deleted_by = $4, updated_at = $3, updated_by = $4
		WHERE id = $1 AND company_id = $2 AND deleted_at IS NULL
	`
	_, err := r.db.Exec(ctx, query, id, companyID, time.Now(), deletedBy)
	return err
}
