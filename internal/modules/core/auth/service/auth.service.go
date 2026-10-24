package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/config"
	"timebox-be/internal/database"
	apikeyDTO "timebox-be/internal/modules/core/apikey/dto"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	"timebox-be/internal/modules/core/auth/repository"
	userDomain "timebox-be/internal/modules/core/user/domain"
	userRepo "timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/crypto"
	"timebox-be/pkg/email"
	"timebox-be/pkg/googleauth"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/token"
	"timebox-be/pkg/validator"
)

var (
	// ErrInvalidCredentials covers an unknown login, a wrong password and
	// a locked account alike, so a response never reveals which accounts
	// exist or whether a guess made during a lock was right.
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrUserNotActive         = errors.New("user is not active")
	ErrEmailNotVerified      = errors.New("email not verified")
	ErrEmailAlreadyExists    = errors.New("email already exists")
	ErrUsernameAlreadyExists = errors.New("username already exists")
	ErrInvalidRefreshToken   = errors.New("invalid refresh token")
	ErrRefreshTokenExpired   = errors.New("refresh token expired")
	ErrRefreshTokenRevoked   = errors.New("refresh token revoked")
	// ErrInvalidResetToken: a reset grant that is unknown, used, expired,
	// or issued for an address the account no longer has.
	ErrInvalidResetToken = errors.New("invalid or expired reset token")

	// Password change errors. The caller is signed in, so these may say
	// what went wrong.
	ErrIncorrectPassword = errors.New("current password is incorrect")
	// ErrNoPassword: a Google-only account has no password to change or
	// confirm with, and cannot unlink Google; it sets one through
	// forgot-password.
	ErrNoPassword = errors.New("account has no password")
	// ErrPasswordLocked: too many wrong passwords, through sign-in or a
	// password change; wait for the lock to pass.
	ErrPasswordLocked = errors.New("too many incorrect passwords")
)

// Fallbacks for a service built without these settings. Production reads
// MAX_LOGIN_ATTEMPTS, ACCOUNT_LOCKOUT_DURATION and JWT_REFRESH_EXPIRATION.
const (
	defaultMaxLoginAttempts       = 5
	defaultLockoutDuration        = 30 * time.Minute
	defaultRefreshTokenExpiration = 7 * 24 * time.Hour
	defaultMaxVerificationEmails  = 5
	defaultMaxResetEmails         = 3
)

// desktopAppName is how a session of the desktop app (Electron, whose
// user agent carries "timebox/<version>" and "Electron/<version>") is
// named, in place of a browser.
const desktopAppName = "Timebox Desktop"

// refreshReuseGrace separates a client race from a stolen token. A rotated
// refresh token presented again within this window is most likely two
// tabs refreshing at once, and is simply refused. Later than that, the
// token was copied: every session of the user is revoked.
const refreshReuseGrace = 30 * time.Second

// RoleRepository is what the auth service needs from the role module.
type RoleRepository interface {
	GetUserRoleNames(ctx context.Context, userID string) ([]string, error)
	// RemoveUserRoles joins the transaction carried by ctx.
	RemoveUserRoles(ctx context.Context, userID string) error
}

// SubjectCache is the authz cache (implemented by *authz.Service): the
// user's live permissions, and invalidation after their sessions are
// revoked.
type SubjectCache interface {
	GetSubject(ctx context.Context, userID string) (authz.Subject, error)
	InvalidateUser(ctx context.Context, userID string) error
}

// GoogleOAuth exchanges the authorization codes of Google sign-in
// (implemented by *googleauth.Verifier). Kept as an interface so tests
// can fake Google.
type GoogleOAuth interface {
	ClientID(platform string) (string, error)
	RedirectURIs(platform string) []string
	Exchange(ctx context.Context, e googleauth.Exchange) (*googleauth.Identity, error)
}

