package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	branchDomain "venturo-skeleton-go/internal/modules/core/branch/domain"
	clientDomain "venturo-skeleton-go/internal/modules/core/client/domain"
	"venturo-skeleton-go/internal/modules/core/company/domain"
	"venturo-skeleton-go/internal/modules/core/company/dto"
	"venturo-skeleton-go/internal/modules/core/company/repository"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

// BranchRepository is the minimal branch dependency used by CompanyService
// to seed a default branch when a company is created.
type BranchRepository interface {
	Create(ctx context.Context, branch *branchDomain.Branch) error
}

// ClientLookup is the minimal client dependency CompanyService needs to
// resolve a caller's client_id when the caller creates a new company
// without an ambient company context (e.g. super_admin hitting POST
// /companies with no CompanyContext).
type ClientLookup interface {
	FindByOwnerUserID(ctx context.Context, ownerID string) (*clientDomain.Client, error)
}

var (
	ErrCompanyNotFound       = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Company not found")
	ErrParentNotFound        = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Parent company not found")
	ErrUserAlreadyMember     = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "User is already a member")
	ErrMembershipNotFound    = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Membership not found")
	ErrCannotRemoveOwner     = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Cannot remove company owner")
	ErrNotAuthorized         = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Not authorized")
	ErrOwnerTransferOnly     = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Only super_admin can transfer ownership")
	ErrOwnerAssignOnly       = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Only super_admin can assign a different owner")
	ErrCannotDeactivateOwner = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "Cannot deactivate company owner")
	ErrClientUnresolved      = derrors.NewErrorf(derrors.ErrorCodeUnknown, "could not resolve client for new company")
)

type CompanyService struct {
	companyRepo        *repository.CompanyRepository
	companyUserRepo    *repository.CompanyUserRepository
	branchRepo         BranchRepository
	clientLookup       ClientLookup
	defaultAdminRoleID string
}

// SetBranchRepo injects the branch repository so Create can seed a default branch.
func (s *CompanyService) SetBranchRepo(repo BranchRepository) {
	s.branchRepo = repo
}

// SetClientLookup injects the client repository so Create can resolve a
// caller's client_id via their owned client when they have no parent
// company to inherit from.
func (s *CompanyService) SetClientLookup(c ClientLookup) {
	s.clientLookup = c
}

// SetDefaultAdminRoleID injects the role assigned to the creator when they
// create a new company, so the owner has full company-scoped permissions
// out of the box (mirrors the SignUp flow).
func (s *CompanyService) SetDefaultAdminRoleID(roleID string) {
	s.defaultAdminRoleID = roleID
}

func NewCompanyService(
	companyRepo *repository.CompanyRepository,
	companyUserRepo *repository.CompanyUserRepository,
) *CompanyService {
	return &CompanyService{
		companyRepo:     companyRepo,
		companyUserRepo: companyUserRepo,
	}
}

// GetAll returns paginated list of companies.
// Super admin sees all companies. Non-super admin sees every company they are a
// member of (via core.company_users) plus each of those companies' descendants.
func (s *CompanyService) GetAll(ctx context.Context, params *dto.CompanyQueryParams, userID string, isSuperAdmin bool) (*dto.CompanyListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	var (
		companies []domain.Company
		total     int64
		err       error
	)

	if isSuperAdmin {
		companies, total, err = s.companyRepo.FindAll(ctx, params)
	} else {
		memberships, mErr := s.companyUserRepo.FindByUser(ctx, userID)
		if mErr != nil {
			return nil, mErr
		}
		companyIDs := make([]string, 0, len(memberships))
		for _, m := range memberships {
			companyIDs = append(companyIDs, m.CompanyID)
		}
		companies, total, err = s.companyRepo.FindAllScoped(ctx, params, companyIDs)
	}

	if err != nil {
		return nil, err
	}

	companyResponses := make([]dto.CompanyResponse, len(companies))
	for i, company := range companies {
		companyResponses[i] = s.toCompanyResponse(&company)
	}

	return &dto.CompanyListResponse{
		Companies: companyResponses,
		Total:     total,
		Page:      params.Page,
		Limit:     params.Limit,
	}, nil
}

