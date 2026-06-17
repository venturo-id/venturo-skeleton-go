package service

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"venturo-skeleton-go/internal/config"
	"venturo-skeleton-go/internal/modules/core/auth/domain"
	"venturo-skeleton-go/internal/modules/core/auth/dto"
	"venturo-skeleton-go/internal/modules/core/auth/repository"
	branchDomain "venturo-skeleton-go/internal/modules/core/branch/domain"
	clientDomain "venturo-skeleton-go/internal/modules/core/client/domain"
	companyDomain "venturo-skeleton-go/internal/modules/core/company/domain"
	userDomain "venturo-skeleton-go/internal/modules/core/user/domain"
	userRepo "venturo-skeleton-go/internal/modules/core/user/repository"
	"venturo-skeleton-go/pkg/crypto"
	"venturo-skeleton-go/pkg/derrors"
	pkgfirebase "venturo-skeleton-go/pkg/firebase"
	"venturo-skeleton-go/pkg/jwt"
	"venturo-skeleton-go/pkg/logger"
	"venturo-skeleton-go/pkg/token"
)

var (
	ErrInvalidCredentials    = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "invalid credentials")
	ErrUserNotActive         = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "user is not active")
	ErrUserLocked            = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "user account is locked")
	ErrEmailNotVerified      = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "email not verified")
	ErrEmailAlreadyExists    = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Email already exists")
	ErrUsernameAlreadyExists = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Username already exists")
	ErrInvalidRefreshToken   = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "invalid refresh token")
	ErrRefreshTokenExpired   = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "refresh token expired")
	ErrRefreshTokenRevoked   = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "refresh token revoked")
	ErrCompanyNotFound       = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Company not found")
	ErrNotCompanyMember      = derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, "User is not a member of this company")

	// Google sign-in errors
	ErrFirebaseNotConfigured = derrors.NewErrorf(derrors.ErrorCodeServiceUnavailable, "google sign-in not configured")
	ErrInvalidGoogleToken    = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "invalid google id token")
	ErrGoogleEmailMissing    = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "google account did not return an email")
	ErrUnexpectedProvider    = derrors.NewErrorf(derrors.ErrorCodeUnauthorized, "unexpected firebase sign-in provider")
)

// firebaseSignInProviderGoogle is the value Firebase reports in the
// ID token's `firebase.sign_in_provider` claim for a Google sign-in.
const firebaseSignInProviderGoogle = "google.com"

const (
	MaxFailedLoginAttempts = 5
	LockDuration           = 15 * time.Minute
	RefreshTokenExpiry     = 7 * 24 * time.Hour // 7 days
	AccessTokenExpiryHours = 24
)

// CompanyUserRepository interface for company user operations
type CompanyUserRepository interface {
	IsMember(ctx context.Context, userID, companyID string) (bool, error)
	GetPrimaryCompany(ctx context.Context, userID string) (*companyDomain.CompanyUser, error)
	FindByUser(ctx context.Context, userID string) ([]companyDomain.CompanyUser, error)
	FindByUserWithDetails(ctx context.Context, userID string) ([]companyDomain.UserCompanyDetail, error)
	Create(ctx context.Context, cu *companyDomain.CompanyUser) error
}

// CompanyRepository interface for company operations
type CompanyRepository interface {
	FindByID(ctx context.Context, id string) (*companyDomain.Company, error)
	Create(ctx context.Context, company *companyDomain.Company) error
}

// RoleRepository interface for role operations
type RoleRepository interface {
	GetUserPermissions(ctx context.Context, userID string, companyID *string) ([]string, error)
	GetUserRoleNames(ctx context.Context, userID string, companyID *string) ([]string, error)
}

// BranchRepository interface for branch operations
type BranchRepository interface {
	Create(ctx context.Context, branch *branchDomain.Branch) error
}

// ClientService is the slice of the client service that auth needs:
//   - CreateForUser: SignUp provisions a new client for a registrant.
//   - GetByID:       SignIn / SwitchCompany resolve client_id + slug
//     from the caller's company so both show up in the
//     JWT and the response payload.
type ClientService interface {
	CreateForUser(ctx context.Context, userID, username, displayName string) (*clientDomain.Client, error)
	GetByID(ctx context.Context, id string) (*clientDomain.Client, error)
}

// PermissionReader is the narrow seam GetMe needs from the authz
// package. Implemented by *authz.Service — accepted as an interface so
// auth doesn't import authz directly (and so tests can fake it).
type PermissionReader interface {
	GetPermissions(ctx context.Context, userID, companyID string) ([]string, error)
}

// FirebaseVerifier is the seam the auth service uses to verify Firebase
// ID tokens. Concrete impl: *pkg/firebase.Client. Kept as an interface
// so tests can fake it without spinning up Firebase, and so the
// production wiring stays optional — the auth service still boots when
// Firebase is not configured; only the Google sign-in endpoint refuses
// to work.
type FirebaseVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*pkgfirebase.VerifiedToken, error)
}

type AuthService struct {
	userRepo         *userRepo.UserRepository
	userIdentityRepo *userRepo.UserIdentityRepository
	refreshTokenRepo *repository.RefreshTokenRepository
	companyUserRepo  CompanyUserRepository
	companyRepo      CompanyRepository
	roleRepo         RoleRepository
	branchRepo       BranchRepository
	clientService    ClientService
	permissionReader PermissionReader
	firebase         FirebaseVerifier
	config           *config.Config
}

