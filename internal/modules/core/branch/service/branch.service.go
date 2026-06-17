package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"venturo-skeleton-go/internal/modules/core/branch/domain"
	"venturo-skeleton-go/internal/modules/core/branch/dto"
	"venturo-skeleton-go/internal/modules/core/branch/repository"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

const (
	defaultBranchCodePrefix = "CB"
	maxCodeGenRetries       = 3
)

var (
	ErrBranchNotFound      = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Branch not found")
	ErrCannotDeleteDefault = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Cannot delete default branch")
	ErrBranchCodeTaken     = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Branch code already exists")
)

// CompanyMembershipLookup returns the set of company IDs a user is an active
// member of. Used by GetAllByCompanies to scope multi-tenant listing to the
// caller's memberships without pulling the company_user rows in full.
type CompanyMembershipLookup interface {
	FindCompanyIDsByUser(ctx context.Context, userID string) ([]string, error)
}

type BranchService struct {
	branchRepo   *repository.BranchRepository
	memberLookup CompanyMembershipLookup
}

func NewBranchService(branchRepo *repository.BranchRepository) *BranchService {
	return &BranchService{branchRepo: branchRepo}
}

// SetCompanyMembershipLookup wires the company-user repo (or any compatible
// implementation) into the service. Required for GetAllByCompanies to resolve
// non-super_admin callers' allowed companies.
func (s *BranchService) SetCompanyMembershipLookup(lookup CompanyMembershipLookup) {
	s.memberLookup = lookup
}

// GetAll returns paginated list of branches for a company
func (s *BranchService) GetAll(ctx context.Context, companyID string, params *dto.BranchQueryParams) (*dto.BranchListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	branches, total, err := s.branchRepo.FindAll(ctx, companyID, params)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.BranchResponse, len(branches))
	for i, b := range branches {
		responses[i] = toBranchResponse(&b)
	}

	return &dto.BranchListResponse{
		Branches: responses,
		Total:    total,
		Page:     params.Page,
		Limit:    params.Limit,
	}, nil
}

// GetAllByCompanies returns paginated branches across every company the caller
// has access to. Behaviour:
//   - super_admin + no filter          → every company, every branch
//   - super_admin + company_ids filter → only the requested companies
//   - non-super_admin + no filter      → all companies the user is a member of
//   - non-super_admin + company_ids    → intersection of filter & memberships
//
// Branches are additionally narrowed by the user_branches allowlist when the
// BranchScope middleware populates params.ScopeBranchIDs.
func (s *BranchService) GetAllByCompanies(ctx context.Context, userID string, isSuperAdmin bool, params *dto.BranchQueryParams) (*dto.BranchListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	// Resolve companyIDs with these semantics for the repo:
	//   nil       → no company filter (super_admin unrestricted)
	//   non-empty → restrict to these companies
	// An empty (but non-nil) slice short-circuits to an empty response below.
	var companyIDs []string
	if isSuperAdmin {
		if len(params.CompanyIDs) > 0 {
			companyIDs = params.CompanyIDs
		}
		// else companyIDs stays nil → unrestricted
	} else {
		if s.memberLookup == nil {
			return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company membership lookup is not configured")
		}
		memberIDs, err := s.memberLookup.FindCompanyIDsByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if len(params.CompanyIDs) > 0 {
			companyIDs = intersectStrings(params.CompanyIDs, memberIDs)
		} else {
			companyIDs = memberIDs
		}
		if len(companyIDs) == 0 {
			return &dto.BranchListResponse{
				Branches: []dto.BranchResponse{},
				Total:    0,
				Page:     params.Page,
				Limit:    params.Limit,
			}, nil
		}
	}

	branches, total, err := s.branchRepo.FindAllByCompanies(ctx, companyIDs, params)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.BranchResponse, len(branches))
	for i, b := range branches {
		responses[i] = toBranchResponse(&b)
	}

	return &dto.BranchListResponse{
		Branches: responses,
		Total:    total,
		Page:     params.Page,
		Limit:    params.Limit,
	}, nil
}

