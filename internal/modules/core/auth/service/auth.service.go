package service

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"venturo-skeleton-go-simple/internal/config"
	"venturo-skeleton-go-simple/internal/modules/core/auth/domain"
	"venturo-skeleton-go-simple/internal/modules/core/auth/dto"
	"venturo-skeleton-go-simple/internal/modules/core/auth/repository"
	userDomain "venturo-skeleton-go-simple/internal/modules/core/user/domain"
	userRepo "venturo-skeleton-go-simple/internal/modules/core/user/repository"
	"venturo-skeleton-go-simple/pkg/crypto"
	pkgfirebase "venturo-skeleton-go-simple/pkg/firebase"
	"venturo-skeleton-go-simple/pkg/jwt"
	"venturo-skeleton-go-simple/pkg/logger"
	"venturo-skeleton-go-simple/pkg/token"
)

var (
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrUserNotActive         = errors.New("user is not active")
	ErrUserLocked            = errors.New("user account is locked")
	ErrEmailNotVerified      = errors.New("email not verified")
	ErrEmailAlreadyExists    = errors.New("email already exists")
	ErrUsernameAlreadyExists = errors.New("username already exists")
	ErrInvalidRefreshToken   = errors.New("invalid refresh token")
	ErrRefreshTokenExpired   = errors.New("refresh token expired")
	ErrRefreshTokenRevoked   = errors.New("refresh token revoked")

	// Google sign-in errors
	ErrFirebaseNotConfigured = errors.New("google sign-in not configured")
	ErrInvalidGoogleToken    = errors.New("invalid google id token")
	ErrGoogleEmailMissing    = errors.New("google account did not return an email")
	ErrUnexpectedProvider    = errors.New("unexpected firebase sign-in provider")
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

// RoleRepository interface for role operations
type RoleRepository interface {
	GetUserPermissions(ctx context.Context, userID string) ([]string, error)
	GetUserRoleNames(ctx context.Context, userID string) ([]string, error)
}

// PermissionReader is the narrow seam GetMe needs from the authz
// package. Implemented by *authz.Service — accepted as an interface so
// auth doesn't import authz directly (and so tests can fake it).
type PermissionReader interface {
	GetPermissions(ctx context.Context, userID string) ([]string, error)
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
	roleRepo         RoleRepository
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

// SetRoleRepo sets the role repository
func (s *AuthService) SetRoleRepo(repo RoleRepository) {
	s.roleRepo = repo
}

// SetPermissionReader wires the authz-backed permission cache used by
// GetMe. Without this, GetMe falls back to the role repo directly —
// correct, but bypasses Redis.
func (s *AuthService) SetPermissionReader(r PermissionReader) {
	s.permissionReader = r
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

	// No role is assigned here: a self-registered user starts with zero
	// access until a super_admin grants a role.
	logger.Info("User signed up",
		logger.String("user_id", user.ID),
		logger.String("email", user.Email))

	return &dto.SignUpResponse{
		Message: "User registered successfully.",
		User: dto.UserInfo{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			FullName: user.FullName,
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

	// Get user roles and permissions
	var roles, permissions []string
	if s.roleRepo != nil {
		roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID)
		permissions, _ = s.roleRepo.GetUserPermissions(ctx, user.ID)
	}

	// Generate access token
	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(user.ID, user.Email, user.Username, fullName, isSuperAdmin, roles)
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
		logger.String("email", user.Email))

	// A user without roles (e.g. fresh signup) must still get JSON arrays,
	// not null — mirrors issueGoogleTokens.
	if permissions == nil {
		permissions = []string{}
	}
	if roles == nil {
		roles = []string{}
	}

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
		roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID)
	}

	// Generate new access token
	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(user.ID, user.Email, user.Username, fullName, isSuperAdmin, roles)
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

// GetMe returns the signed-in user's current profile, roles, and
// *effective* permissions. Permissions come from the authz cache (Redis)
// when wired; otherwise the role repo is consulted directly. Intended for
// FE rehydration after a page reload or re-sync after an unexpected 403.
func (s *AuthService) GetMe(ctx context.Context, userID string, isSuperAdmin bool) (*dto.MeResponse, error) {
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

	// Roles — always from the role repo. The list is small so there's
	// no cache layer in front of it.
	if s.roleRepo != nil {
		resp.Roles, _ = s.roleRepo.GetUserRoleNames(ctx, userID)
	}

	// Permissions — prefer the Redis-backed authz cache. Fall back to
	// the role repo if the reader isn't wired (tests / bootstrapping).
	switch {
	case s.permissionReader != nil:
		resp.Permissions, err = s.permissionReader.GetPermissions(ctx, userID)
		if err != nil {
			logger.Warn("GetMe: permission reader failed, falling back to role repo", logger.Err(err))
			if s.roleRepo != nil {
				resp.Permissions, _ = s.roleRepo.GetUserPermissions(ctx, userID)
			}
		}
	case s.roleRepo != nil:
		resp.Permissions, _ = s.roleRepo.GetUserPermissions(ctx, userID)
	}

	if resp.Permissions == nil {
		resp.Permissions = []string{}
	}
	if resp.Roles == nil {
		resp.Roles = []string{}
	}

	return resp, nil
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
//       then sign that user in. The pre-existing user keeps their
//       roles — no auto-provisioning here.
//  4. Not found at all → first-time Google user:
//       a) create a new core.users row (no password, no role —
//          same as SignUp)
//       b) link the Google identity
//       c) sign the new user in
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
		return nil, errors.New("user identity repository not set")
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

	// From here on the response build mirrors SignIn: load roles,
	// permissions; issue JWT + refresh token.
	return s.issueGoogleTokens(ctx, user, isNewUser, deviceInfo, ipAddress)
}

// provisionGoogleUser creates the user row and links the Google
// identity. Like SignUp, no role is assigned.
func (s *AuthService) provisionGoogleUser(
	ctx context.Context,
	tok *pkgfirebase.VerifiedToken,
	email string,
) (*userDomain.User, error) {
	now := time.Now()

	// Username: derived from email local-part. Collisions resolved by
	// appending a 4-hex suffix.
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
		ID:              uuid.New().String(),
		Email:           email,
		Username:        username,
		FullName:        &full,
		AvatarURL:       avatarURL,
		IsActive:        true,
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

	// Link the Google identity after the user row exists so a failed
	// insert doesn't leave an orphan identity row.
	if err := s.linkGoogleIdentity(ctx, user.ID, tok, email); err != nil {
		return nil, err
	}

	logger.Info("User provisioned via Google sign-in",
		logger.String("user_id", user.ID),
		logger.String("email", user.Email))

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

// issueGoogleTokens packages the response: loads roles, permissions;
// mints access + refresh tokens; persists refresh.
// Logic mirrors the tail of SignIn — kept separate so the Google flow
// can attach `is_new_user`.
func (s *AuthService) issueGoogleTokens(
	ctx context.Context,
	user *userDomain.User,
	isNewUser bool,
	deviceInfo domain.DeviceInfo,
	ipAddress string,
) (*dto.GoogleSignInResponse, error) {
	var roles, permissions []string
	if s.roleRepo != nil {
		roles, _ = s.roleRepo.GetUserRoleNames(ctx, user.ID)
		permissions, _ = s.roleRepo.GetUserPermissions(ctx, user.ID)
	}

	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	isSuperAdmin := containsRole(roles, jwt.RoleSuperAdmin)
	accessToken, err := jwt.GenerateToken(
		user.ID, user.Email, user.Username, fullName, isSuperAdmin, roles,
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
	return "", errors.New("could not allocate unique username after retries")
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
