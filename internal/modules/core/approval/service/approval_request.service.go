package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"venturo-skeleton-go/internal/modules/core/approval/domain"
	"venturo-skeleton-go/internal/modules/core/approval/dto"
	"venturo-skeleton-go/internal/modules/core/approval/repository"
	roleRepo "venturo-skeleton-go/internal/modules/core/role/repository"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

// ─── ERRORS ─────────────────────────────────────────────────────

var (
	ErrApprovalConfigNotConfigured = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Approval config not configured for this branch & feature")
	ErrRequestNotFound             = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Approval request not found")
	ErrRequestNotWaiting           = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "Approval request is not waiting")
	ErrNotEligibleApprover         = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "You are not an eligible approver at the current level")
	ErrRequestAlreadyExists        = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "An active approval request already exists for this document")
	ErrCannotCancel                = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Only the submitter can cancel a waiting request")
)

// ─── SERVICE ────────────────────────────────────────────────────

type ApprovalRequestService struct {
	configRepo  *repository.ApprovalConfigRepository
	requestRepo *repository.ApprovalRequestRepository
	roleRepo    *roleRepo.RoleRepository
}

func NewApprovalRequestService(
	configRepo *repository.ApprovalConfigRepository,
	requestRepo *repository.ApprovalRequestRepository,
	rRepo *roleRepo.RoleRepository,
) *ApprovalRequestService {
	return &ApprovalRequestService{
		configRepo:  configRepo,
		requestRepo: requestRepo,
		roleRepo:    rRepo,
	}
}

// ─── SUBMIT ─────────────────────────────────────────────────────