// intersectStrings returns the set intersection of a and b, preserving the
// order of a. Duplicate entries in a are de-duplicated.
func intersectStrings(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	out := make([]string, 0, len(a))
	seen := make(map[string]struct{}, len(a))
	for _, v := range a {
		if _, ok := set[v]; !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// GetByID returns a branch by ID
func (s *BranchService) GetByID(ctx context.Context, id string) (*dto.BranchResponse, error) {
	branch, err := s.branchRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if branch == nil {
		return nil, ErrBranchNotFound
	}

	resp := toBranchResponse(branch)
	return &resp, nil
}

// Create creates a new branch
func (s *BranchService) Create(ctx context.Context, companyID string, req *dto.CreateBranchRequest, createdBy string) (*dto.BranchResponse, error) {
	now := time.Now()

	isDefault := false
	if req.IsDefault != nil {
		isDefault = *req.IsDefault
	}

	// If setting as default, clear existing default first
	if isDefault {
		if err := s.branchRepo.ClearDefault(ctx, companyID); err != nil {
			return nil, err
		}
	}

	sort := 0
	if req.Sort != nil {
		sort = *req.Sort
	}

	userProvidedCode := req.Code != nil && strings.TrimSpace(*req.Code) != ""

	branch := &domain.Branch{
		ID:        uuid.New().String(),
		CompanyID: companyID,
		Name:      req.Name,
		LogoURL:   req.LogoURL,
		Sort:      sort,
		IsDefault: isDefault,
		IsActive:  true,
		CreatedAt: now,
		CreatedBy: &createdBy,
		UpdatedAt: now,
		UpdatedBy: &createdBy,
	}

	if userProvidedCode {
		branch.Code = strings.TrimSpace(*req.Code)
		if err := s.branchRepo.Create(ctx, branch); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrBranchCodeTaken
			}
			return nil, err
		}
	} else {
		// Auto-generate with retry in case the window between
		// code generation and INSERT races with a concurrent create.
		var lastErr error
		for attempt := 0; attempt < maxCodeGenRetries; attempt++ {
			code, err := s.branchRepo.GenerateNextCode(ctx, companyID, defaultBranchCodePrefix)
			if err != nil {
				return nil, err
			}
			branch.Code = code
			if err := s.branchRepo.Create(ctx, branch); err != nil {
				if isUniqueViolation(err) {
					lastErr = err
					continue
				}
				return nil, err
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return nil, lastErr
		}
	}

	logger.Info("Branch created",
		logger.String("branch_id", branch.ID),
		logger.String("company_id", companyID),
		logger.String("code", branch.Code),
		logger.String("name", branch.Name))

	resp := toBranchResponse(branch)
	return &resp, nil
}

// isUniqueViolation returns true if the error is a Postgres unique_violation (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Update updates a branch
func (s *BranchService) Update(ctx context.Context, id string, req *dto.UpdateBranchRequest, updatedBy string) (*dto.BranchResponse, error) {
	branch, err := s.branchRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if branch == nil {
		return nil, ErrBranchNotFound
	}

	if req.Code != nil {
		trimmed := strings.TrimSpace(*req.Code)
		if trimmed == "" {
			return nil, derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "Code cannot be empty")
		}
		branch.Code = trimmed
	}
	if req.Name != nil {
		branch.Name = *req.Name
	}
	if req.LogoURL != nil {
		branch.LogoURL = req.LogoURL
	}
	if req.Sort != nil {
		branch.Sort = *req.Sort
	}
	if req.IsActive != nil {
		branch.IsActive = *req.IsActive
	}
	if req.IsDefault != nil && *req.IsDefault && !branch.IsDefault {
		// Setting as default — clear existing default first
		if err := s.branchRepo.ClearDefault(ctx, branch.CompanyID); err != nil {
			return nil, err
		}
		branch.IsDefault = true
	}

	branch.UpdatedAt = time.Now()
	branch.UpdatedBy = &updatedBy

	if err := s.branchRepo.Update(ctx, branch); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrBranchCodeTaken
		}
		return nil, err
	}

	resp := toBranchResponse(branch)
	return &resp, nil
}

// Delete soft deletes a branch
func (s *BranchService) Delete(ctx context.Context, id, deletedBy string) error {
	branch, err := s.branchRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if branch == nil {
		return ErrBranchNotFound
	}
	if branch.IsDefault {
		return ErrCannotDeleteDefault
	}

	return s.branchRepo.SoftDelete(ctx, id, deletedBy)
}

func toBranchResponse(b *domain.Branch) dto.BranchResponse {
	return dto.BranchResponse{
		ID:        b.ID,
		CompanyID: b.CompanyID,
		Code:      b.Code,
		Name:      b.Name,
		LogoURL:   b.LogoURL,
		Sort:      b.Sort,
		IsDefault: b.IsDefault,
		IsActive:  b.IsActive,
		CreatedBy: b.CreatedBy,
		CreatedAt: b.CreatedAt,
		UpdatedAt: b.UpdatedAt,
	}
}