// Avatars is the profile photo storage. nil: Google pictures are not
// copied, and accounts made with Google start without one.
type Avatars interface {
	// ImportGooglePicture copies a Google profile picture into storage and
	// returns its public URL.
	ImportGooglePicture(ctx context.Context, userID, pictureURL string) (string, error)
	// Delete removes a stored photo; a URL it did not store is left alone.
	Delete(ctx context.Context, avatarURL string) error
}

// APIKeys is what the auth service needs from the API key module
// (implemented by its service). Writes join the transaction carried by ctx.
type APIKeys interface {
	RevokeAllForUser(ctx context.Context, userID, reason string) error
	DeleteAllForUser(ctx context.Context, userID string) error
	ListAllForUser(ctx context.Context, userID string) ([]apikeyDTO.APIKeyResponse, error)
}

// AuditLog records what happens to accounts, and lists what one user did
// for their data export (implemented by audit.Log).
type AuditLog interface {
	audit.Recorder
	ListByActor(ctx context.Context, userID string, limit int) ([]audit.Entry, error)
}

// RateLimiter counts the emails sent to each account, and keeps codes
// apart by the resend cooldown (implemented by *ratelimit.Limiter).
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
	Hold(ctx context.Context, key string, d time.Duration) (held bool, remaining time.Duration, err error)
	Remaining(ctx context.Context, key string) (time.Duration, error)
}

// Deps are the auth service's collaborators. Every field is required
// except Google (nil disables Google sign-in) and Avatars.
type Deps struct {
	DB            database.TxBeginner
	Users         *userRepo.UserRepository
	Identities    *userRepo.UserIdentityRepository
	RefreshTokens *repository.RefreshTokenRepository
	AccountTokens *repository.AccountTokenRepository
	Roles         RoleRepository
	Subjects      SubjectCache
	APIKeys       APIKeys
	Limiter       RateLimiter
	Audit         AuditLog
	// Mailer should not block (see internal/shared/mailer): the public
	// email endpoints must answer equally fast whether or not they send.
	Mailer  email.EmailService
	Google  GoogleOAuth
	Avatars Avatars
}

type AuthService struct {
	db               database.TxBeginner
	userRepo         *userRepo.UserRepository
	userIdentityRepo *userRepo.UserIdentityRepository
	refreshTokenRepo *repository.RefreshTokenRepository
	accountTokenRepo *repository.AccountTokenRepository
	roleRepo         RoleRepository
	subjects         SubjectCache
	apiKeys          APIKeys
	limiter          RateLimiter
	audit            AuditLog
	mailer           email.EmailService
	google           GoogleOAuth // nil: Google sign-in is disabled
	avatars          Avatars     // nil: Google pictures are not copied
	security         config.SecurityConfig
}

// NewAuthService wires the auth service. A missing required dependency is
// a wiring bug and panics at startup rather than on the first request
// that needs it.
func NewAuthService(security config.SecurityConfig, d Deps) *AuthService {
	required := map[string]bool{
		"DB": d.DB == nil, "Users": d.Users == nil, "Identities": d.Identities == nil,
		"RefreshTokens": d.RefreshTokens == nil, "AccountTokens": d.AccountTokens == nil,
		"Roles": d.Roles == nil, "Subjects": d.Subjects == nil, "APIKeys": d.APIKeys == nil,
		"Limiter": d.Limiter == nil, "Mailer": d.Mailer == nil, "Audit": d.Audit == nil,
	}
	for name, missing := range required {
		if missing {
			panic("auth service: missing dependency " + name)
		}
	}

	return &AuthService{
		db:               d.DB,
		userRepo:         d.Users,
		userIdentityRepo: d.Identities,
		refreshTokenRepo: d.RefreshTokens,
		accountTokenRepo: d.AccountTokens,
		roleRepo:         d.Roles,
		subjects:         d.Subjects,
		apiKeys:          d.APIKeys,
		limiter:          d.Limiter,
		audit:            d.Audit,
		mailer:           d.Mailer,
		google:           d.Google,
		avatars:          d.Avatars,
		security:         security,
	}
}

func (s *AuthService) maxLoginAttempts() int {
	if s.security.MaxLoginAttempts <= 0 {
		return defaultMaxLoginAttempts
	}
	return s.security.MaxLoginAttempts
}

