package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"venturo-skeleton-go/internal/modules/core/branch/domain"
	"venturo-skeleton-go/internal/modules/core/branch/repository"
	"venturo-skeleton-go/pkg/derrors"
)

var (
	// ErrInvalidBranchIDs is returned when one or more submitted branch IDs do
	// not exist (or were soft-deleted).
	ErrInvalidBranchIDs = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "invalid branch_ids")
	// ErrBranchNotInScope is returned when a submitted branch belongs to a
	// company the caller is not allowed to assign under (non-super-admin).
	ErrBranchNotInScope = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "branch not in caller's company scope")
)

// CompanyScopeResolver resolves which company IDs a tenant scope is allowed
// to operate on (scope company + descendants). Implemented by the company
// service; kept as a narrow interface here to avoid a cyclic import.
type CompanyScopeResolver interface {
	VisibleCompanyIDs(ctx context.Context, rootCompanyID string) ([]string, error)
}

// UserBranchService owns the sync/assignment logic for core.user_branches.
// It is separate from BranchService so the branch CRUD surface stays focused.
type UserBranchService struct {
	repo          *repository.UserBranchRepository
	scopeResolver CompanyScopeResolver
}

func NewUserBranchService(repo *repository.UserBranchRepository) *UserBranchService {
	return &UserBranchService{repo: repo}
}

// SetScopeResolver injects the company scope resolver. When set, non-nil
// scopeCompanyID calls will enforce that every branch belongs to that scope
// company or one of its descendants.
func (s *UserBranchService) SetScopeResolver(r CompanyScopeResolver) {
	s.scopeResolver = r
}

// AssignUserToBranchesTx inserts user_branches rows inside the caller's
// transaction. Insert-only; safe for the user create flow.
//
// Every branch_id is verified to exist and (when scopeCompanyID is non-nil)
// to belong to the caller's company scope. An unknown or out-of-scope id
// aborts the transaction.
func (s *UserBranchService) AssignUserToBranchesTx(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	branchIDs []string,
	createdBy string,
	scopeCompanyID *string,
) error {
	if len(branchIDs) == 0 {
		return nil
	}
	if err := s.validateBranches(ctx, branchIDs, scopeCompanyID); err != nil {
		return err
	}

	now := time.Now()
	for _, branchID := range dedupe(branchIDs) {
		ub := &domain.UserBranch{
			ID:        uuid.New().String(),
			UserID:    userID,
			BranchID:  branchID,
			CreatedAt: now,
			CreatedBy: &createdBy,
			UpdatedAt: now,
			UpdatedBy: &createdBy,
		}
		if err := s.repo.CreateTx(ctx, tx, ub); err != nil {
			return err
		}
	}
	return nil
}

// SyncUserBranches reconciles a user's branch access to exactly branchIDs.
// Adds missing entries, soft-deletes ones not in the desired list.
//
// When scopeCompanyID is non-nil, all *incoming* branch_ids must belong to
// that scope, AND existing entries that fall outside the scope are NOT
// removed (so a company admin cannot strip access a super admin granted in
// another tenant).
func (s *UserBranchService) SyncUserBranches(
	ctx context.Context,
	userID string,
	branchIDs []string,
	updatedBy string,
	scopeCompanyID *string,
) error {
	desired := dedupe(branchIDs)
	if err := s.validateBranches(ctx, desired, scopeCompanyID); err != nil {
		return err
	}

	current, err := s.repo.FindByUser(ctx, userID)
	if err != nil {
		return err
	}

	// Resolve current branch -> company so we can skip removals that are
	// outside the caller's scope.
	currentIDs := make([]string, len(current))
	for i, ub := range current {
		currentIDs[i] = ub.BranchID
	}
	currentCompany := map[string]string{}
	if len(currentIDs) > 0 {
		currentCompany, err = s.repo.FindBranchesByIDsWithCompany(ctx, currentIDs)
		if err != nil {
			return err
		}
	}

	// Build visible-set for scope-based filtering (non-super-admin).
	var visibleSet map[string]struct{}
	if scopeCompanyID != nil && s.scopeResolver != nil {
		ids, err := s.scopeResolver.VisibleCompanyIDs(ctx, *scopeCompanyID)
		if err != nil {
			return err
		}
		visibleSet = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			visibleSet[id] = struct{}{}
		}
	}

	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}
	currentSet := make(map[string]string, len(current)) // branch_id -> membership id
	for _, ub := range current {
		currentSet[ub.BranchID] = ub.ID
	}

	now := time.Now()

	// Add missing
	for _, branchID := range desired {
		if _, exists := currentSet[branchID]; exists {
			continue
		}
		ub := &domain.UserBranch{
			ID:        uuid.New().String(),
			UserID:    userID,
			BranchID:  branchID,
			CreatedAt: now,
			CreatedBy: &updatedBy,
			UpdatedAt: now,
			UpdatedBy: &updatedBy,
		}
		if err := s.repo.Create(ctx, ub); err != nil {
			return err
		}
	}

	// Remove extras (respecting scope)
	for _, ub := range current {
		if _, keep := desiredSet[ub.BranchID]; keep {
			continue
		}
		if visibleSet != nil {
			companyID, ok := currentCompany[ub.BranchID]
			if !ok {
				// Branch row is gone (rare); keep the membership untouched.
				continue
			}
			if _, inScope := visibleSet[companyID]; !inScope {
				continue
			}
		}
		if err := s.repo.SoftDelete(ctx, ub.ID, updatedBy); err != nil {
			return err
		}
	}

	return nil
}

// GetUserBranchIDs returns the active branch IDs a user is assigned to (no
// details). Used by the /users/:id/branches GET endpoint.
func (s *UserBranchService) GetUserBranchIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.repo.FindByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, ub := range rows {
		out[i] = ub.BranchID
	}
	return out, nil
}

// GetUserBranchesWithDetails returns branch details for a user — used to
// enrich UserResponse.
func (s *UserBranchService) GetUserBranchesWithDetails(ctx context.Context, userID string) ([]domain.UserBranchDetail, error) {
	return s.repo.FindByUserWithDetails(ctx, userID)
}

// validateBranches checks that all submitted ids exist and (optionally) lie
// inside the caller's company scope.
func (s *UserBranchService) validateBranches(ctx context.Context, branchIDs []string, scopeCompanyID *string) error {
	if len(branchIDs) == 0 {
		return nil
	}
	resolved, err := s.repo.FindBranchesByIDsWithCompany(ctx, branchIDs)
	if err != nil {
		return err
	}
	for _, id := range branchIDs {
		if _, ok := resolved[id]; !ok {
			return fmt.Errorf("%w: %s", ErrInvalidBranchIDs, id)
		}
	}
	if scopeCompanyID == nil || s.scopeResolver == nil {
		return nil
	}
	visible, err := s.scopeResolver.VisibleCompanyIDs(ctx, *scopeCompanyID)
	if err != nil {
		return err
	}
	visibleSet := make(map[string]struct{}, len(visible))
	for _, id := range visible {
		visibleSet[id] = struct{}{}
	}
	for _, id := range branchIDs {
		companyID := resolved[id]
		if _, ok := visibleSet[companyID]; !ok {
			return fmt.Errorf("%w: %s", ErrBranchNotInScope, id)
		}
	}
	return nil
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