// GetByID returns a company by ID
func (s *CompanyService) GetByID(ctx context.Context, id string) (*dto.CompanyResponse, error) {
	company, err := s.companyRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	response := s.toCompanyResponse(company)
	return &response, nil
}

// Create creates a new company.
// By default, creator becomes owner. Super admin can specify a different owner via req.OwnerID.
// ClientID is resolved in this order:
//  1. Parent company's client_id (if ParentID is set — subsidiaries always share client with parent)
//  2. req.ClientID (super_admin only, required when creating a standalone holding for a specific client)
//  3. Caller's primary company's client_id (for regular users adding a company under their existing client)
func (s *CompanyService) Create(ctx context.Context, req *dto.CreateCompanyRequest, createdBy string, isSuperAdmin bool) (*dto.CompanyResponse, error) {
	// Determine owner: use req.OwnerID if provided (super_admin only), otherwise creator
	ownerID := createdBy
	if req.OwnerID != nil && *req.OwnerID != createdBy {
		if !isSuperAdmin {
			return nil, ErrOwnerAssignOnly
		}
		ownerID = *req.OwnerID
	}

	// Verify parent exists if specified, and capture its client_id for
	// inheritance (step 1 in the resolution order documented above).
	var parentClientID *string
	if req.ParentID != nil {
		parent, err := s.companyRepo.FindByID(ctx, *req.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, ErrParentNotFound
		}
		parentClientID = &parent.ClientID
	}

	clientID, err := s.resolveClientID(ctx, req, createdBy, isSuperAdmin, parentClientID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	company := &domain.Company{
		ID:        uuid.New().String(),
		ClientID:  clientID,
		ParentID:  req.ParentID,
		Name:      req.Name,
		Type:      domain.CompanyType(req.Type),
		LogoURL:   req.LogoURL,
		OwnerID:   ownerID,
		IsActive:  true,
		CreatedAt: now,
		CreatedBy: &createdBy,
		UpdatedAt: now,
		UpdatedBy: &createdBy,
	}

	err = s.companyRepo.Create(ctx, company)
	if err != nil {
		return nil, err
	}

	// Add owner as company member with the default admin role so the owner
	// has full company-scoped permissions out of the box (mirrors SignUp).
	// Only set as primary if owner has no existing primary company.
	hasPrimary, _ := s.companyUserRepo.GetPrimaryCompany(ctx, ownerID)
	var defaultRoleID *string
	if s.defaultAdminRoleID != "" {
		id := s.defaultAdminRoleID
		defaultRoleID = &id
	}
	companyUser := &domain.CompanyUser{
		ID:        uuid.New().String(),
		CompanyID: company.ID,
		UserID:    ownerID,
		RoleID:    defaultRoleID,
		IsPrimary: hasPrimary == nil,
		IsActive:  true,
		InvitedBy: &createdBy,
		JoinedAt:  now,
		CreatedAt: now,
		CreatedBy: &createdBy,
		UpdatedAt: now,
		UpdatedBy: &createdBy,
	}

	err = s.companyUserRepo.Create(ctx, companyUser)
	if err != nil {
		logger.Error("Failed to add owner as company member", logger.Err(err))
	}

	// Seed default branch "Cabang Pusat" (CB-01) so downstream approval
	// flows (which require a branch_id) work out-of-the-box for the new tenant.
	if s.branchRepo != nil {
		defaultBranch := &branchDomain.Branch{
			ID:        uuid.New().String(),
			CompanyID: company.ID,
			Code:      "CB-01",
			Name:      "Cabang Pusat",
			Sort:      1,
			IsDefault: true,
			IsActive:  true,
			CreatedAt: now,
			CreatedBy: &createdBy,
			UpdatedAt: now,
			UpdatedBy: &createdBy,
		}
		if err := s.branchRepo.Create(ctx, defaultBranch); err != nil {
			logger.Error("Failed to create default branch for new company", logger.Err(err))
		}
	}

	logger.Info("Company created",
		logger.String("company_id", company.ID),
		logger.String("name", company.Name),
		logger.String("owner_id", ownerID),
		logger.String("created_by", createdBy))

	response := s.toCompanyResponse(company)
	return &response, nil
}

// resolveClientID picks the client_id for a new company. Resolution
// order (see Create's doc comment for the full rationale):
//
//  1. parentClientID (FromParent): subsidiaries always share the
//     parent's client — the caller has already looked up the parent.
//  2. req.ClientID: super_admin only; lets an admin create a standalone
//     holding under a specific client.
//  3. Caller's primary company's client_id (via company_users +
//     companies): regular users adding a second holding under their own
//     existing client.
//  4. Caller's owned client (via ClientLookup.FindByOwnerUserID): the
//     registrant creating their very first post-signup holding before
//     primary-company resolution has settled.
//
// Returns ErrClientUnresolved if nothing resolves — callers should
// surface a 400 with a hint to either switch company first or pass
// client_id explicitly (super_admin).
func (s *CompanyService) resolveClientID(
	ctx context.Context,
	req *dto.CreateCompanyRequest,
	callerUserID string,
	isSuperAdmin bool,
	parentClientID *string,
) (string, error) {
	if parentClientID != nil {
		return *parentClientID, nil
	}

	if req.ClientID != nil && *req.ClientID != "" {
		if !isSuperAdmin {
			return "", ErrNotAuthorized
		}
		return *req.ClientID, nil
	}

	// Try primary company → client.
	if primary, err := s.companyUserRepo.GetPrimaryCompany(ctx, callerUserID); err == nil && primary != nil {
		if co, err := s.companyRepo.FindByID(ctx, primary.CompanyID); err == nil && co != nil {
			return co.ClientID, nil
		}
	}

	// Fallback: client owned by caller (used by fresh registrants).
	if s.clientLookup != nil {
		if c, err := s.clientLookup.FindByOwnerUserID(ctx, callerUserID); err == nil && c != nil {
			return c.ID, nil
		}
	}

	return "", ErrClientUnresolved
}

// Update updates a company
func (s *CompanyService) Update(ctx context.Context, id string, req *dto.UpdateCompanyRequest, updatedBy string, isSuperAdmin bool) (*dto.CompanyResponse, error) {
	company, err := s.companyRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	if req.Name != nil {
		company.Name = *req.Name
	}
	if req.Type != nil {
		company.Type = domain.CompanyType(*req.Type)
	}
	if req.LogoURL != nil {
		company.LogoURL = req.LogoURL
	}
	if req.IsActive != nil {
		company.IsActive = *req.IsActive
	}
	if req.Sort != nil {
		company.Sort = *req.Sort
	}

	// Owner transfer — only super_admin
	if req.OwnerID != nil && *req.OwnerID != company.OwnerID {
		if !isSuperAdmin {
			return nil, ErrOwnerTransferOnly
		}

		newOwnerID := *req.OwnerID
		oldOwnerID := company.OwnerID
		company.OwnerID = newOwnerID

		// Ensure new owner is a member of this company
		exists, err := s.companyUserRepo.MembershipExists(ctx, id, newOwnerID)
		if err != nil {
			return nil, err
		}
		if !exists {
			now := time.Now()
			cu := &domain.CompanyUser{
				ID:        uuid.New().String(),
				CompanyID: id,
				UserID:    newOwnerID,
				IsPrimary: false,
				IsActive:  true,
				InvitedBy: &updatedBy,
				JoinedAt:  now,
				CreatedAt: now,
				CreatedBy: &updatedBy,
				UpdatedAt: now,
				UpdatedBy: &updatedBy,
			}
			if err := s.companyUserRepo.Create(ctx, cu); err != nil {
				return nil, err
			}
		}

		logger.Info("Company ownership transferred",
			logger.String("company_id", id),
			logger.String("old_owner", oldOwnerID),
			logger.String("new_owner", newOwnerID))
	}

	company.UpdatedAt = time.Now()
	company.UpdatedBy = &updatedBy

	err = s.companyRepo.Update(ctx, company)
	if err != nil {
		return nil, err
	}

	response := s.toCompanyResponse(company)
	return &response, nil
}

// Delete soft deletes a company
func (s *CompanyService) Delete(ctx context.Context, id, deletedBy string) error {
	company, err := s.companyRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if company == nil {
		return ErrCompanyNotFound
	}

	return s.companyRepo.SoftDelete(ctx, id, deletedBy)
}

// GetTrash returns paginated list of soft-deleted companies (super admin only)
func (s *CompanyService) GetTrash(ctx context.Context, params *dto.CompanyTrashQueryParams) (*dto.CompanyListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	companies, total, err := s.companyRepo.FindAllDeleted(ctx, params)
	if err != nil {
		return nil, err
	}

	companyResponses := make([]dto.CompanyResponse, len(companies))
	for i, company := range companies {
		companyResponses[i] = s.toCompanyResponse(&company)
	}

	return &dto.CompanyListResponse{
		Companies: companyResponses,
		Total:     total,
		Page:      params.Page,
		Limit:     params.Limit,
	}, nil
}

// Restore restores a soft-deleted company
func (s *CompanyService) Restore(ctx context.Context, id, restoredBy string) (*dto.CompanyResponse, error) {
	company, err := s.companyRepo.FindDeletedByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	err = s.companyRepo.Restore(ctx, id, restoredBy)
	if err != nil {
		return nil, err
	}

	// Fetch the restored company
	restored, err := s.companyRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if restored == nil {
		return nil, ErrCompanyNotFound
	}

	logger.Info("Company restored",
		logger.String("company_id", id),
		logger.String("restored_by", restoredBy))

	resp := s.toCompanyResponse(restored)
	return &resp, nil
}

// GetChildren returns direct children of a company
func (s *CompanyService) GetChildren(ctx context.Context, id string) ([]dto.CompanyResponse, error) {
	company, err := s.companyRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	children, err := s.companyRepo.GetChildren(ctx, id)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.CompanyResponse, len(children))
	for i, child := range children {
		responses[i] = s.toCompanyResponse(&child)
	}

	return responses, nil
}