func (s *AuthService) lockoutDuration() time.Duration {
	if s.security.AccountLockoutDuration <= 0 {
		return defaultLockoutDuration
	}
	return s.security.AccountLockoutDuration
}

func (s *AuthService) refreshTokenExpiration() time.Duration {
	if s.security.RefreshTokenExpiration <= 0 {
		return defaultRefreshTokenExpiration
	}
	return s.security.RefreshTokenExpiration
}

// findByLogin resolves a sign-in identifier. Usernames cannot contain "@",
// so an identifier is either an email or a username, never both.
func (s *AuthService) findByLogin(ctx context.Context, login string) (*userDomain.User, error) {
	if strings.Contains(login, "@") {
		return s.userRepo.FindByEmail(ctx, validator.NormalizeEmail(login))
	}
	return s.userRepo.FindByUsername(ctx, validator.NormalizeUsername(login))
}

// SignUp registers a new user and emails them a verification code. The
// account signs in by entering it (VerifyEmail), or with its password once
// verified — or at once, when EMAIL_VERIFICATION_REQUIRED is off.
func (s *AuthService) SignUp(ctx context.Context, req *dto.SignUpRequest) (*dto.SignUpResponse, error) {
	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		logger.Error("Failed to hash password", logger.Err(err))
		return nil, err
	}

	// The email is only a claim here — nothing has proven the registrant
	// owns it — so it stays unverified. Google sign-in relies on this flag
	// to decide whether it may link into an existing account.
	now := time.Now()
	user := &userDomain.User{
		ID:           uuid.New().String(),
		Email:        validator.NormalizeEmail(req.Email),
		Username:     validator.NormalizeUsername(req.Username),
		PasswordHash: passwordHash,
		FullName:     req.FullName,
		Phone:        req.Phone,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if req.Timezone != nil {
		user.Timezone = *req.Timezone
	}

	// A username derived from the email (none was chosen) that someone
	// took meanwhile is derived again, once.
	derived := user.Username == ""
	for attempt := 0; ; attempt++ {
		if derived {
			if user.Username, err = s.allocateUsernameFromEmail(ctx, user.Email); err != nil {
				return nil, err
			}
		}
		err = s.userRepo.Create(ctx, user)
		if errors.Is(err, userRepo.ErrUsernameTaken) && derived && attempt == 0 {
			continue
		}
		break
	}
	if err != nil {
		switch {
		case errors.Is(err, userRepo.ErrEmailTaken):
			return nil, ErrEmailAlreadyExists
		case errors.Is(err, userRepo.ErrUsernameTaken):
			return nil, ErrUsernameAlreadyExists
		}
		return nil, err
	}

	// No role is assigned here: a self-registered user starts with zero
	// access until a super_admin grants a role.
	logger.Info("User signed up", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionSignUp, user.ID, nil)

	// The first code goes out even if a resend for this address is cooling
	// down (asked for before the account existed). The account exists
	// either way; a failed email can be asked for again.
	resend := resendKey(codeKindVerify, user.Email)
	_ = s.holdResend(ctx, resend)
	if err := s.sendVerificationCode(ctx, user); err != nil {
		logger.Error("Failed to send verification code", logger.String("user_id", user.ID), logger.Err(err))
	}

	return &dto.SignUpResponse{
		User:         toUserInfo(user, nil),
		Verification: dto.VerificationSent{Email: user.Email, CodeTicket: s.ticket(ctx, resend)},
	}, nil
}