func NewAuthService(
	userRepo *userRepo.UserRepository,
	refreshTokenRepo *repository.RefreshTokenRepository,
	cfg *config.Config,
) *AuthService {
	return &AuthService{
		userRepo:         userRepo,
		refreshTokenRepo: refreshTokenRepo,
		config:           cfg,
	}
}

// SetUserIdentityRepo wires the user_identities repository used by the
// Google sign-in flow.
func (s *AuthService) SetUserIdentityRepo(repo *userRepo.UserIdentityRepository) {
	s.userIdentityRepo = repo
}

// SetFirebaseVerifier wires the Firebase ID-token verifier. Pass nil
// (or never call this) to disable Google sign-in — the endpoint will
// then return ErrFirebaseNotConfigured.
func (s *AuthService) SetFirebaseVerifier(f FirebaseVerifier) {
	s.firebase = f
}

// SetCompanyUserRepo sets the company user repository (for dependency injection after initialization)
func (s *AuthService) SetCompanyUserRepo(repo CompanyUserRepository) {
	s.companyUserRepo = repo
}

// SetCompanyRepo sets the company repository
func (s *AuthService) SetCompanyRepo(repo CompanyRepository) {
	s.companyRepo = repo
}

// SetRoleRepo sets the role repository
func (s *AuthService) SetRoleRepo(repo RoleRepository) {
	s.roleRepo = repo
}

// SetBranchRepo sets the branch repository
func (s *AuthService) SetBranchRepo(repo BranchRepository) {
	s.branchRepo = repo
}

// SetPermissionReader wires the authz-backed permission cache used by
// GetMe. Without this, GetMe falls back to the role repo directly —
// correct, but bypasses Redis.
func (s *AuthService) SetPermissionReader(r PermissionReader) {
	s.permissionReader = r
}

// resolveClientInfo looks up a client by id and returns its
// (id, slug, *ClientInfo). On any failure (client service missing,
// lookup error, or unknown id) it returns empty strings and nil — the
// caller silently degrades so a missing client never blocks login.
// Used by SignIn / SwitchCompany to enrich the JWT and response.
func (s *AuthService) resolveClientInfo(ctx context.Context, clientID string) (string, string, *dto.ClientInfo) {
	if s.clientService == nil || clientID == "" {
		return "", "", nil
	}
	c, err := s.clientService.GetByID(ctx, clientID)
	if err != nil || c == nil {
		return "", "", nil
	}
	return c.ID, c.Slug, &dto.ClientInfo{
		ID:   c.ID,
		Slug: c.Slug,
		Name: c.Name,
	}
}

// SetClientService wires the client service so SignUp can provision a
// new client row (one-per-registrant) before the first company is created.
func (s *AuthService) SetClientService(svc ClientService) {
	s.clientService = svc
}

// SignUp registers a new user
func (s *AuthService) SignUp(ctx context.Context, req *dto.SignUpRequest) (*dto.SignUpResponse, error) {
	// Check email uniqueness
	exists, err := s.userRepo.EmailExists(ctx, req.Email, nil)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailAlreadyExists
	}

	// Check username uniqueness
	exists, err = s.userRepo.UsernameExists(ctx, req.Username, nil)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUsernameAlreadyExists
	}

	// Hash password
	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		logger.Error("Failed to hash password", logger.Err(err))
		return nil, err
	}

	now := time.Now()
	user := &userDomain.User{
		ID:              uuid.New().String(),
		Email:           req.Email,
		Username:        req.Username,
		PasswordHash:    passwordHash,
		FullName:        req.FullName,
		Phone:           req.Phone,
		IsActive:        true,
		IsEmailVerified: true,
		EmailVerifiedAt: &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	err = s.userRepo.Create(ctx, user)
	if err != nil {
		return nil, err
	}

	// Provision a client (registration-level tenant) for this user before
	// creating any companies. One signup = one client; companies created
	// later (via POST /core/v1/companies) will inherit this client_id.
	if s.clientService == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "client service not set")
	}
	var newClient *clientDomain.Client
	var displayName string
	if user.FullName != nil {
		displayName = *user.FullName
	}
	newClient, err = s.clientService.CreateForUser(ctx, user.ID, user.Username, displayName)
	if err != nil {
		logger.Error("Failed to create client during signup", logger.Err(err))
		return nil, err
	}

	// Create company scoped to the new client.
	companyID := uuid.New().String()
	newCompany := &companyDomain.Company{
		ID:        companyID,
		ClientID:  newClient.ID,
		Name:      req.CompanyName,
		Type:      companyDomain.CompanyTypeHolding,
		OwnerID:   user.ID,
		Sort:      1,
		IsActive:  true,
		CreatedAt: now,
		CreatedBy: &user.ID,
		UpdatedAt: now,
		UpdatedBy: &user.ID,
	}

	if s.companyRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company repository not set")
	}
	err = s.companyRepo.Create(ctx, newCompany)
	if err != nil {
		logger.Error("Failed to create company during signup", logger.Err(err))
		return nil, err
	}

	// Link user to company as primary member with the default admin role
	// (so the owner has full company-scoped permissions out of the box).
	if s.companyUserRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company user repository not set")
	}
	var defaultRoleID *string
	if s.config != nil && s.config.Auth.DefaultAdminRoleID != "" {
		id := s.config.Auth.DefaultAdminRoleID
		defaultRoleID = &id
	}
	companyUser := &companyDomain.CompanyUser{
		ID:        uuid.New().String(),
		CompanyID: companyID,
		UserID:    user.ID,
		RoleID:    defaultRoleID,
		IsPrimary: true,
		IsActive:  true,
		JoinedAt:  now,
		CreatedAt: now,
		CreatedBy: &user.ID,
		UpdatedAt: now,
		UpdatedBy: &user.ID,
	}
	err = s.companyUserRepo.Create(ctx, companyUser)
	if err != nil {
		logger.Error("Failed to link user to company during signup", logger.Err(err))
		return nil, err
	}

	// Create default branch "Cabang Pusat"
	if s.branchRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "branch repository not set")
	}
	defaultBranch := &branchDomain.Branch{
		ID:        uuid.New().String(),
		CompanyID: companyID,
		Name:      "Cabang Pusat",
		Sort:      1,
		IsDefault: true,
		IsActive:  true,
		CreatedAt: now,
		CreatedBy: &user.ID,
		UpdatedAt: now,
		UpdatedBy: &user.ID,
	}
	err = s.branchRepo.Create(ctx, defaultBranch)
	if err != nil {
		logger.Error("Failed to create default branch during signup", logger.Err(err))
		return nil, err
	}

	logger.Info("User signed up with company",
		logger.String("user_id", user.ID),
		logger.String("email", user.Email),
		logger.String("company_id", companyID),
		logger.String("company_name", req.CompanyName))

	return &dto.SignUpResponse{
		Message: "User registered successfully.",
		User: dto.UserInfo{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			FullName: user.FullName,
		},
		Company: dto.CompanyInfo{
			ID:   companyID,
			Name: req.CompanyName,
		},
	}, nil
}

