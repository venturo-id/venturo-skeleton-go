package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/approval/domain"
	"venturo-skeleton-go/internal/modules/core/approval/dto"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type ApprovalRequestRepository struct {
	db *pgxpool.Pool
}

func NewApprovalRequestRepository(db *pgxpool.Pool) *ApprovalRequestRepository {
	return &ApprovalRequestRepository{db: db}
}

// Pool exposes the underlying pool so services can start their own transaction
// and then hand tx back into the *Tx helpers below.
func (r *ApprovalRequestRepository) Pool() *pgxpool.Pool { return r.db }

const approvalRequestColumns = `
	id, company_id, branch_id, feature_key, reff_type, reff_id,
	approval_config_id, status, current_level, total_levels,
	submitted_at, submitted_by, finalized_at, finalized_by, reject_reason,
	created_at
`

func scanApprovalRequest(row pgx.Row, req *domain.ApprovalRequest) error {
	return row.Scan(
		&req.ID, &req.CompanyID, &req.BranchID, &req.FeatureKey,
		&req.ReffType, &req.ReffID, &req.ApprovalConfigID,
		&req.Status, &req.CurrentLevel, &req.TotalLevels,
		&req.SubmittedAt, &req.SubmittedBy,
		&req.FinalizedAt, &req.FinalizedBy, &req.RejectReason,
		&req.CreatedAt,
	)
}

// ─── CREATE ─────────────────────────────────────────────────────

// InsertRequestTx inserts an approval_requests row inside a caller-managed tx.
func (r *ApprovalRequestRepository) InsertRequestTx(ctx context.Context, tx pgx.Tx, req *domain.ApprovalRequest) error {
	query := `
		INSERT INTO core.approval_requests (
			id, company_id, branch_id, feature_key, reff_type, reff_id,
			approval_config_id, status, current_level, total_levels,
			submitted_at, submitted_by, finalized_at, finalized_by, reject_reason,
			created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`
	_, err := tx.Exec(ctx, query,
		req.ID, req.CompanyID, req.BranchID, req.FeatureKey, req.ReffType, req.ReffID,
		req.ApprovalConfigID, req.Status, req.CurrentLevel, req.TotalLevels,
		req.SubmittedAt, req.SubmittedBy, req.FinalizedAt, req.FinalizedBy, req.RejectReason,
		req.CreatedAt,
	)
	if err != nil {
		logger.Error("Failed to insert approval_request", logger.Err(err))
		return err
	}
	return nil
}

// InsertLevelTx inserts one approval_request_levels row inside a tx.
func (r *ApprovalRequestRepository) InsertLevelTx(ctx context.Context, tx pgx.Tx, l *domain.ApprovalRequestLevel) error {
	query := `
		INSERT INTO core.approval_request_levels (
			id, approval_request_id, level, role_id, role_name,
			eligible_approver_ids, status, action_by, action_at, comment, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`
	_, err := tx.Exec(ctx, query,
		l.ID, l.ApprovalRequestID, l.Level, l.RoleID, l.RoleName,
		l.EligibleApproverIDs, l.Status, l.ActionBy, l.ActionAt, l.Comment, l.CreatedAt,
	)
	if err != nil {
		logger.Error("Failed to insert approval_request_level", logger.Err(err))
		return err
	}
	return nil
}

// ─── READ ───────────────────────────────────────────────────────

// FindByID loads a request without its levels.
func (r *ApprovalRequestRepository) FindByID(ctx context.Context, tx pgx.Tx, id string) (*domain.ApprovalRequest, error) {
	query := fmt.Sprintf(`SELECT %s FROM core.approval_requests WHERE id = $1`, approvalRequestColumns)
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, query, id)
	} else {
		row = r.db.QueryRow(ctx, query, id)
	}

	var req domain.ApprovalRequest
	if err := scanApprovalRequest(row, &req); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, derrors.WrapErrorf(err, derrors.ErrorCodeCustomNotFound, "Approval request %s not found", id)
		}
		return nil, derrors.WrapErrorf(err, derrors.ErrorCodeUnknown, "query approval request")
	}
	return &req, nil
}

// FindActiveByDoc returns the currently-waiting request for a (reff_type,
// reff_id). Used by callers that orchestrate approvals on a document.
func (r *ApprovalRequestRepository) FindActiveByDoc(ctx context.Context, tx pgx.Tx, reffType, reffID string) (*domain.ApprovalRequest, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM core.approval_requests
		WHERE reff_type = $1 AND reff_id = $2 AND status = 'waiting'
		LIMIT 1
	`, approvalRequestColumns)
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, query, reffType, reffID)
	} else {
		row = r.db.QueryRow(ctx, query, reffType, reffID)
	}

	var req domain.ApprovalRequest
	if err := scanApprovalRequest(row, &req); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

// FindLatestByDoc returns the most recently submitted request for a document,
// regardless of status. Resubmit-after-reject may produce multiple historical
// requests per doc — this picks the latest one for FE status/history dialogs.
// Scoped by company_id to prevent cross-tenant reads.
func (r *ApprovalRequestRepository) FindLatestByDoc(ctx context.Context, tx pgx.Tx, companyID, reffType, reffID string) (*domain.ApprovalRequest, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM core.approval_requests
		WHERE company_id = $1 AND reff_type = $2 AND reff_id = $3
		ORDER BY submitted_at DESC
		LIMIT 1
	`, approvalRequestColumns)
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, query, companyID, reffType, reffID)
	} else {
		row = r.db.QueryRow(ctx, query, companyID, reffType, reffID)
	}

	var req domain.ApprovalRequest
	if err := scanApprovalRequest(row, &req); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