// SignIn authenticates a user and returns tokens.
//
// The password is checked before anything about the account is revealed:
// a caller who does not know it gets ErrInvalidCredentials whether the
// login is unknown, the password wrong, or the account locked, and an
// unknown login costs as much time as a wrong password.
func (s *AuthService) SignIn(ctx context.Context, req *dto.SignInRequest, deviceInfo domain.DeviceInfo, ipAddress string) (*dto.SignInResponse, error) {
	user, err := s.findByLogin(ctx, req.Login)
	if err != nil {
		return nil, err
	}
	if user == nil {
		crypto.SpendComparison(req.Password)
		s.audit.Record(ctx, audit.Entry{Action: audit.ActionSignInRefused, Details: map[string]any{"reason": "unknown_login"}})
		return nil, ErrInvalidCredentials
	}

	attempt, err := s.reservePasswordAttempt(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if attempt == 0 {
		// Locked: not even a right password gets in, and the answer must
		// not tell a guesser it was right.
		crypto.SpendComparison(req.Password)
		logger.Warn("Sign-in attempt on a locked account", logger.String("user_id", user.ID))
		s.record(ctx, audit.ActionSignInRefused, user.ID, map[string]any{"reason": "locked"})
		return nil, ErrInvalidCredentials
	}

	if !crypto.ComparePassword(user.PasswordHash, req.Password) {
		s.recordWrongPassword(ctx, user, attempt, "sign_in")
		return nil, ErrInvalidCredentials
	}

	var (
		sess    *session
		refused error
		reason  string
	)
	// The clear locks the user row, so a password reset, claim or "sign out
	// everywhere" running meanwhile either lands before it — and this
	// sign-in fails — or waits and ends the session issued here.
	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		// The password is right, which ends the run of wrong ones — unless a
		// parallel guess locked the account since this attempt was counted,
		// or the password changed since it was read; then this sign-in loses
		// too.
		cleared, err := s.userRepo.ClearPasswordAttempts(ctx, user.ID, user.PasswordHash, attempt)
		if err != nil {
			return err
		}
		if !cleared {
			refused, reason = ErrInvalidCredentials, "locked"
			return nil
		}

		// From here on the caller knows the password. The row is locked:
		// this read is current.
		current, err := s.userRepo.FindByID(ctx, user.ID)
		if err != nil {
			return err
		}
		switch {
		case current == nil || !current.IsActive:
			refused, reason = ErrUserNotActive, "inactive"
			return nil
		case s.security.EmailVerificationRequired && !current.IsEmailVerified:
			refused, reason = &EmailNotVerifiedError{Email: current.Email}, "email_unverified"
			return nil
		}
		user = current
		if err := s.userRepo.TouchLastLogin(ctx, user.ID); err != nil {
			return err
		}
		sess, err = s.startSession(ctx, user, deviceInfo, ipAddress)
		return err
	})
	if err != nil {
		return nil, err
	}
	// A refused sign-in still commits the clear.
	if refused != nil {
		s.record(ctx, audit.ActionSignInRefused, user.ID, map[string]any{"reason": reason})
		return nil, refused
	}

	logger.Info("User signed in", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionSignIn, user.ID, map[string]any{"method": "password", "session_id": sess.sessionID})

	return s.signInResponse(ctx, user, sess)
}

// EmailNotVerifiedError refuses a sign-in with the right password into an
// account whose email is not verified yet. It names the address, so a
// client that signed in by username knows where the code goes.
type EmailNotVerifiedError struct {
	Email string
}

func (e *EmailNotVerifiedError) Error() string { return ErrEmailNotVerified.Error() }
func (e *EmailNotVerifiedError) Unwrap() error { return ErrEmailNotVerified }

// signInResponse is what a new session answers with.
func (s *AuthService) signInResponse(ctx context.Context, user *userDomain.User, sess *session) (*dto.SignInResponse, error) {
	info, err := s.userInfo(ctx, user)
	if err != nil {
		return nil, err
	}
	return &dto.SignInResponse{
		AccessToken:  sess.accessToken,
		RefreshToken: sess.refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    accessTokenExpiresIn(),
		User:         info,
		Roles:        sess.roles,
		Permissions:  sess.permissions,
	}, nil
}