// SignIn authenticates a user and returns tokens
func (s *AuthService) SignIn(ctx context.Context, req *dto.SignInRequest, deviceInfo domain.DeviceInfo, ipAddress string) (*dto.SignInResponse, error) {
	// Find user by email or username
	user, err := s.userRepo.FindByEmailOrUsername(ctx, req.Login)
	if err != nil {
		return nil, err
	}
	if user == nil {
		logger.Warn("User not found", logger.String("login", req.Login))
		return nil, ErrInvalidCredentials
	}

	// Check if user is active
	if !user.IsActive {
		logger.Warn("Inactive user login attempt", logger.String("user_id", user.ID))
		return nil, ErrUserNotActive
	}

	// Check if user is locked
	if user.IsLocked() {
		logger.Warn("Locked user login attempt", logger.String("user_id", user.ID))
		return nil, ErrUserLocked
	}

	// Check email verification if required
	if s.config != nil && s.config.Security.EmailVerificationRequired && !user.IsEmailVerified {
		logger.Warn("Unverified email login attempt", logger.String("user_id", user.ID))
		return nil, ErrEmailNotVerified
	}

	// Verify password
	if !crypto.ComparePassword(user.PasswordHash, req.Password) {
		// Increment failed login count
		_ = s.userRepo.IncrementFailedLogin(ctx, user.ID)

		// Lock user if too many failed attempts
		if user.FailedLoginCount+1 >= MaxFailedLoginAttempts {
			lockUntil := time.Now().Add(LockDuration)
			_ = s.userRepo.LockUser(ctx, user.ID, lockUntil)
			logger.Warn("User locked due to failed login attempts", logger.String("user_id", user.ID))
		}

		logger.Warn("Invalid password", logger.String("user_id", user.ID))
		return nil, ErrInvalidCredentials
	}

	// Update last login
	_ = s.userRepo.UpdateLastLogin(ctx, user.ID)

	// Try to load primary company for auto-scoped permissions
	var companyID, companyName string
	var companyInfo *dto.CompanyInfo
	var clientID, clientSlug string
	var clientInfo *dto.ClientInfo

	if s.companyUserRepo != nil {
		primaryMembership, _ := s.companyUserRepo.GetPrimaryCompany(ctx, user.ID)
		if primaryMembership != nil {
			companyID = primaryMembership.CompanyID
			if s.companyRepo != nil {
				company, _ := s.companyRepo.FindByID(ctx, companyID)
				if company != nil {
					companyName = company.Name
					companyInfo = &dto.CompanyInfo{
						ID:   company.ID,
						Name: company.Name,
					}
					clientID, clientSlug, clientInfo = s.resolveClientInfo(ctx, company.ClientID)
				}
			}
		}
	}

	// Get user roles and permissions (scoped to primary company if available)
	var roles, permissions []string
	if s.roleRepo != nil {
		if companyID != "" {
			roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID, &companyID)
			permissions, _ = s.roleRepo.GetUserPermissions(ctx, user.ID, &companyID)
		} else {
			roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID, nil)
			permissions, _ = s.roleRepo.GetUserPermissions(ctx, user.ID, nil)
		}
	}

	// Generate access token with company context
	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(user.ID, companyID, companyName, clientID, clientSlug, user.Email, user.Username, fullName, isSuperAdmin, roles)
	if err != nil {
		logger.Error("Failed to generate access token", logger.Err(err))
		return nil, err
	}

	// Generate refresh token
	refreshTokenStr, err := token.GenerateSecureToken(32)
	if err != nil {
		logger.Error("Failed to generate refresh token", logger.Err(err))
		return nil, err
	}

	// Store refresh token hash in database
	refreshToken := &domain.RefreshToken{
		ID:         uuid.New().String(),
		UserID:     user.ID,
		TokenHash:  hashToken(refreshTokenStr),
		DeviceInfo: deviceInfo,
		IPAddress:  &ipAddress,
		ExpiresAt:  time.Now().Add(RefreshTokenExpiry),
		CreatedAt:  time.Now(),
	}

	err = s.refreshTokenRepo.Create(ctx, refreshToken)
	if err != nil {
		logger.Error("Failed to store refresh token", logger.Err(err))
		return nil, err
	}

	logger.Info("User signed in",
		logger.String("user_id", user.ID),
		logger.String("email", user.Email),
		logger.String("company_id", companyID))

	return &dto.SignInResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		TokenType:    "Bearer",
		ExpiresIn:    AccessTokenExpiryHours * 3600,
		User: dto.UserInfo{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			FullName: user.FullName,
		},
		Company:     companyInfo,
		Client:      clientInfo,
		Roles:       roles,
		Permissions: permissions,
	}, nil
}