// GetAncestors returns all ancestors of a company
func (s *CompanyService) GetAncestors(ctx context.Context, id string) ([]dto.CompanyResponse, error) {
	company, err := s.companyRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	ancestors, err := s.companyRepo.GetAncestors(ctx, id)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.CompanyResponse, len(ancestors))
	for i, ancestor := range ancestors {
		responses[i] = s.toCompanyResponse(&ancestor)
	}

	return responses, nil
}

// GetUsers returns paginated list of company users
func (s *CompanyService) GetUsers(ctx context.Context, companyID string, params *dto.CompanyUserQueryParams) (*dto.CompanyUserListResponse, error) {
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	company, err := s.companyRepo.FindByID(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	users, total, err := s.companyUserRepo.FindByCompany(ctx, companyID, params)
	if err != nil {
		return nil, err
	}

	userResponses := make([]dto.CompanyUserResponse, len(users))
	for i, user := range users {
		userResponses[i] = s.toCompanyUserResponse(&user)
	}

	return &dto.CompanyUserListResponse{
		Users: userResponses,
		Total: total,
		Page:  params.Page,
		Limit: params.Limit,
	}, nil
}

// AddUser adds a user to a company
func (s *CompanyService) AddUser(ctx context.Context, companyID string, req *dto.AddCompanyUserRequest, invitedBy string) (*dto.CompanyUserResponse, error) {
	company, err := s.companyRepo.FindByID(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if company == nil {
		return nil, ErrCompanyNotFound
	}

	// Check if user is already a member
	exists, err := s.companyUserRepo.MembershipExists(ctx, companyID, req.UserID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUserAlreadyMember
	}

	now := time.Now()
	cu := &domain.CompanyUser{
		ID:        uuid.New().String(),
		CompanyID: companyID,
		UserID:    req.UserID,
		RoleID:    req.RoleID,
		IsPrimary: false,
		IsActive:  true,
		InvitedBy: &invitedBy,
		JoinedAt:  now,
		CreatedAt: now,
		CreatedBy: &invitedBy,
		UpdatedAt: now,
		UpdatedBy: &invitedBy,
	}

	err = s.companyUserRepo.Create(ctx, cu)
	if err != nil {
		return nil, err
	}

	// Fetch with details
	users, _, err := s.companyUserRepo.FindByCompany(ctx, companyID, &dto.CompanyUserQueryParams{
		Page:  1,
		Limit: 1,
	})
	if err != nil || len(users) == 0 {
		return &dto.CompanyUserResponse{
			ID:        cu.ID,
			CompanyID: cu.CompanyID,
			UserID:    cu.UserID,
			RoleID:    cu.RoleID,
			IsPrimary: cu.IsPrimary,
			IsActive:  cu.IsActive,
			JoinedAt:  cu.JoinedAt,
			CreatedAt: cu.CreatedAt,
		}, nil
	}

	response := s.toCompanyUserResponse(&users[0])
	return &response, nil
}

// UpdateUser updates a user's membership in a company
func (s *CompanyService) UpdateUser(ctx context.Context, companyID, userID string, req *dto.UpdateCompanyUserRequest, updatedBy string) (*dto.CompanyUserResponse, error) {
	cu, err := s.companyUserRepo.FindByCompanyAndUser(ctx, companyID, userID)
	if err != nil {
		return nil, err
	}
	if cu == nil {
		return nil, ErrMembershipNotFound
	}

	// Prevent deactivating company owner
	if req.IsActive != nil && !*req.IsActive {
		company, err := s.companyRepo.FindByID(ctx, companyID)
		if err != nil {
			return nil, err
		}
		if company != nil && company.OwnerID == userID {
			return nil, ErrCannotDeactivateOwner
		}
	}

	if req.RoleID != nil {
		cu.RoleID = req.RoleID
	}
	if req.IsActive != nil {
		cu.IsActive = *req.IsActive
	}
	if req.IsPrimary != nil && *req.IsPrimary {
		err = s.companyUserRepo.SetPrimaryCompany(ctx, userID, companyID)
		if err != nil {
			return nil, err
		}
		cu.IsPrimary = true
	}

	cu.UpdatedAt = time.Now()
	cu.UpdatedBy = &updatedBy

	err = s.companyUserRepo.Update(ctx, cu)
	if err != nil {
		return nil, err
	}

	return &dto.CompanyUserResponse{
		ID:        cu.ID,
		CompanyID: cu.CompanyID,
		UserID:    cu.UserID,
		RoleID:    cu.RoleID,
		IsPrimary: cu.IsPrimary,
		IsActive:  cu.IsActive,
		JoinedAt:  cu.JoinedAt,
		CreatedAt: cu.CreatedAt,
	}, nil
}

// RemoveUser removes a user from a company
func (s *CompanyService) RemoveUser(ctx context.Context, companyID, userID, deletedBy string) error {
	company, err := s.companyRepo.FindByID(ctx, companyID)
	if err != nil {
		return err
	}
	if company == nil {
		return ErrCompanyNotFound
	}

	// Cannot remove owner
	if company.OwnerID == userID {
		return ErrCannotRemoveOwner
	}

	cu, err := s.companyUserRepo.FindByCompanyAndUser(ctx, companyID, userID)
	if err != nil {
		return err
	}
	if cu == nil {
		return ErrMembershipNotFound
	}

	return s.companyUserRepo.SoftDelete(ctx, cu.ID, deletedBy)
}

// SyncUserCompanies batch syncs a user's company memberships.
// Adds missing memberships and removes ones not in the list.
// Does not remove the user from companies where they are the owner.
//
// Errors from individual add/update/remove operations are now returned to the
// caller (previously they were silently logged, which masked partial failures
// and could leave a user with no memberships at all).
func (s *CompanyService) SyncUserCompanies(ctx context.Context, userID string, req *dto.SyncUserCompaniesRequest, updatedBy string) error {
	// Get current memberships
	currentMemberships, err := s.companyUserRepo.FindByUser(ctx, userID)
	if err != nil {
		return err
	}

	// Build set of desired company IDs
	desired := make(map[string]bool, len(req.CompanyIDs))
	for _, id := range req.CompanyIDs {
		desired[id] = true
	}

	// Build set of current company IDs
	current := make(map[string]string, len(currentMemberships)) // companyID -> membershipID
	for _, m := range currentMemberships {
		current[m.CompanyID] = m.ID
	}

	// Does the user already have a primary company among their active
	// memberships? If not, the first new (or surviving) company we process
	// will be promoted so SignIn can resolve company_id. This heals users
	// whose create-time assignment never got a primary (legacy bug).
	hasPrimary := false
	for _, m := range currentMemberships {
		if m.IsPrimary {
			hasPrimary = true
			break
		}
	}
	promotedPrimary := hasPrimary

	now := time.Now()

	// Add missing memberships and update role for existing ones
	for _, companyID := range req.CompanyIDs {
		if membershipID, exists := current[companyID]; exists {
			// Update role for existing membership if role_id is provided
			if req.RoleID != nil {
				cu, err := s.companyUserRepo.FindByID(ctx, membershipID)
				if err != nil {
					return err
				}
				if cu != nil {
					cu.RoleID = req.RoleID
					cu.UpdatedAt = now
					cu.UpdatedBy = &updatedBy
					if err := s.companyUserRepo.Update(ctx, cu); err != nil {
						return err
					}
				}
			}
			// Promote this surviving membership to primary if the user has
			// none yet. SetPrimaryCompany clears any other stray primary
			// flags atomically.
			if !promotedPrimary {
				if err := s.companyUserRepo.SetPrimaryCompany(ctx, userID, companyID); err != nil {
					return err
				}
				promotedPrimary = true
			}
			continue
		}
		// Verify company exists
		company, err := s.companyRepo.FindByID(ctx, companyID)
		if err != nil {
			return err
		}
		if company == nil {
			// Reject explicitly instead of silently dropping — silently
			// dropping invalid IDs hides bugs in the caller and can lead to
			// users being created with zero memberships.
			return fmt.Errorf("%w: %s", ErrCompanyNotFound, companyID)
		}

		makePrimary := !promotedPrimary
		cu := &domain.CompanyUser{
			ID:        uuid.New().String(),
			CompanyID: companyID,
			UserID:    userID,
			RoleID:    req.RoleID,
			IsPrimary: makePrimary,
			IsActive:  true,
			InvitedBy: &updatedBy,
			JoinedAt:  now,
			CreatedAt: now,
			CreatedBy: &updatedBy,
			UpdatedAt: now,
			UpdatedBy: &updatedBy,
		}
		if err := s.companyUserRepo.Create(ctx, cu); err != nil {
			return err
		}
		if makePrimary {
			promotedPrimary = true
		}
	}

	// Remove memberships not in desired list (skip owner companies)
	for _, m := range currentMemberships {
		if desired[m.CompanyID] {
			continue
		}
		// Check if user is owner of this company — don't remove
		company, err := s.companyRepo.FindByID(ctx, m.CompanyID)
		if err != nil {
			return err
		}
		if company != nil && company.OwnerID == userID {
			continue
		}
		if err := s.companyUserRepo.SoftDelete(ctx, m.ID, updatedBy); err != nil {
			return err
		}
	}

	return nil
}

// AssignUserToCompaniesTx attaches a freshly-created user to one or more
// companies inside the caller's transaction. Unlike SyncUserCompanies, this
// is fail-fast and only inserts (it never removes), so it is safe to use as
// part of an atomic "create user" flow. Each company_id is verified to exist
// before insert; an unknown ID returns ErrCompanyNotFound and rolls back the
// caller's transaction.
//
// Primary promotion: when the user has no existing primary company (typical
// case for a brand-new user created by admin), the FIRST company in the list
// is marked is_primary=true. Without this, SignIn cannot resolve a
// company_id and the user's JWT gets `company_id: ""`, which trips the
// CompanyContext middleware on every scoped endpoint.
func (s *CompanyService) AssignUserToCompaniesTx(ctx context.Context, tx pgx.Tx, userID string, req *dto.SyncUserCompaniesRequest, createdBy string) error {
	if len(req.CompanyIDs) == 0 {
		return nil
	}

	// Does the user already have a primary company? Read outside the tx is
	// fine — membership is (userID, companyID)-unique so there's no race
	// with the concurrent inserts we're about to make for *this* user.
	existingPrimary, err := s.companyUserRepo.GetPrimaryCompany(ctx, userID)
	if err != nil {
		return err
	}
	needsPrimary := existingPrimary == nil

	now := time.Now()
	for i, companyID := range req.CompanyIDs {
		// Verify company exists. The read uses the pool (not the tx) on
		// purpose: companies is a slow-changing parent table, so a read
		// outside the tx is fine and avoids unnecessary lock contention.
		company, err := s.companyRepo.FindByID(ctx, companyID)
		if err != nil {
			return err
		}
		if company == nil {
			return fmt.Errorf("%w: %s", ErrCompanyNotFound, companyID)
		}

		cu := &domain.CompanyUser{
			ID:        uuid.New().String(),
			CompanyID: companyID,
			UserID:    userID,
			RoleID:    req.RoleID,
			IsPrimary: needsPrimary && i == 0,
			IsActive:  true,
			InvitedBy: &createdBy,
			JoinedAt:  now,
			CreatedAt: now,
			CreatedBy: &createdBy,
			UpdatedAt: now,
			UpdatedBy: &createdBy,
		}
		if err := s.companyUserRepo.CreateTx(ctx, tx, cu); err != nil {
			return err
		}
	}
	return nil
}

// GetUserCompanyIDs returns the list of company IDs a user is a member of, with ownership flag
func (s *CompanyService) GetUserCompanyIDs(ctx context.Context, userID string) ([]dto.UserCompanyIDResponse, error) {
	memberships, err := s.companyUserRepo.FindByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Collect company IDs to batch-check ownership
	companyIDs := make([]string, len(memberships))
	for i, m := range memberships {
		companyIDs[i] = m.CompanyID
	}

	// Build ownership set
	ownerSet := make(map[string]bool)
	for _, companyID := range companyIDs {
		company, err := s.companyRepo.FindByID(ctx, companyID)
		if err != nil {
			continue
		}
		if company != nil && company.OwnerID == userID {
			ownerSet[companyID] = true
		}
	}

	result := make([]dto.UserCompanyIDResponse, len(memberships))
	for i, m := range memberships {
		result[i] = dto.UserCompanyIDResponse{
			CompanyID: m.CompanyID,
			IsOwner:   ownerSet[m.CompanyID],
		}
	}
	return result, nil
}

// GetUserCompaniesWithDetails returns companies a user belongs to with name, owner, and role info
func (s *CompanyService) GetUserCompaniesWithDetails(ctx context.Context, userID string) ([]domain.UserCompanyDetail, error) {
	return s.companyUserRepo.FindByUserWithDetails(ctx, userID)
}

// IsCompanyAdmin checks if user is an admin/owner of a company or any of its ancestors.
// Used for scoped authorization.
func (s *CompanyService) IsCompanyAdmin(ctx context.Context, userID, companyID string) (bool, error) {
	// Check if user is owner of this company
	company, err := s.companyRepo.FindByID(ctx, companyID)
	if err != nil {
		return false, err
	}
	if company == nil {
		return false, nil
	}
	if company.OwnerID == userID {
		return true, nil
	}

	// Check if user is owner of any ancestor
	ancestors, err := s.companyRepo.GetAncestors(ctx, companyID)
	if err != nil {
		return false, err
	}
	for _, a := range ancestors {
		if a.OwnerID == userID {
			return true, nil
		}
	}

	// Check if user is a member with admin-level permission on this company
	isMember, err := s.companyUserRepo.IsMember(ctx, userID, companyID)
	if err != nil {
		return false, err
	}

	return isMember, nil
}

// toCompanyResponse converts domain.Company to dto.CompanyResponse
func (s *CompanyService) toCompanyResponse(company *domain.Company) dto.CompanyResponse {
	return dto.CompanyResponse{
		ID:        company.ID,
		ClientID:  company.ClientID,
		ParentID:  company.ParentID,
		Name:      company.Name,
		Type:      string(company.Type),
		LogoURL:   company.LogoURL,
		OwnerID:   company.OwnerID,
		OwnerName: company.OwnerName,
		Sort:      company.Sort,
		IsActive:  company.IsActive,
		CreatedBy: company.CreatedBy,
		CreatedAt: company.CreatedAt,
		UpdatedAt: company.UpdatedAt,
	}
}

// toCompanyUserResponse converts domain.CompanyUserWithDetails to dto.CompanyUserResponse
func (s *CompanyService) toCompanyUserResponse(cu *domain.CompanyUserWithDetails) dto.CompanyUserResponse {
	return dto.CompanyUserResponse{
		ID:           cu.ID,
		CompanyID:    cu.CompanyID,
		UserID:       cu.UserID,
		RoleID:       cu.RoleID,
		RoleName:     cu.RoleName,
		RoleCode:     cu.RoleCode,
		UserEmail:    cu.UserEmail,
		UserUsername: cu.UserUsername,
		UserFullName: cu.UserFullName,
		IsPrimary:    cu.IsPrimary,
		IsActive:     cu.IsActive,
		JoinedAt:     cu.JoinedAt,
		CreatedAt:    cu.CreatedAt,
	}
}