// reservePasswordAttempt counts a password check — at sign-in or a
// password change — before it is made, so a burst of parallel guesses gets
// no more checks than MAX_LOGIN_ATTEMPTS: every guess is numbered before
// any of them is checked, and the one that reaches the limit locks the
// account at once. It returns the attempt's number, or 0 when the account
// is locked and nothing may be checked.
func (s *AuthService) reservePasswordAttempt(ctx context.Context, userID string) (int, error) {
	now := time.Now()
	return s.userRepo.ReservePasswordAttempt(ctx, userID, s.maxLoginAttempts(), now, now.Add(s.lockoutDuration()))
}

// recordWrongPassword reports a wrong password checked as attempt for
// purpose ("sign_in", "password_change", "reauth"). The attempt that
// reached the limit has locked the account; the sign-in answer does not
// say so, so the owner learns it by email.
func (s *AuthService) recordWrongPassword(ctx context.Context, user *userDomain.User, attempt int, purpose string) {
	s.record(ctx, audit.ActionWrongPassword, user.ID, map[string]any{"purpose": purpose, "attempt": attempt})
	if attempt < s.maxLoginAttempts() {
		logger.Warn("Invalid password", logger.String("user_id", user.ID), logger.Int("attempt", attempt))
		return
	}
	logger.Warn("User locked due to failed login attempts", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionAccountLocked, user.ID, map[string]any{"locked_for": s.lockoutDuration().String()})
	// Only one attempt per lock reaches the limit: the rest are refused
	// unchecked until it expires.
	_ = s.mailer.SendAccountLockedEmail(user.Email, displayName(user), s.lockoutDuration())
}

// session is a freshly issued token pair plus what the client shows.
type session struct {
	sessionID    string
	accessToken  string
	refreshToken string
	roles        []string
	permissions  []string
}

// startSession starts a sign-in session: a new session id, an access
// token and a refresh token.
func (s *AuthService) startSession(ctx context.Context, user *userDomain.User, deviceInfo domain.DeviceInfo, ipAddress string) (*session, error) {
	sessionID := uuid.New().String()
	sess, err := s.issueAccessToken(ctx, user, sessionID)
	if err != nil {
		return nil, err
	}

	sess.refreshToken, err = s.storeRefreshToken(ctx, user.ID, domain.RefreshToken{
		SessionID:  sessionID,
		SignedInAt: time.Now(),
		DeviceInfo: deviceInfo,
		IPAddress:  &ipAddress,
	})
	if err != nil {
		return nil, err
	}

	return sess, nil
}