// RefreshToken refreshes the access token using a refresh token
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (*dto.RefreshTokenResponse, error) {
	tokenHash := hashToken(refreshTokenStr)

	// Find refresh token
	refreshToken, err := s.refreshTokenRepo.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if refreshToken == nil {
		return nil, ErrInvalidRefreshToken
	}

	// Check if token is revoked
	if refreshToken.IsRevoked() {
		return nil, ErrRefreshTokenRevoked
	}

	// Check if token is expired
	if refreshToken.IsExpired() {
		return nil, ErrRefreshTokenExpired
	}

	// Get user
	user, err := s.userRepo.FindByID(ctx, refreshToken.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive {
		return nil, ErrUserNotActive
	}

	// Update last used
	_ = s.refreshTokenRepo.UpdateLastUsed(ctx, refreshToken.ID)

	// Get user roles. Permissions are not needed here — the refresh
	// response returns only the new token pair, and permissions are
	// looked up from Redis on subsequent requests via authz.
	var roles []string
	if s.roleRepo != nil {
		roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID, nil)
	}

	// Generate new access token
	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(user.ID, "", "", "", "", user.Email, user.Username, fullName, isSuperAdmin, roles)
	if err != nil {
		logger.Error("Failed to generate access token", logger.Err(err))
		return nil, err
	}

	// Generate new refresh token (token rotation)
	newRefreshTokenStr, err := token.GenerateSecureToken(32)
	if err != nil {
		logger.Error("Failed to generate new refresh token", logger.Err(err))
		return nil, err
	}

	// Revoke old refresh token
	_ = s.refreshTokenRepo.Revoke(ctx, refreshToken.ID)

	// Store new refresh token
	newRefreshToken := &domain.RefreshToken{
		ID:         uuid.New().String(),
		UserID:     user.ID,
		TokenHash:  hashToken(newRefreshTokenStr),
		DeviceInfo: refreshToken.DeviceInfo,
		IPAddress:  refreshToken.IPAddress,
		ExpiresAt:  time.Now().Add(RefreshTokenExpiry),
		CreatedAt:  time.Now(),
	}

	err = s.refreshTokenRepo.Create(ctx, newRefreshToken)
	if err != nil {
		logger.Error("Failed to store new refresh token", logger.Err(err))
		return nil, err
	}

	return &dto.RefreshTokenResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshTokenStr,
		TokenType:    "Bearer",
		ExpiresIn:    AccessTokenExpiryHours * 3600,
	}, nil
}

// Logout revokes a refresh token
func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	tokenHash := hashToken(refreshTokenStr)

	err := s.refreshTokenRepo.RevokeByTokenHash(ctx, tokenHash)
	if err != nil {
		logger.Error("Failed to revoke refresh token", logger.Err(err))
		return err
	}

	return nil
}

// LogoutAll revokes all refresh tokens for a user
func (s *AuthService) LogoutAll(ctx context.Context, userID string) error {
	err := s.refreshTokenRepo.RevokeAllByUserID(ctx, userID)
	if err != nil {
		logger.Error("Failed to revoke all refresh tokens", logger.Err(err))
		return err
	}

	return nil
}

// SwitchCompany switches the user's active company context
func (s *AuthService) SwitchCompany(ctx context.Context, userID, companyID string) (*dto.SwitchCompanyResponse, error) {
	// Check if user is a member of the company
	if s.companyUserRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company user repository not set")
	}

	isMember, err := s.companyUserRepo.IsMember(ctx, userID, companyID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, ErrNotCompanyMember
	}

	// Get user
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotActive
	}

	// Fetch company details
	var companyName string
	var clientID, clientSlug string
	var clientInfo *dto.ClientInfo
	if s.companyRepo != nil {
		company, _ := s.companyRepo.FindByID(ctx, companyID)
		if company != nil {
			companyName = company.Name
			clientID, clientSlug, clientInfo = s.resolveClientInfo(ctx, company.ClientID)
		}
	}
	_ = clientInfo // switch-company response doesn't carry client info today; claim covers the FE

	// Get user roles for the company. Permissions live in Redis and are
	// fetched on the request path; we still load them here to return in
	// the response body so the FE can update its UI without decoding
	// the (now permission-less) JWT.
	var roles, permissions []string
	if s.roleRepo != nil {
		roles, _ = s.roleRepo.GetUserRoleNames(ctx, userID, &companyID)
		permissions, _ = s.roleRepo.GetUserPermissions(ctx, userID, &companyID)
	}

	// Generate new access token with company context
	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(user.ID, companyID, companyName, clientID, clientSlug, user.Email, user.Username, fullName, isSuperAdmin, roles)
	if err != nil {
		logger.Error("Failed to generate access token", logger.Err(err))
		return nil, err
	}

	// Generate new refresh token
	refreshTokenStr, err := token.GenerateSecureToken(32)
	if err != nil {
		logger.Error("Failed to generate refresh token", logger.Err(err))
		return nil, err
	}

	// Store refresh token
	refreshToken := &domain.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		TokenHash: hashToken(refreshTokenStr),
		ExpiresAt: time.Now().Add(RefreshTokenExpiry),
		CreatedAt: time.Now(),
	}

	err = s.refreshTokenRepo.Create(ctx, refreshToken)
	if err != nil {
		logger.Error("Failed to store refresh token", logger.Err(err))
		return nil, err
	}

	return &dto.SwitchCompanyResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		TokenType:    "Bearer",
		ExpiresIn:    AccessTokenExpiryHours * 3600,
		Company: dto.CompanyInfo{
			ID:   companyID,
			Name: companyName,
		},
		Roles:       roles,
		Permissions: permissions,
	}, nil
}