// Submit opens its own transaction. For callers that want to bundle this into
// a larger transaction (e.g. journal entry save + submit), use SubmitTx.
func (s *ApprovalRequestService) Submit(ctx context.Context, p dto.SubmitParams) (*dto.SubmitResult, error) {
	tx, err := s.requestRepo.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := s.SubmitTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// SubmitTx performs the submit flow inside a caller-managed transaction.
//
// Flow:
//  1. Resolve active approval_config for (company, branch, feature_key)
//  2. If none → error (per Opsi B: config is mandatory per branch)
//  3. Load levels
//  4. If 0 levels → auto-approve: insert request as 'approved' with no level rows
//  5. Otherwise snapshot each level into approval_request_levels and set
//     request status='waiting', current_level=1
func (s *ApprovalRequestService) SubmitTx(ctx context.Context, tx pgx.Tx, p dto.SubmitParams) (*dto.SubmitResult, error) {
	// Guard: a document can only have one active request at a time.
	existing, err := s.requestRepo.FindActiveByDoc(ctx, tx, p.ReffType, p.ReffID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrRequestAlreadyExists
	}

	config, err := s.configRepo.FindActive(ctx, tx, p.CompanyID, p.BranchID, p.FeatureKey)
	if err != nil {
		return nil, err
	}

	// Load levels from config; if no config exists treat as 0-level (auto-approve).
	var levels []domain.ApprovalConfigLevel
	if config != nil {
		levels, err = s.configRepo.FindLevels(ctx, tx, config.ID)
		if err != nil {
			return nil, err
		}
	}

	now := time.Now()
	request := &domain.ApprovalRequest{
		ID:          uuid.New().String(),
		CompanyID:   p.CompanyID,
		BranchID:    p.BranchID,
		FeatureKey:  p.FeatureKey,
		ReffType:    p.ReffType,
		ReffID:      p.ReffID,
		TotalLevels: int16(len(levels)),
		SubmittedAt: now,
		SubmittedBy: p.SubmittedBy,
		CreatedAt:   now,
	}
	if config != nil {
		request.ApprovalConfigID = &config.ID
	}

	autoApproved := len(levels) == 0
	if autoApproved {
		request.Status = domain.RequestStatusApproved
		request.CurrentLevel = nil
		nowCopy := now
		request.FinalizedAt = &nowCopy
		finalizedBy := p.SubmittedBy
		request.FinalizedBy = &finalizedBy
	} else {
		request.Status = domain.RequestStatusWaiting
		firstLevel := int16(1)
		request.CurrentLevel = &firstLevel
	}

	if err := s.requestRepo.InsertRequestTx(ctx, tx, request); err != nil {
		return nil, err
	}

	// Snapshot each level
	snapshotLevels := make([]domain.ApprovalRequestLevel, 0, len(levels))
	for _, l := range levels {
		roleName := ""
		// Best-effort snapshot: a since-deleted role leaves roleName empty
		// rather than failing the submit. FindByID now wraps a missing row as
		// NotFound (unwraps to pgx.ErrNoRows), so tolerate that and only
		// propagate genuine query failures.
		role, err := s.roleRepo.FindByID(ctx, l.RoleID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if role != nil {
			roleName = role.Name
		}
		lvl := domain.ApprovalRequestLevel{
			ID:                  uuid.New().String(),
			ApprovalRequestID:   request.ID,
			Level:               l.Level,
			RoleID:              l.RoleID,
			RoleName:            roleName,
			EligibleApproverIDs: l.ApproverUserIDs,
			Status:              domain.LevelStatusPending,
			CreatedAt:           now,
		}
		if err := s.requestRepo.InsertLevelTx(ctx, tx, &lvl); err != nil {
			return nil, err
		}
		snapshotLevels = append(snapshotLevels, lvl)
	}
	request.Levels = snapshotLevels

	logger.Info("Approval request submitted",
		logger.String("request_id", request.ID),
		logger.String("feature_key", p.FeatureKey),
		logger.String("reff_id", p.ReffID),
		logger.Bool("auto_approved", autoApproved),
	)

	resp := toRequestResponse(request)
	return &dto.SubmitResult{Request: &resp, AutoApproved: autoApproved}, nil
}

// ─── APPROVE / REJECT / CANCEL ──────────────────────────────────

// ApproveResult describes the outcome of Approve so callers can update their
// own denormalized state (e.g. journal_entries.status).
type ApproveResult struct {
	Finalized    bool                         // true when status flipped to 'approved'
	NewStatus    string                       // final status ('waiting' | 'approved')
	CurrentLevel *int16                       // nil when finalized
	TotalLevels  int16                        // snapshot N
	Request      *dto.ApprovalRequestResponse // refreshed response
}

// Approve opens its own transaction.
func (s *ApprovalRequestService) Approve(ctx context.Context, requestID, userID, comment string) (*ApproveResult, error) {
	tx, err := s.requestRepo.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := s.ApproveTx(ctx, tx, requestID, userID, comment)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// ApproveTx runs approval inside caller tx.
func (s *ApprovalRequestService) ApproveTx(ctx context.Context, tx pgx.Tx, requestID, userID, comment string) (*ApproveResult, error) {
	req, err := s.requestRepo.FindByID(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	if req.Status != domain.RequestStatusWaiting || req.CurrentLevel == nil {
		return nil, ErrRequestNotWaiting
	}

	levels, err := s.requestRepo.FindLevels(ctx, tx, req.ID)
	if err != nil {
		return nil, err
	}

	// Find the current-level row
	var cur *domain.ApprovalRequestLevel
	for i := range levels {
		if levels[i].Level == *req.CurrentLevel {
			cur = &levels[i]
			break
		}
	}
	if cur == nil {
		return nil, ErrRequestNotWaiting
	}

	if !containsID(cur.EligibleApproverIDs, userID) {
		return nil, ErrNotEligibleApprover
	}

	now := time.Now()
	nowStr := now.Format(time.RFC3339Nano)
	var commentPtr *string
	if comment != "" {
		commentPtr = &comment
	}

	if err := s.requestRepo.MarkLevelActionTx(ctx, tx, req.ID, *req.CurrentLevel, domain.LevelStatusApproved, userID, nowStr, commentPtr); err != nil {
		return nil, err
	}

	finalized := *req.CurrentLevel >= req.TotalLevels
	var newStatus string
	var newCurrent *int16

	if finalized {
		newStatus = domain.RequestStatusApproved
		newCurrent = nil
		finBy := userID
		if err := s.requestRepo.UpdateRequestStatusTx(ctx, tx, req.ID, newStatus, newCurrent, &nowStr, &finBy, nil); err != nil {
			return nil, err
		}
	} else {
		nextLevel := *req.CurrentLevel + 1
		newStatus = domain.RequestStatusWaiting
		newCurrent = &nextLevel
		if err := s.requestRepo.UpdateRequestStatusTx(ctx, tx, req.ID, newStatus, newCurrent, nil, nil, nil); err != nil {
			return nil, err
		}
	}

	refreshed, err := s.reloadRequestResponse(ctx, tx, req.ID)
	if err != nil {
		return nil, err
	}

	return &ApproveResult{
		Finalized:    finalized,
		NewStatus:    newStatus,
		CurrentLevel: newCurrent,
		TotalLevels:  req.TotalLevels,
		Request:      refreshed,
	}, nil
}

// RejectResult is returned by Reject/RejectTx.
type RejectResult struct {
	Request *dto.ApprovalRequestResponse
}

// Reject opens its own transaction.
func (s *ApprovalRequestService) Reject(ctx context.Context, requestID, userID, reason string) (*RejectResult, error) {
	tx, err := s.requestRepo.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := s.RejectTx(ctx, tx, requestID, userID, reason)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ApprovalRequestService) RejectTx(ctx context.Context, tx pgx.Tx, requestID, userID, reason string) (*RejectResult, error) {
	req, err := s.requestRepo.FindByID(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	if req.Status != domain.RequestStatusWaiting || req.CurrentLevel == nil {
		return nil, ErrRequestNotWaiting
	}

	levels, err := s.requestRepo.FindLevels(ctx, tx, req.ID)
	if err != nil {
		return nil, err
	}

	var cur *domain.ApprovalRequestLevel
	for i := range levels {
		if levels[i].Level == *req.CurrentLevel {
			cur = &levels[i]
			break
		}
	}
	if cur == nil {
		return nil, ErrRequestNotWaiting
	}
	if !containsID(cur.EligibleApproverIDs, userID) {
		return nil, ErrNotEligibleApprover
	}

	now := time.Now()
	nowStr := now.Format(time.RFC3339Nano)
	reasonCopy := reason

	if err := s.requestRepo.MarkLevelActionTx(ctx, tx, req.ID, *req.CurrentLevel, domain.LevelStatusRejected, userID, nowStr, &reasonCopy); err != nil {
		return nil, err
	}
	if err := s.requestRepo.SkipRemainingPendingTx(ctx, tx, req.ID); err != nil {
		return nil, err
	}
	finBy := userID
	if err := s.requestRepo.UpdateRequestStatusTx(ctx, tx, req.ID, domain.RequestStatusRejected, nil, &nowStr, &finBy, &reasonCopy); err != nil {
		return nil, err
	}

	refreshed, err := s.reloadRequestResponse(ctx, tx, req.ID)
	if err != nil {
		return nil, err
	}
	return &RejectResult{Request: refreshed}, nil
}

// Cancel lets the original submitter abort a waiting request. Opens its own tx.
func (s *ApprovalRequestService) Cancel(ctx context.Context, requestID, userID, reason string) (*dto.ApprovalRequestResponse, error) {
	tx, err := s.requestRepo.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := s.CancelTx(ctx, tx, requestID, userID, reason)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ApprovalRequestService) CancelTx(ctx context.Context, tx pgx.Tx, requestID, userID, reason string) (*dto.ApprovalRequestResponse, error) {
	req, err := s.requestRepo.FindByID(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	if req.Status != domain.RequestStatusWaiting {
		return nil, ErrRequestNotWaiting
	}
	if req.SubmittedBy != userID {
		return nil, ErrCannotCancel
	}

	now := time.Now().Format(time.RFC3339Nano)
	finBy := userID
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}

	if err := s.requestRepo.SkipRemainingPendingTx(ctx, tx, req.ID); err != nil {
		return nil, err
	}
	if err := s.requestRepo.UpdateRequestStatusTx(ctx, tx, req.ID, domain.RequestStatusCancelled, nil, &now, &finBy, reasonPtr); err != nil {
		return nil, err
	}
	return s.reloadRequestResponse(ctx, tx, req.ID)
}

// ─── READ ───────────────────────────────────────────────────────

func (s *ApprovalRequestService) GetByID(ctx context.Context, id string) (*dto.ApprovalRequestResponse, error) {
	return s.reloadRequestResponse(ctx, nil, id)
}

// GetActiveByDoc returns the waiting request (if any) for a document.
func (s *ApprovalRequestService) GetActiveByDoc(ctx context.Context, reffType, reffID string) (*dto.ApprovalRequestResponse, error) {
	req, err := s.requestRepo.FindActiveByDoc(ctx, nil, reffType, reffID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, nil
	}
	return s.reloadRequestResponse(ctx, nil, req.ID)
}

// GetLatestByDoc returns the most recent approval request for a document
// regardless of status. Intended for FE dialogs that need to show who has
// approved / who still needs to approve on a list row, even after the document
// is already posted or rejected. Returns (nil, nil) when no request exists.
func (s *ApprovalRequestService) GetLatestByDoc(ctx context.Context, companyID, reffType, reffID string) (*dto.ApprovalRequestResponse, error) {
	req, err := s.requestRepo.FindLatestByDoc(ctx, nil, companyID, reffType, reffID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, nil
	}
	return s.reloadRequestResponse(ctx, nil, req.ID)
}

func (s *ApprovalRequestService) List(ctx context.Context, companyID, userID string, params *dto.ApprovalRequestQueryParams) ([]dto.ApprovalRequestResponse, int64, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 20
	}

	reqs, total, err := s.requestRepo.FindAll(ctx, companyID, userID, params)
	if err != nil {
		return nil, 0, err
	}

	out := make([]dto.ApprovalRequestResponse, 0, len(reqs))
	for i := range reqs {
		levels, err := s.requestRepo.FindLevels(ctx, nil, reqs[i].ID)
		if err != nil {
			return nil, 0, err
		}
		reqs[i].Levels = levels
		out = append(out, toRequestResponse(&reqs[i]))
	}
	return out, total, nil
}

// ─── helpers ────────────────────────────────────────────────────

func (s *ApprovalRequestService) reloadRequestResponse(ctx context.Context, tx pgx.Tx, id string) (*dto.ApprovalRequestResponse, error) {
	req, err := s.requestRepo.FindByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	levels, err := s.requestRepo.FindLevels(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	req.Levels = levels
	resp := toRequestResponse(req)
	return &resp, nil
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func toRequestResponse(r *domain.ApprovalRequest) dto.ApprovalRequestResponse {
	levels := make([]dto.ApprovalRequestLevelResponse, 0, len(r.Levels))
	for _, l := range r.Levels {
		levels = append(levels, dto.ApprovalRequestLevelResponse{
			ID:                  l.ID,
			Level:               l.Level,
			RoleID:              l.RoleID,
			RoleName:            l.RoleName,
			EligibleApproverIDs: l.EligibleApproverIDs,
			Status:              l.Status,
			ActionBy:            l.ActionBy,
			ActionAt:            l.ActionAt,
			Comment:             l.Comment,
		})
	}
	return dto.ApprovalRequestResponse{
		ID:           r.ID,
		CompanyID:    r.CompanyID,
		BranchID:     r.BranchID,
		FeatureKey:   r.FeatureKey,
		ReffType:     r.ReffType,
		ReffID:       r.ReffID,
		Status:       r.Status,
		CurrentLevel: r.CurrentLevel,
		TotalLevels:  r.TotalLevels,
		SubmittedAt:  r.SubmittedAt,
		SubmittedBy:  r.SubmittedBy,
		FinalizedAt:  r.FinalizedAt,
		FinalizedBy:  r.FinalizedBy,
		RejectReason: r.RejectReason,
		Levels:       levels,
	}
}