// issueAccessToken signs an access token for user in session sessionID.
// Roles and the super admin flag in it are informational; permissions
// come from the live subject.
func (s *AuthService) issueAccessToken(ctx context.Context, user *userDomain.User, sessionID string) (*session, error) {
	subject, err := s.subjects.GetSubject(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	roles, err := s.roleRepo.GetUserRoleNames(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []string{}
	}

	fullName := ""
	if user.FullName != nil {
		fullName = *user.FullName
	}
	accessToken, err := jwt.GenerateToken(user.ID, user.Email, user.Username, fullName, subject.SuperAdmin, roles, sessionID)
	if err != nil {
		logger.Error("Failed to generate access token", logger.Err(err))
		return nil, err
	}

	return &session{sessionID: sessionID, accessToken: accessToken, roles: roles, permissions: subject.Permissions}, nil
}

// storeRefreshToken stores a new refresh token for userID and returns it;
// only its hash is stored. t describes the session (SessionID,
// SignedInAt, DeviceInfo, IPAddress) and, on rotation, the token it
// replaces (RotatedFromID).
func (s *AuthService) storeRefreshToken(ctx context.Context, userID string, t domain.RefreshToken) (string, error) {
	refreshTokenStr, err := token.GenerateSecureToken(32)
	if err != nil {
		logger.Error("Failed to generate refresh token", logger.Err(err))
		return "", err
	}

	now := time.Now()
	t.ID = uuid.New().String()
	t.UserID = userID
	t.TokenHash = token.Hash(refreshTokenStr)
	t.ExpiresAt = now.Add(s.refreshTokenExpiration())
	t.CreatedAt = now
	if err := s.refreshTokenRepo.Create(ctx, &t); err != nil {
		return "", err
	}

	return refreshTokenStr, nil
}

// RefreshToken rotates a refresh token: the presented token is revoked and
// a new access + refresh pair of the same session is issued.
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (*dto.RefreshTokenResponse, error) {
	tokenHash := token.Hash(refreshTokenStr)

	var (
		claimed *domain.RefreshToken
		sess    *session
		refused error
	)
	// The old token's revocation, its successor and the new access token
	// commit together: no reader ever sees the session without a live
	// token, which would make it look ended (see
	// authz.Subject.EndedSessions), and a failure leaves the presented
	// token usable for a retry.
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		presented, err := s.refreshTokenRepo.FindByTokenHash(ctx, tokenHash)
		if err != nil || presented == nil {
			return err
		}
		// Locked before the claim: a change ending every session of the
		// user waits for this rotation and then revokes its new token, or
		// has revoked the presented one already.
		user, err := s.userRepo.FindByIDForUpdate(ctx, presented.UserID)
		if err != nil {
			return err
		}

		// Claim the token atomically: of two requests presenting the
		// same token, exactly one gets it.
		if claimed, err = s.refreshTokenRepo.Claim(ctx, tokenHash); err != nil || claimed == nil {
			return err
		}

		// Refused, the claimed token stays consumed: the session is over.
		switch {
		case user == nil || !user.IsActive:
			refused = ErrUserNotActive
			return nil
		case s.security.EmailVerificationRequired && !user.IsEmailVerified:
			// Verification was required, or withdrawn, after sign-in.
			refused = ErrEmailNotVerified
			return nil
		}

		refreshToken, err := s.storeRefreshToken(ctx, user.ID, domain.RefreshToken{
			SessionID:     claimed.SessionID,
			SignedInAt:    claimed.SignedInAt,
			DeviceInfo:    claimed.DeviceInfo,
			IPAddress:     claimed.IPAddress,
			RotatedFromID: &claimed.ID,
		})
		if err != nil {
			return err
		}
		// After the successor is stored: a subject loaded now must not see
		// the session without a live token.
		if sess, err = s.issueAccessToken(ctx, user, claimed.SessionID); err != nil {
			return err
		}
		sess.refreshToken = refreshToken
		return nil
	})
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return nil, s.rejectUnclaimable(ctx, tokenHash)
	}
	if refused != nil {
		// The session is over: its access token goes with it.
		s.invalidateSubject(ctx, claimed.UserID)
		return nil, refused
	}

	return &dto.RefreshTokenResponse{
		AccessToken:  sess.accessToken,
		RefreshToken: sess.refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    accessTokenExpiresIn(),
	}, nil
}

// rejectUnclaimable explains why a refresh token could not be claimed.
// A token that was rotated more than refreshReuseGrace ago is being
// replayed — whoever presents it copied it — so every session of the
// user is revoked.
func (s *AuthService) rejectUnclaimable(ctx context.Context, tokenHash string) error {
	refreshToken, err := s.refreshTokenRepo.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	if refreshToken == nil {
		return ErrInvalidRefreshToken
	}
	if !refreshToken.IsRevoked() {
		// Unclaimable but not revoked: only expiry is left.
		return ErrRefreshTokenExpired
	}

	rotated, err := s.refreshTokenRepo.WasRotated(ctx, refreshToken.ID)
	if err != nil {
		return err
	}
	if rotated && time.Since(*refreshToken.RevokedAt) > refreshReuseGrace {
		logger.Warn("Rotated refresh token presented again; revoking all sessions",
			logger.String("user_id", refreshToken.UserID),
			logger.String("token_id", refreshToken.ID))
		s.record(ctx, audit.ActionRefreshTokenReused, refreshToken.UserID, map[string]any{"session_id": refreshToken.SessionID})
		if err := s.RevokeAllSessions(ctx, refreshToken.UserID); err != nil {
			logger.Error("Failed to revoke sessions after refresh token reuse", logger.Err(err))
		}
	}

	return ErrRefreshTokenRevoked
}