// GetMe returns the signed-in user's current profile, active company,
// client context, roles, and *effective* permissions. Permissions come
// from the authz cache (Redis) when wired; otherwise the role repo is
// consulted directly. Intended for FE rehydration after a page reload
// or re-sync after an unexpected 403.
func (s *AuthService) GetMe(ctx context.Context, userID, companyID string, isSuperAdmin bool) (*dto.MeResponse, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotActive
	}

	resp := &dto.MeResponse{
		User: dto.UserInfo{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			FullName: user.FullName,
		},
		IsSuperAdmin: isSuperAdmin,
	}

	// Resolve company + client context when the caller has switched.
	var companyScope *string
	if companyID != "" && s.companyRepo != nil {
		company, _ := s.companyRepo.FindByID(ctx, companyID)
		if company != nil {
			resp.Company = &dto.CompanyInfo{
				ID:   company.ID,
				Name: company.Name,
			}
			_, _, resp.Client = s.resolveClientInfo(ctx, company.ClientID)
			cid := company.ID
			companyScope = &cid
		}
	}

	// Roles — always from the role repo. The list is small so there's
	// no cache layer in front of it.
	if s.roleRepo != nil {
		resp.Roles, _ = s.roleRepo.GetUserRoleNames(ctx, userID, companyScope)
	}

	// Permissions — prefer the Redis-backed authz cache. Fall back to
	// the role repo if the reader isn't wired (tests / bootstrapping).
	switch {
	case s.permissionReader != nil:
		scope := ""
		if companyScope != nil {
			scope = *companyScope
		}
		resp.Permissions, err = s.permissionReader.GetPermissions(ctx, userID, scope)
		if err != nil {
			logger.Warn("GetMe: permission reader failed, falling back to role repo", logger.Err(err))
			if s.roleRepo != nil {
				resp.Permissions, _ = s.roleRepo.GetUserPermissions(ctx, userID, companyScope)
			}
		}
	case s.roleRepo != nil:
		resp.Permissions, _ = s.roleRepo.GetUserPermissions(ctx, userID, companyScope)
	}

	if resp.Permissions == nil {
		resp.Permissions = []string{}
	}
	if resp.Roles == nil {
		resp.Roles = []string{}
	}

	return resp, nil
}

// GetMyCompanies returns the list of companies the user has access to
func (s *AuthService) GetMyCompanies(ctx context.Context, userID string) ([]dto.MyCompanyResponse, error) {
	if s.companyUserRepo == nil {
		return []dto.MyCompanyResponse{}, nil
	}

	details, err := s.companyUserRepo.FindByUserWithDetails(ctx, userID)
	if err != nil {
		return nil, err
	}

	companies := make([]dto.MyCompanyResponse, 0, len(details))
	for _, d := range details {
		companies = append(companies, dto.MyCompanyResponse{
			ID:        d.CompanyID,
			Name:      d.CompanyName,
			Type:      d.CompanyType,
			LogoURL:   d.LogoURL,
			ParentID:  d.ParentID,
			IsPrimary: d.IsPrimary,
			IsOwner:   d.OwnerID == userID,
			RoleName:  d.RoleName,
			RoleCode:  d.RoleCode,
		})
	}

	return companies, nil
}

// containsRole checks if a role exists in the roles slice
func containsRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}