// FindLevels loads the ordered level list for a request.
func (r *ApprovalRequestRepository) FindLevels(ctx context.Context, tx pgx.Tx, requestID string) ([]domain.ApprovalRequestLevel, error) {
	query := `
		SELECT id, approval_request_id, level, role_id, role_name,
		       eligible_approver_ids, status, action_by, action_at, comment, created_at
		FROM core.approval_request_levels
		WHERE approval_request_id = $1
		ORDER BY level ASC
	`
	var rows pgx.Rows
	var err error
	if tx != nil {
		rows, err = tx.Query(ctx, query, requestID)
	} else {
		rows, err = r.db.Query(ctx, query, requestID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var levels []domain.ApprovalRequestLevel
	for rows.Next() {
		var l domain.ApprovalRequestLevel
		if err := rows.Scan(
			&l.ID, &l.ApprovalRequestID, &l.Level, &l.RoleID, &l.RoleName,
			&l.EligibleApproverIDs, &l.Status, &l.ActionBy, &l.ActionAt, &l.Comment,
			&l.CreatedAt,
		); err != nil {
			return nil, err
		}
		levels = append(levels, l)
	}
	return levels, nil
}

// FindAll returns a paginated list with filters. When OnlyMine is true, the
// query restricts to requests whose current level has the user in its
// eligible_approver_ids snapshot.
func (r *ApprovalRequestRepository) FindAll(ctx context.Context, companyID, userID string, params *dto.ApprovalRequestQueryParams) ([]domain.ApprovalRequest, int64, error) {
	conditions := []string{"r.company_id = $1"}
	args := []interface{}{companyID}
	idx := 2

	if params.FeatureKey != "" {
		conditions = append(conditions, fmt.Sprintf("r.feature_key = $%d", idx))
		args = append(args, params.FeatureKey)
		idx++
	}
	if params.BranchID != "" {
		conditions = append(conditions, fmt.Sprintf("r.branch_id = $%d", idx))
		args = append(args, params.BranchID)
		idx++
	}
	if params.Status != "" {
		conditions = append(conditions, fmt.Sprintf("r.status = $%d", idx))
		args = append(args, params.Status)
		idx++
	}

	joinClause := ""
	if params.OnlyMine {
		// Restrict to the row that matches the request's current level.
		joinClause = `
			JOIN core.approval_request_levels l
			  ON l.approval_request_id = r.id
			 AND l.level = r.current_level
		`
		conditions = append(conditions, "r.status = 'waiting'")
		conditions = append(conditions, fmt.Sprintf("$%d = ANY(l.eligible_approver_ids)", idx))
		args = append(args, userID)
		idx++
	}

	where := strings.Join(conditions, " AND ")

	countQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT r.id)
		FROM core.approval_requests r
		%s
		WHERE %s
	`, joinClause, where)

	var total int64
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(`
		SELECT %s
		FROM core.approval_requests r
		%s
		WHERE %s
		ORDER BY r.submitted_at DESC
		LIMIT $%d OFFSET $%d
	`,
		prefixColumns(approvalRequestColumns, "r"),
		joinClause, where, idx, idx+1,
	)
	args = append(args, params.Limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []domain.ApprovalRequest
	for rows.Next() {
		var req domain.ApprovalRequest
		if err := scanApprovalRequest(rows, &req); err != nil {
			return nil, 0, err
		}
		list = append(list, req)
	}
	return list, total, nil
}

// ─── UPDATE ─────────────────────────────────────────────────────

// UpdateRequestStatusTx updates status/current_level/finalized_* on a request.
func (r *ApprovalRequestRepository) UpdateRequestStatusTx(
	ctx context.Context, tx pgx.Tx,
	id, status string, currentLevel *int16,
	finalizedAt *string, finalizedBy *string, rejectReason *string,
) error {
	query := `
		UPDATE core.approval_requests
		SET status = $2,
		    current_level = $3,
		    finalized_at = COALESCE($4::timestamptz, finalized_at),
		    finalized_by = COALESCE($5::uuid, finalized_by),
		    reject_reason = COALESCE($6, reject_reason)
		WHERE id = $1
	`
	_, err := tx.Exec(ctx, query, id, status, currentLevel, finalizedAt, finalizedBy, rejectReason)
	return err
}

// MarkLevelActionTx sets a level row to approved/rejected/skipped.
func (r *ApprovalRequestRepository) MarkLevelActionTx(
	ctx context.Context, tx pgx.Tx,
	requestID string, level int16,
	status string, actionBy string, actionAt string, comment *string,
) error {
	query := `
		UPDATE core.approval_request_levels
		SET status = $3, action_by = $4, action_at = $5::timestamptz, comment = $6
		WHERE approval_request_id = $1 AND level = $2
	`
	_, err := tx.Exec(ctx, query, requestID, level, status, actionBy, actionAt, comment)
	return err
}

// SkipRemainingPendingTx marks all still-pending levels as skipped — used on
// reject/cancel to keep the level audit trail explicit.
func (r *ApprovalRequestRepository) SkipRemainingPendingTx(ctx context.Context, tx pgx.Tx, requestID string) error {
	query := `
		UPDATE core.approval_request_levels
		SET status = 'skipped'
		WHERE approval_request_id = $1 AND status = 'pending'
	`
	_, err := tx.Exec(ctx, query, requestID)
	return err
}

// ─── helpers ────────────────────────────────────────────────────

// prefixColumns turns "a, b, c" into "r.a, r.b, r.c" for aliased SELECTs.
func prefixColumns(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = alias + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}