// Logout ends the session one of the caller's refresh tokens belongs to:
// the token is revoked, and the session's access tokens are refused from
// the next request on.
func (s *AuthService) Logout(ctx context.Context, userID, refreshTokenStr string) error {
	sessionID, err := s.refreshTokenRepo.RevokeByTokenHash(ctx, userID, token.Hash(refreshTokenStr))
	if err != nil {
		return err
	}
	if sessionID != "" {
		s.invalidateSubject(ctx, userID)
		s.record(ctx, audit.ActionLogout, userID, map[string]any{"session_id": sessionID})
	}
	return nil
}

// LogoutAll ends every session of the user, access tokens included.
func (s *AuthService) LogoutAll(ctx context.Context, userID string) error {
	if err := s.RevokeAllSessions(ctx, userID); err != nil {
		return err
	}
	s.record(ctx, audit.ActionLogoutAll, userID, nil)
	return nil
}

// RevokeAllSessions revokes every refresh token of the user and rejects
// every access token issued so far. Used for sign-out everywhere, a
// password change, deactivation, and a detected stolen refresh token.
func (s *AuthService) RevokeAllSessions(ctx context.Context, userID string) error {
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		return s.revokeSessionsTx(ctx, userID)
	})
	if err != nil {
		return err
	}
	s.invalidateSubject(ctx, userID)
	return nil
}

// revokeSessionsTx moves the session epoch and revokes the user's refresh
// tokens, in the transaction ctx carries. The caller invalidates the authz
// cache once that transaction has committed (invalidateSubject): done
// earlier, a concurrent request could cache the old epoch again.
//
// The user row is locked first, which waits for a sign-in or refresh
// holding it (see UserRepository.FindByIDForUpdate): the epoch, taken
// after that, is later than the access token such a request issued, the
// revocation sees its refresh token, and the next one waits for this
// transaction.
func (s *AuthService) revokeSessionsTx(ctx context.Context, userID string) error {
	if _, err := s.userRepo.FindByIDForUpdate(ctx, userID); err != nil {
		return err
	}
	if err := s.userRepo.RevokeTokensIssuedBefore(ctx, userID, time.Now()); err != nil {
		return err
	}
	return s.refreshTokenRepo.RevokeAllByUserID(ctx, userID)
}

// invalidateSubject drops the cached authz state, which carries the old
// session epoch. A failure is logged: the TTL heals the cache.
func (s *AuthService) invalidateSubject(ctx context.Context, userID string) {
	if err := s.subjects.InvalidateUser(ctx, userID); err != nil {
		logger.Warn("authz cache invalidation failed",
			logger.String("user_id", userID),
			logger.Err(err))
	}
}

// record writes an audit entry about the account userID. The actor is
// the caller in ctx (see middleware.JWTAuth); a request made before
// anyone is signed in — sign-up, sign-in, an emailed link — is recorded
// as acting on its own account.
func (s *AuthService) record(ctx context.Context, action, userID string, details map[string]any) {
	e := audit.Entry{Action: action, TargetType: audit.TargetUser, TargetID: userID, Details: details}
	if audit.ActorID(ctx) == "" {
		e.ActorID = userID
	}
	s.audit.Record(ctx, e)
}

// PurgeExpiredTokens deletes refresh tokens, emailed codes and reset
// grants that can no longer be used, and returns how many went.
func (s *AuthService) PurgeExpiredTokens(ctx context.Context) (int64, error) {
	now := time.Now()
	refresh, err := s.refreshTokenRepo.DeleteExpiredBefore(ctx, now)
	if err != nil {
		return 0, err
	}
	codes, err := s.accountTokenRepo.DeleteExpiredBefore(ctx, now)
	if err != nil {
		return refresh, err
	}
	return refresh + codes, nil
}

