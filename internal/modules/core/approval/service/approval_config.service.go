package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"venturo-skeleton-go/internal/modules/core/approval/domain"
	"venturo-skeleton-go/internal/modules/core/approval/dto"
	"venturo-skeleton-go/internal/modules/core/approval/repository"
	"venturo-skeleton-go/pkg/derrors"
)

var (
	ErrConfigNotFound     = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Approval config not found")
	ErrUnknownFeatureKey  = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "unknown feature_key")
	ErrDuplicateLevel     = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "duplicate level numbers in config")
	ErrLevelNotSequential = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "levels must be sequential starting from 1")
)

type ApprovalConfigService struct {
	repo *repository.ApprovalConfigRepository
}

func NewApprovalConfigService(repo *repository.ApprovalConfigRepository) *ApprovalConfigService {
	return &ApprovalConfigService{repo: repo}
}

// ─── READ ───────────────────────────────────────────────────────

func (s *ApprovalConfigService) GetAll(ctx context.Context, companyID string, params *dto.ApprovalConfigQueryParams) ([]dto.ApprovalConfigResponse, error) {
	configs, err := s.repo.FindAll(ctx, companyID, params)
	if err != nil {
		return nil, err
	}

	out := make([]dto.ApprovalConfigResponse, 0, len(configs))
	for i := range configs {
		levels, err := s.repo.FindLevels(ctx, nil, configs[i].ID)
		if err != nil {
			return nil, err
		}
		configs[i].Levels = levels
		out = append(out, toConfigResponse(&configs[i]))
	}
	return out, nil
}

func (s *ApprovalConfigService) GetByID(ctx context.Context, id, companyID string) (*dto.ApprovalConfigResponse, error) {
	c, err := s.repo.FindByID(ctx, id, companyID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrConfigNotFound
	}
	levels, err := s.repo.FindLevels(ctx, nil, c.ID)
	if err != nil {
		return nil, err
	}
	c.Levels = levels
	resp := toConfigResponse(c)
	return &resp, nil
}

// ─── WRITE ──────────────────────────────────────────────────────

func (s *ApprovalConfigService) Create(ctx context.Context, companyID string, req *dto.CreateApprovalConfigRequest, createdBy string) (*dto.ApprovalConfigResponse, error) {
	if !domain.IsKnownFeatureKey(req.FeatureKey) {
		return nil, ErrUnknownFeatureKey
	}
	if err := validateLevelInputs(req.Levels); err != nil {
		return nil, err
	}

	now := time.Now()
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	config := &domain.ApprovalConfig{
		ID:         uuid.New().String(),
		CompanyID:  companyID,
		BranchID:   req.BranchID,
		FeatureKey: req.FeatureKey,
		IsActive:   isActive,
		CreatedAt:  now,
		CreatedBy:  &createdBy,
		UpdatedAt:  now,
		UpdatedBy:  &createdBy,
	}

	for _, in := range req.Levels {
		config.Levels = append(config.Levels, domain.ApprovalConfigLevel{
			ID:              uuid.New().String(),
			Level:           in.Level,
			RoleID:          in.RoleID,
			ApproverUserIDs: in.ApproverUserIDs,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	if err := s.repo.Create(ctx, config); err != nil {
		return nil, err
	}

	resp := toConfigResponse(config)
	return &resp, nil
}

func (s *ApprovalConfigService) Update(ctx context.Context, id, companyID string, req *dto.UpdateApprovalConfigRequest, updatedBy string) (*dto.ApprovalConfigResponse, error) {
	existing, err := s.repo.FindByID(ctx, id, companyID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrConfigNotFound
	}

	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}

	replaceLevels := false
	if req.Levels != nil {
		if err := validateLevelInputs(*req.Levels); err != nil {
			return nil, err
		}
		replaceLevels = true
		existing.Levels = nil
		now := time.Now()
		for _, in := range *req.Levels {
			existing.Levels = append(existing.Levels, domain.ApprovalConfigLevel{
				ID:              uuid.New().String(),
				Level:           in.Level,
				RoleID:          in.RoleID,
				ApproverUserIDs: in.ApproverUserIDs,
				CreatedAt:       now,
				UpdatedAt:       now,
			})
		}
	}

	existing.UpdatedAt = time.Now()
	existing.UpdatedBy = &updatedBy

	if err := s.repo.Update(ctx, existing, replaceLevels); err != nil {
		return nil, err
	}

	// Reload levels after update
	levels, err := s.repo.FindLevels(ctx, nil, existing.ID)
	if err != nil {
		return nil, err
	}
	existing.Levels = levels

	resp := toConfigResponse(existing)
	return &resp, nil
}

func (s *ApprovalConfigService) Delete(ctx context.Context, id, companyID, deletedBy string) error {
	existing, err := s.repo.FindByID(ctx, id, companyID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrConfigNotFound
	}
	return s.repo.SoftDelete(ctx, id, companyID, deletedBy)
}

// ─── helpers ────────────────────────────────────────────────────

func validateLevelInputs(levels []dto.ApprovalConfigLevelInput) error {
	seen := make(map[int16]bool, len(levels))
	for _, l := range levels {
		if seen[l.Level] {
			return ErrDuplicateLevel
		}
		seen[l.Level] = true
	}
	// Enforce levels start from 1 and are contiguous (1..N).
	for i := int16(1); i <= int16(len(levels)); i++ {
		if !seen[i] {
			return ErrLevelNotSequential
		}
	}
	return nil
}

func toConfigResponse(c *domain.ApprovalConfig) dto.ApprovalConfigResponse {
	levels := make([]dto.ApprovalConfigLevelResponse, 0, len(c.Levels))
	for _, l := range c.Levels {
		levels = append(levels, dto.ApprovalConfigLevelResponse{
			ID:              l.ID,
			Level:           l.Level,
			RoleID:          l.RoleID,
			ApproverUserIDs: l.ApproverUserIDs,
		})
	}
	return dto.ApprovalConfigResponse{
		ID:         c.ID,
		CompanyID:  c.CompanyID,
		BranchID:   c.BranchID,
		FeatureKey: c.FeatureKey,
		IsActive:   c.IsActive,
		Levels:     levels,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}