// ─── Google sign-in ─────────────────────────────────────────────────
//
// SignInWithGoogle is the single entry point for both sign-in and
// sign-up via a Firebase Google ID token. Flow:
//
//  1. Verify the ID token via Firebase Admin.
//  2. Look up core.user_identities by (google, firebase_uid).
//     - Found → refresh the identity snapshot, sign that user in.
//  3. Not found → look up core.users by email.
//     - Found → link the Google identity to the existing account,
//     then sign that user in. The pre-existing user keeps their
//     company/role/branch setup — no auto-provisioning here.
//  4. Not found at all → first-time Google user:
//     a) create a new core.users row (no password)
//     b) provision a client, company (named after the Google
//     profile), default branch — same flow as SignUp
//     c) link the Google identity
//     d) sign the new user in
//
// All four branches return a *dto.GoogleSignInResponse with
// `is_new_user` distinguishing case (4) from the rest. Token issuance
// (JWT + refresh) is shared with the email/password path.
func (s *AuthService) SignInWithGoogle(
	ctx context.Context,
	req *dto.GoogleSignInRequest,
	deviceInfo domain.DeviceInfo,
	ipAddress string,
) (*dto.GoogleSignInResponse, error) {
	if s.firebase == nil {
		return nil, ErrFirebaseNotConfigured
	}
	if s.userIdentityRepo == nil {
		// Mis-wired bootstrap — refuse to run a half-configured flow.
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "user identity repository not set")
	}

	// 1. Verify token. anything that returns here has a valid
	// signature, audience, and expiry per Firebase Admin SDK.
	tok, err := s.firebase.VerifyIDToken(ctx, req.IDToken)
	if err != nil {
		logger.Warn("Firebase ID token verification failed", logger.Err(err))
		return nil, ErrInvalidGoogleToken
	}

	// Defence in depth: only accept tokens that actually came from a
	// Google sign-in (not, say, a Firebase email-link flow that happens
	// to be enabled in the same project).
	if tok.SignInProvider != "" && tok.SignInProvider != firebaseSignInProviderGoogle {
		logger.Warn("Rejected Firebase token with unexpected sign-in provider",
			logger.String("sign_in_provider", tok.SignInProvider))
		return nil, ErrUnexpectedProvider
	}

	if tok.Email == "" {
		return nil, ErrGoogleEmailMissing
	}
	email := strings.ToLower(strings.TrimSpace(tok.Email))

	// 2. Identity already linked → returning user.
	identity, err := s.userIdentityRepo.FindByProvider(ctx, userDomain.IdentityProviderGoogle, tok.UID)
	if err != nil {
		return nil, err
	}

	var (
		user      *userDomain.User
		isNewUser bool
	)

	switch {
	case identity != nil:
		// Returning Google user.
		user, err = s.userRepo.FindByID(ctx, identity.UserID)
		if err != nil {
			return nil, err
		}
		if user == nil || !user.IsActive || user.DeletedAt != nil {
			return nil, ErrUserNotActive
		}
		// Refresh the snapshot so audit/debug fields stay current.
		_ = s.userIdentityRepo.UpdateOnLogin(ctx, identity.ID, &email, marshalProfile(tok))

	default:
		// 3. No identity yet — try linking to an existing user by email.
		existing, err := s.userRepo.FindByEmail(ctx, email)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if !existing.IsActive || existing.DeletedAt != nil {
				return nil, ErrUserNotActive
			}
			user = existing
			if err := s.linkGoogleIdentity(ctx, user.ID, tok, email); err != nil {
				return nil, err
			}
		} else {
			// 4. Brand-new user — full provisioning.
			user, err = s.provisionGoogleUser(ctx, tok, email)
			if err != nil {
				return nil, err
			}
			isNewUser = true
		}
	}

	// Update last_login_at on the user row (mirrors SignIn).
	_ = s.userRepo.UpdateLastLogin(ctx, user.ID)

	// From here on the response build mirrors SignIn: load primary
	// company, roles, permissions; issue JWT + refresh token.
	return s.issueGoogleTokens(ctx, user, isNewUser, deviceInfo, ipAddress)
}

// provisionGoogleUser creates the user + client + company + default
// branch atomically-ish (sequential inserts; matches the existing
// SignUp flow which is also non-transactional today). The company name
// is forced to the Google profile name as requested by the product:
// no onboarding form needed.
func (s *AuthService) provisionGoogleUser(
	ctx context.Context,
	tok *pkgfirebase.VerifiedToken,
	email string,
) (*userDomain.User, error) {
	if s.clientService == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "client service not set")
	}
	if s.companyRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company repository not set")
	}
	if s.companyUserRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "company user repository not set")
	}
	if s.branchRepo == nil {
		return nil, derrors.NewErrorf(derrors.ErrorCodeUnknown, "branch repository not set")
	}

	now := time.Now()

	// Username: derived from email local-part. Collisions resolved by
	// appending a 4-hex suffix (mirrors client.allocateSlug's spirit).
	username, err := s.allocateUsernameFromEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	// Names: prefer the provider's `name` claim; if it's empty (rare
	// for Google) fall back to the email local-part.
	displayName := strings.TrimSpace(tok.Name)
	if displayName == "" {
		displayName = usernameFromEmail(email)
	}

	var avatarURL *string
	if tok.Picture != "" {
		pic := tok.Picture
		avatarURL = &pic
	}

	full := displayName
	user := &userDomain.User{
		ID:        uuid.New().String(),
		Email:     email,
		Username:  username,
		FullName:  &full,
		AvatarURL: avatarURL,
		IsActive:  true,
		// Google verifies emails itself before issuing an ID token —
		// trust the claim and mark the local row verified so the user
		// can use the app immediately even if EMAIL_VERIFICATION_REQUIRED
		// is on.
		IsEmailVerified: tok.EmailVerified,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if tok.EmailVerified {
		v := now
		user.EmailVerifiedAt = &v
	}

	if err := s.userRepo.CreateExternal(ctx, user); err != nil {
		return nil, err
	}

	// Client (registration-level tenant) — one per registrant.
	newClient, err := s.clientService.CreateForUser(ctx, user.ID, user.Username, displayName)
	if err != nil {
		logger.Error("Failed to create client during google signup", logger.Err(err))
		return nil, err
	}

	// Company — name follows the Google display name verbatim, as
	// requested by product. Truncate to the column's 255-char limit
	// defensively (Google profile names are usually short).
	companyName := truncateName(displayName, 255)
	companyID := uuid.New().String()
	newCompany := &companyDomain.Company{
		ID:        companyID,
		ClientID:  newClient.ID,
		Name:      companyName,
		Type:      companyDomain.CompanyTypeHolding,
		OwnerID:   user.ID,
		Sort:      1,
		IsActive:  true,
		CreatedAt: now,
		CreatedBy: &user.ID,
		UpdatedAt: now,
		UpdatedBy: &user.ID,
	}
	if err := s.companyRepo.Create(ctx, newCompany); err != nil {
		logger.Error("Failed to create company during google signup", logger.Err(err))
		return nil, err
	}

	// Link user → company as primary admin.
	var defaultRoleID *string
	if s.config != nil && s.config.Auth.DefaultAdminRoleID != "" {
		id := s.config.Auth.DefaultAdminRoleID
		defaultRoleID = &id
	}
	companyUser := &companyDomain.CompanyUser{
		ID:        uuid.New().String(),
		CompanyID: companyID,
		UserID:    user.ID,
		RoleID:    defaultRoleID,
		IsPrimary: true,
		IsActive:  true,
		JoinedAt:  now,
		CreatedAt: now,
		CreatedBy: &user.ID,
		UpdatedAt: now,
		UpdatedBy: &user.ID,
	}
	if err := s.companyUserRepo.Create(ctx, companyUser); err != nil {
		logger.Error("Failed to link user to company during google signup", logger.Err(err))
		return nil, err
	}

	// Default branch — same "Cabang Pusat" as email signup.
	defaultBranch := &branchDomain.Branch{
		ID:        uuid.New().String(),
		CompanyID: companyID,
		Name:      "Cabang Pusat",
		Sort:      1,
		IsDefault: true,
		IsActive:  true,
		CreatedAt: now,
		CreatedBy: &user.ID,
		UpdatedAt: now,
		UpdatedBy: &user.ID,
	}
	if err := s.branchRepo.Create(ctx, defaultBranch); err != nil {
		logger.Error("Failed to create default branch during google signup", logger.Err(err))
		return nil, err
	}

	// Link the Google identity last so a failure in earlier provisioning
	// doesn't leave an orphan identity row.
	if err := s.linkGoogleIdentity(ctx, user.ID, tok, email); err != nil {
		return nil, err
	}

	logger.Info("User provisioned via Google sign-in",
		logger.String("user_id", user.ID),
		logger.String("email", user.Email),
		logger.String("company_id", companyID),
		logger.String("company_name", companyName))

	return user, nil
}