// GetMe returns the signed-in user's current profile, roles, and
// effective permissions from the caller's live subject. Intended for FE
// rehydration after a page reload or re-sync after an unexpected 403.
func (s *AuthService) GetMe(ctx context.Context, userID string, subject authz.Subject) (*dto.MeResponse, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotActive
	}

	roles, err := s.roleRepo.GetUserRoleNames(ctx, userID)
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []string{}
	}
	permissions := subject.Permissions
	if permissions == nil {
		permissions = []string{}
	}

	info, err := s.userInfo(ctx, user)
	if err != nil {
		return nil, err
	}
	return &dto.MeResponse{
		User:         info,
		Roles:        roles,
		Permissions:  permissions,
		IsSuperAdmin: subject.SuperAdmin,
	}, nil
}

// userInfo describes user to themselves, with the Google account linked
// to it.
func (s *AuthService) userInfo(ctx context.Context, user *userDomain.User) (dto.UserInfo, error) {
	google, err := s.userIdentityRepo.FindByUser(ctx, user.ID, userDomain.IdentityProviderGoogle)
	if err != nil {
		return dto.UserInfo{}, err
	}
	return toUserInfo(user, google), nil
}

// toUserInfo describes user, whose linked Google identity is google (nil:
// none).
func toUserInfo(user *userDomain.User, google *userDomain.UserIdentity) dto.UserInfo {
	info := dto.UserInfo{
		ID:              user.ID,
		Email:           user.Email,
		Username:        user.Username,
		FullName:        user.FullName,
		AvatarURL:       user.AvatarURL,
		Timezone:        user.Timezone,
		HasPassword:     user.PasswordHash != "",
		IsEmailVerified: user.IsEmailVerified,
	}
	if google != nil {
		info.Google = &dto.LinkedGoogle{Email: google.Email, LinkedAt: google.CreatedAt}
	}
	return info
}

// accessTokenExpiresIn is the expires_in sent with every token pair; it
// follows JWT_EXPIRATION, the same setting the token is signed with.
func accessTokenExpiresIn() int {
	return int(jwt.GetExpirationTime().Seconds())
}

// GetDeviceInfoFromUserAgent extracts device info from a user agent
// string. Checks run from most to least specific: the desktop app's
// Electron UA also says "Chrome", every Edge UA also says "Chrome", every
// Chrome UA says "Safari", iOS UAs say "Mac OS X" and Android UAs say
// "Linux".
func GetDeviceInfoFromUserAgent(userAgent string) domain.DeviceInfo {
	info := domain.DeviceInfo{}

	ua := strings.ToLower(userAgent)

	switch {
	case strings.Contains(ua, "timebox/"), strings.Contains(ua, "electron/"):
		info.Browser = desktopAppName
	case strings.Contains(ua, "edg/"), strings.Contains(ua, "edge/"):
		info.Browser = "Edge"
	case strings.Contains(ua, "opr/"), strings.Contains(ua, "opera"):
		info.Browser = "Opera"
	case strings.Contains(ua, "firefox/"), strings.Contains(ua, "fxios/"):
		info.Browser = "Firefox"
	case strings.Contains(ua, "chrome/"), strings.Contains(ua, "crios/"):
		info.Browser = "Chrome"
	case strings.Contains(ua, "safari/"):
		info.Browser = "Safari"
	default:
		info.Browser = "Unknown"
	}

	switch {
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"):
		info.OS = "iOS"
	case strings.Contains(ua, "android"):
		info.OS = "Android"
	case strings.Contains(ua, "windows"):
		info.OS = "Windows"
	case strings.Contains(ua, "mac os x"), strings.Contains(ua, "macintosh"):
		info.OS = "macOS"
	case strings.Contains(ua, "linux"):
		info.OS = "Linux"
	default:
		info.OS = "Unknown"
	}

	switch {
	case strings.Contains(ua, "ipad"), strings.Contains(ua, "tablet"):
		info.Type = "tablet"
	case strings.Contains(ua, "mobile"), strings.Contains(ua, "iphone"), strings.Contains(ua, "android"):
		info.Type = "mobile"
	default:
		info.Type = "desktop"
	}

	info.Name = info.Browser + " on " + info.OS

	return info
}