// linkGoogleIdentity inserts the (google, firebase_uid) row for a user
// that already exists in core.users. Idempotent: callers should not
// reach here twice for the same user, but the DB UNIQUE will catch any
// race that slips through.
func (s *AuthService) linkGoogleIdentity(
	ctx context.Context,
	userID string,
	tok *pkgfirebase.VerifiedToken,
	email string,
) error {
	now := time.Now()
	identity := &userDomain.UserIdentity{
		ID:             uuid.New().String(),
		UserID:         userID,
		Provider:       userDomain.IdentityProviderGoogle,
		ProviderUserID: tok.UID,
		Email:          &email,
		RawProfile:     marshalProfile(tok),
		LastLoginAt:    &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.userIdentityRepo.Create(ctx, identity); err != nil {
		logger.Error("Failed to link google identity", logger.Err(err))
		return err
	}
	return nil
}

// issueGoogleTokens packages the response: loads primary company,
// roles, permissions; mints access + refresh tokens; persists refresh.
// Logic mirrors the tail of SignIn — kept separate so the Google flow
// can attach `is_new_user`.
func (s *AuthService) issueGoogleTokens(
	ctx context.Context,
	user *userDomain.User,
	isNewUser bool,
	deviceInfo domain.DeviceInfo,
	ipAddress string,
) (*dto.GoogleSignInResponse, error) {
	var (
		companyID, companyName string
		companyInfo            *dto.CompanyInfo
		clientID, clientSlug   string
		clientInfo             *dto.ClientInfo
	)

	if s.companyUserRepo != nil {
		primaryMembership, _ := s.companyUserRepo.GetPrimaryCompany(ctx, user.ID)
		if primaryMembership != nil {
			companyID = primaryMembership.CompanyID
			if s.companyRepo != nil {
				company, _ := s.companyRepo.FindByID(ctx, companyID)
				if company != nil {
					companyName = company.Name
					companyInfo = &dto.CompanyInfo{ID: company.ID, Name: company.Name}
					clientID, clientSlug, clientInfo = s.resolveClientInfo(ctx, company.ClientID)
				}
			}
		}
	}

	var roles, permissions []string
	if s.roleRepo != nil {
		if companyID != "" {
			roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID, &companyID)
			permissions, _ = s.roleRepo.GetUserPermissions(ctx, user.ID, &companyID)
		} else {
			roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID, nil)
			permissions, _ = s.roleRepo.GetUserPermissions(ctx, user.ID, nil)
		}
	}

	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(
		user.ID, companyID, companyName, clientID, clientSlug,
		user.Email, user.Username, fullName, isSuperAdmin, roles,
	)
	if err != nil {
		logger.Error("Failed to generate access token", logger.Err(err))
		return nil, err
	}

	refreshTokenStr, err := token.GenerateSecureToken(32)
	if err != nil {
		logger.Error("Failed to generate refresh token", logger.Err(err))
		return nil, err
	}

	refreshToken := &domain.RefreshToken{
		ID:         uuid.New().String(),
		UserID:     user.ID,
		TokenHash:  hashToken(refreshTokenStr),
		DeviceInfo: deviceInfo,
		IPAddress:  &ipAddress,
		ExpiresAt:  time.Now().Add(RefreshTokenExpiry),
		CreatedAt:  time.Now(),
	}
	if err := s.refreshTokenRepo.Create(ctx, refreshToken); err != nil {
		logger.Error("Failed to store refresh token", logger.Err(err))
		return nil, err
	}

	if permissions == nil {
		permissions = []string{}
	}
	if roles == nil {
		roles = []string{}
	}

	return &dto.GoogleSignInResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		TokenType:    "Bearer",
		ExpiresIn:    AccessTokenExpiryHours * 3600,
		IsNewUser:    isNewUser,
		User: dto.UserInfo{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			FullName: user.FullName,
		},
		Company:     companyInfo,
		Client:      clientInfo,
		Roles:       roles,
		Permissions: permissions,
	}, nil
}

// allocateUsernameFromEmail derives a unique username from the email
// local-part. Adds a 4-hex suffix on collision; gives up after a few
// tries (would only happen under sustained collision pressure, which
// is essentially impossible with random suffixes).
func (s *AuthService) allocateUsernameFromEmail(ctx context.Context, email string) (string, error) {
	base := sanitizeUsername(usernameFromEmail(email))
	if base == "" {
		base = "user-" + shortHex(4)
	}

	candidate := base
	for attempt := 0; attempt < 5; attempt++ {
		exists, err := s.userRepo.UsernameExists(ctx, candidate, nil)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		suffix := "-" + shortHex(4)
		maxBase := 100 - len(suffix) // core.users.username is VARCHAR(100)
		if len(base) > maxBase {
			base = base[:maxBase]
		}
		candidate = base + suffix
	}
	return "", derrors.NewErrorf(derrors.ErrorCodeUnknown, "could not allocate unique username after retries")
}

// usernameFromEmail returns the part before '@', lowercased and
// trimmed. Caller still feeds the result through sanitizeUsername.
func usernameFromEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[:at]))
}

// sanitizeUsername coerces a free-form string to the charset the
// app tends to accept for usernames: lowercase alnum + dot + hyphen
// + underscore. Anything else is dropped. Returns "" if the result
// is shorter than 3 chars.
func sanitizeUsername(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	b := make([]byte, 0, len(lower))
	for i := 0; i < len(lower); i++ {
		ch := lower[i]
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= '0' && ch <= '9',
			ch == '.', ch == '-', ch == '_':
			b = append(b, ch)
		}
	}
	out := string(b)
	if len(out) < 3 {
		return ""
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// shortHex returns n bytes of hex from crypto/rand. Used only for
// suffixes on collision, so the small allocation is fine.
func shortHex(n int) string {
	buf := make([]byte, n)
	if _, err := cryptoRand(buf); err != nil {
		// crypto/rand failures are essentially impossible on a healthy
		// host; fall back to a timestamp-derived value so we still
		// produce *something* rather than panic during sign-up.
		return hex.EncodeToString([]byte(time.Now().Format("0405")))[:n*2]
	}
	return hex.EncodeToString(buf)
}

// cryptoRand is wrapped behind a var so a test can swap it out.
var cryptoRand = func(b []byte) (int, error) {
	return cryptorand.Read(b)
}

// truncateName clamps a string to maxRunes UTF-8 runes (not bytes), so
// multibyte characters in a Google display name don't get split mid-codepoint.
func truncateName(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

// marshalProfile serializes a verified token's claims for storage in
// user_identities.raw_profile. Falls back to "{}" on marshalling error
// so a non-fatal serialization issue doesn't kill the sign-in.
func marshalProfile(tok *pkgfirebase.VerifiedToken) []byte {
	b, err := json.Marshal(tok.Claims)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// hashToken creates a SHA-256 hash of the token
func hashToken(tokenStr string) string {
	h := sha256.New()
	h.Write([]byte(tokenStr))
	return hex.EncodeToString(h.Sum(nil))
}

// GetDeviceInfoFromUserAgent extracts device info from user agent string
func GetDeviceInfoFromUserAgent(userAgent string) domain.DeviceInfo {
	info := domain.DeviceInfo{}

	ua := strings.ToLower(userAgent)

	// Detect browser
	switch {
	case strings.Contains(ua, "chrome"):
		info.Browser = "Chrome"
	case strings.Contains(ua, "firefox"):
		info.Browser = "Firefox"
	case strings.Contains(ua, "safari"):
		info.Browser = "Safari"
	case strings.Contains(ua, "edge"):
		info.Browser = "Edge"
	default:
		info.Browser = "Unknown"
	}

	// Detect OS
	switch {
	case strings.Contains(ua, "windows"):
		info.OS = "Windows"
	case strings.Contains(ua, "mac"):
		info.OS = "macOS"
	case strings.Contains(ua, "linux"):
		info.OS = "Linux"
	case strings.Contains(ua, "android"):
		info.OS = "Android"
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"):
		info.OS = "iOS"
	default:
		info.OS = "Unknown"
	}

	// Detect type
	switch {
	case strings.Contains(ua, "mobile"), strings.Contains(ua, "android"), strings.Contains(ua, "iphone"):
		info.Type = "mobile"
	case strings.Contains(ua, "tablet"), strings.Contains(ua, "ipad"):
		info.Type = "tablet"
	default:
		info.Type = "desktop"
	}

	info.Name = info.Browser + " on " + info.OS

	return info
}
