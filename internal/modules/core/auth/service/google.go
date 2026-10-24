package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	userDomain "timebox-be/internal/modules/core/user/domain"
	userRepo "timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/googleauth"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/validator"
)

// ─── Google sign-in ─────────────────────────────────────────────────
//
// The app runs Google's authorization code flow with PKCE and hands the
// code to the backend, which holds the client secret, exchanges it and
// verifies the ID token (pkg/googleauth). An identity is keyed by Google's
// account id ("sub"), and an account links at most one Google account.

var (
	// ErrGoogleNotConfigured: the platform has no Google OAuth client.
	ErrGoogleNotConfigured = errors.New("google sign-in not configured")
	// ErrInvalidRedirectURI: the redirect URI is not allowed for the
	// platform.
	ErrInvalidRedirectURI = errors.New("redirect uri is not allowed")
	// ErrGoogleAuthFailed: Google refused the code, or its answer did not
	// verify.
	ErrGoogleAuthFailed = errors.New("google sign-in failed")
	// ErrGoogleEmailNotVerified: a Google account whose email Google has
	// not verified cannot be matched to an account by that email.
	ErrGoogleEmailNotVerified = errors.New("google account email is not verified")
	// ErrGoogleAccountConflict: the account with the Google account's
	// email signs in with another Google account.
	ErrGoogleAccountConflict = errors.New("the account with this email is linked to another google account")
	// ErrGoogleAlreadyLinked: the caller's account has a Google account
	// linked already; unlink it first.
	ErrGoogleAlreadyLinked = errors.New("a google account is already linked")
	// ErrGoogleAccountInUse: the Google account signs in to another
	// account.
	ErrGoogleAccountInUse = errors.New("google account is linked to another account")
	// ErrGoogleNotLinked: the caller's account has no Google account to
	// unlink.
	ErrGoogleNotLinked = errors.New("no google account is linked")
)

// googleImportTimeout bounds copying a Google profile picture, which a
// sign-in waits for.
const googleImportTimeout = 5 * time.Second

// GoogleClient returns the OAuth client id the app starts Google's flow
// with on platform.
func (s *AuthService) GoogleClient(platform string) (*dto.GoogleClientResponse, error) {
	if s.google == nil {
		return nil, ErrGoogleNotConfigured
	}
	clientID, err := s.google.ClientID(platform)
	if err != nil {
		return nil, ErrGoogleNotConfigured
	}
	return &dto.GoogleClientResponse{
		Platform:     platform,
		ClientID:     clientID,
		RedirectURIs: s.google.RedirectURIs(platform),
	}, nil
}

// exchangeGoogle trades the app's code for the verified Google account.
func (s *AuthService) exchangeGoogle(ctx context.Context, e googleauth.Exchange) (*googleauth.Identity, error) {
	if s.google == nil {
		return nil, ErrGoogleNotConfigured
	}
	identity, err := s.google.Exchange(ctx, e)
	switch {
	case errors.Is(err, googleauth.ErrNotConfigured):
		return nil, ErrGoogleNotConfigured
	case errors.Is(err, googleauth.ErrInvalidRedirectURI):
		return nil, ErrInvalidRedirectURI
	case err != nil:
		logger.Warn("Google sign-in failed", logger.String("platform", e.Platform), logger.Err(err))
		return nil, ErrGoogleAuthFailed
	case identity.Email == "":
		// The app asks for the email scope; without it the account cannot
		// be named.
		logger.Warn("Google sign-in without an email", logger.String("platform", e.Platform))
		return nil, ErrGoogleAuthFailed
	}
	return identity, nil
}

// SignInWithGoogle is the single entry point for both sign-in and
// sign-up with Google. Flow:
//
//  1. Exchange the code and verify the ID token.
//  2. Look up core.user_identities by (google, sub).
//     - Found → refresh the identity snapshot, sign that user in.
//  3. Not found → look up core.users by email. Steps 3 and 4 bind the
//     Google account to an email address, so Google must report that
//     email as verified.
//     - Found → link the Google account to the existing account and sign
//     that user in; the user keeps their roles. If the account's own
//     email was never verified, anyone could have registered it with this
//     address, so the Google user — the proven owner — claims it: the
//     registrant's password is removed and every session and API key of
//     the account is revoked (see endPriorAccessTx). An account that links
//     another Google account already is refused.
//  4. Not found at all → first-time Google user: create a core.users row
//     (no password, no role — same as SignUp) and link the identity, in
//     one transaction.
//
// Two first sign-ins racing each other make one of them hit a unique
// index; that one starts over once and finds what the other created.
func (s *AuthService) SignInWithGoogle(
	ctx context.Context,
	req *dto.GoogleSignInRequest,
	deviceInfo domain.DeviceInfo,
	ipAddress string,
) (*dto.GoogleSignInResponse, error) {
	identity, err := s.exchangeGoogle(ctx, req.Exchange())
	if err != nil {
		return nil, err
	}
	emailAddr := validator.NormalizeEmail(identity.Email)

	user, isNewUser, err := s.resolveGoogleUser(ctx, identity, emailAddr, req.Timezone)
	if _, race := database.UniqueViolation(err); race || errors.Is(err, userRepo.ErrEmailTaken) || errors.Is(err, userRepo.ErrUsernameTaken) {
		user, isNewUser, err = s.resolveGoogleUser(ctx, identity, emailAddr, req.Timezone)
	}
	if err != nil {
		return nil, err
	}

	var sess *session
	// Under the user row lock, like a password sign-in: a change ending
	// every session either comes first and is seen here, or waits and
	// ends this one.
	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		current, err := s.userRepo.FindByIDForUpdate(ctx, user.ID)
		if err != nil {
			return err
		}
		switch {
		case current == nil || !current.IsActive:
			return ErrUserNotActive
		case s.security.EmailVerificationRequired && !current.IsEmailVerified:
			// A linked account whose verification an admin withdrew: Google
			// proves the Google account, not the address the account has.
			return &EmailNotVerifiedError{Email: current.Email}
		}
		user = current

		// A Google sign-in proves identity without the password, so it does
		// not reset the password-failure count or lift a lock.
		if err := s.userRepo.TouchLastLogin(ctx, user.ID); err != nil {
			return err
		}
		sess, err = s.startSession(ctx, user, deviceInfo, ipAddress)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, audit.ActionSignIn, user.ID, map[string]any{"method": "google", "new_user": isNewUser, "session_id": sess.sessionID})

	resp, err := s.signInResponse(ctx, user, sess)
	if err != nil {
		return nil, err
	}
	return &dto.GoogleSignInResponse{SignInResponse: *resp, IsNewUser: isNewUser}, nil
}

// resolveGoogleUser runs steps 2–4 of SignInWithGoogle. timezone, when
// set, is the zone of a newly provisioned account.
func (s *AuthService) resolveGoogleUser(ctx context.Context, identity *googleauth.Identity, emailAddr string, timezone *string) (*userDomain.User, bool, error) {
	// 2. Identity already linked → returning user.
	linked, err := s.userIdentityRepo.FindByProvider(ctx, userDomain.IdentityProviderGoogle, identity.Subject)
	if err != nil {
		return nil, false, err
	}
	if linked != nil {
		user, err := s.userRepo.FindByID(ctx, linked.UserID)
		if err != nil {
			return nil, false, err
		}
		if user == nil || !user.IsActive {
			return nil, false, ErrUserNotActive
		}
		// Refresh the snapshot so audit/debug fields stay current.
		_ = s.userIdentityRepo.UpdateOnLogin(ctx, linked.ID, &emailAddr, marshalProfile(identity))
		return user, false, nil
	}

	// 3. No identity yet — try linking to an existing user by email.
	if !identity.EmailVerified {
		return nil, false, ErrGoogleEmailNotVerified
	}
	existing, err := s.userRepo.FindByEmail(ctx, emailAddr)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if !existing.IsActive {
			return nil, false, ErrUserNotActive
		}
		// The claim, the link and the revocations commit together: the
		// account never changes hands while the registrant's access lives on.
		claimed := !existing.IsEmailVerified
		err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
			if _, err := s.userRepo.FindByIDForUpdate(ctx, existing.ID); err != nil {
				return err
			}
			other, err := s.userIdentityRepo.FindByUser(ctx, existing.ID, userDomain.IdentityProviderGoogle)
			if err != nil {
				return err
			}
			if other != nil {
				return ErrGoogleAccountConflict
			}
			if claimed {
				// False when the email got verified meanwhile (e.g. its owner
				// entered a verification code): then this is a plain link.
				if claimed, err = s.userRepo.ClaimUnverified(ctx, existing.ID, time.Now()); err != nil {
					return err
				}
			}
			if claimed {
				if err := s.endPriorAccessTx(ctx, existing.ID, true); err != nil {
					return err
				}
			}
			return s.linkGoogleIdentity(ctx, existing.ID, identity, emailAddr)
		})
		if err != nil {
			return nil, false, err
		}
		if claimed {
			s.invalidateSubject(ctx, existing.ID)
			logger.Warn("Unverified account claimed through Google sign-in", logger.String("user_id", existing.ID))
			s.record(ctx, audit.ActionAccountClaimed, existing.ID, map[string]any{"via": "google"})
			existing.PasswordHash = ""
			existing.MarkEmailVerified(true, time.Now())
		}
		s.record(ctx, audit.ActionGoogleLinked, existing.ID, map[string]any{"via": "sign_in"})
		_ = s.mailer.SendGoogleLinkedEmail(existing.Email, displayName(existing), emailAddr)
		s.importGooglePicture(ctx, existing, identity)
		return existing, false, nil
	}

	// 4. Brand-new user — full provisioning.
	user, err := s.provisionGoogleUser(ctx, identity, emailAddr, timezone)
	if err != nil {
		return nil, false, err
	}
	return user, true, nil
}

// provisionGoogleUser creates the user row and links the Google identity
// in one transaction. Like SignUp, no role is assigned.
func (s *AuthService) provisionGoogleUser(ctx context.Context, identity *googleauth.Identity, emailAddr string, timezone *string) (*userDomain.User, error) {
	username, err := s.allocateUsernameFromEmail(ctx, emailAddr)
	if err != nil {
		return nil, err
	}

	// Names: prefer the provider's `name` claim; if it's empty (rare
	// for Google) fall back to the email local-part.
	fullName := identity.Name
	if fullName == "" {
		fullName = usernameFromEmail(emailAddr)
	}

	now := time.Now()
	user := &userDomain.User{
		ID:        uuid.New().String(),
		Email:     emailAddr,
		Username:  username,
		FullName:  &fullName,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if timezone != nil {
		user.Timezone = *timezone
	}
	// Google verified the email (checked by the caller), so the account
	// can be used even with EMAIL_VERIFICATION_REQUIRED on.
	user.MarkEmailVerified(true, now)
	// Copied before the row exists, so the account starts with it. Lost if
	// the insert below loses a race: the retry copies it again.
	if url := s.copyGooglePicture(ctx, user.ID, identity); url != "" {
		user.AvatarURL = &url
	}

	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		if err := s.userRepo.Create(ctx, user); err != nil {
			return err
		}
		return s.linkGoogleIdentity(ctx, user.ID, identity, emailAddr)
	})
	if err != nil {
		return nil, err
	}

	logger.Info("User provisioned via Google sign-in", logger.String("user_id", user.ID))
	_ = s.mailer.SendWelcomeEmail(user.Email, displayName(user))

	return user, nil
}

// linkGoogleIdentity inserts the (google, sub) row for a user that
// already exists in core.users.
func (s *AuthService) linkGoogleIdentity(ctx context.Context, userID string, identity *googleauth.Identity, emailAddr string) error {
	now := time.Now()
	return s.userIdentityRepo.Create(ctx, &userDomain.UserIdentity{
		ID:             uuid.New().String(),
		UserID:         userID,
		Provider:       userDomain.IdentityProviderGoogle,
		ProviderUserID: identity.Subject,
		Email:          &emailAddr,
		RawProfile:     marshalProfile(identity),
		LastLoginAt:    &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

// copyGooglePicture copies the Google account's picture into storage and
// returns its URL, or "" when there is none, storage is not configured, or
// the copy failed (logged: an avatar is not worth failing a sign-in). The
// Google URL itself is never stored: the apps load images from our storage
// only.
func (s *AuthService) copyGooglePicture(ctx context.Context, userID string, identity *googleauth.Identity) string {
	if s.avatars == nil || identity.Picture == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, googleImportTimeout)
	defer cancel()

	url, err := s.avatars.ImportGooglePicture(ctx, userID, identity.Picture)
	if err != nil {
		logger.Warn("Failed to copy Google profile picture", logger.String("user_id", userID), logger.Err(err))
		return ""
	}
	return url
}

// importGooglePicture gives user, if they have no avatar, the picture of
// the Google account just linked.
func (s *AuthService) importGooglePicture(ctx context.Context, user *userDomain.User, identity *googleauth.Identity) {
	if user.AvatarURL != nil {
		return
	}
	url := s.copyGooglePicture(ctx, user.ID, identity)
	if url == "" {
		return
	}
	if _, err := s.userRepo.SetAvatar(ctx, user.ID, &url); err != nil {
		logger.Warn("Failed to set avatar from Google", logger.String("user_id", user.ID), logger.Err(err))
		return
	}
	user.AvatarURL = &url
}

// checkGoogleReauth accepts a Google sign-in, made just now, into the
// Google account linked to user.
func (s *AuthService) checkGoogleReauth(ctx context.Context, user *userDomain.User, e googleauth.Exchange) error {
	identity, err := s.exchangeGoogle(ctx, e)
	if err != nil {
		return err
	}

	linked, err := s.userIdentityRepo.FindByProvider(ctx, userDomain.IdentityProviderGoogle, identity.Subject)
	if err != nil {
		return err
	}
	if linked == nil || linked.UserID != user.ID {
		return ErrGoogleAccountMismatch
	}
	// A code is single use and short lived, so its ID token is new; this
	// only guards against a clock or a replay gone wrong.
	if time.Since(identity.IssuedAt) > reauthMaxAge {
		return ErrGoogleReauthStale
	}
	return nil
}

// LinkGoogle links the Google account behind e to the caller's account,
// so it can sign in with it. The current password is required: a session
// alone — perhaps a stolen access token — must not be able to add a way
// in. An account without a password has its Google account linked already.
func (s *AuthService) LinkGoogle(ctx context.Context, userID, password string, e googleauth.Exchange) (*dto.UserInfo, error) {
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.reauthenticate(ctx, user, Reauth{Password: password}); err != nil {
		return nil, err
	}
	identity, err := s.exchangeGoogle(ctx, e)
	if err != nil {
		return nil, err
	}
	googleEmail := validator.NormalizeEmail(identity.Email)

	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		if err := s.confirmReauthTx(ctx, user); err != nil {
			return err
		}
		mine, err := s.userIdentityRepo.FindByUser(ctx, user.ID, userDomain.IdentityProviderGoogle)
		if err != nil {
			return err
		}
		if mine != nil {
			return ErrGoogleAlreadyLinked
		}
		other, err := s.userIdentityRepo.FindByProvider(ctx, userDomain.IdentityProviderGoogle, identity.Subject)
		if err != nil {
			return err
		}
		if other != nil {
			return ErrGoogleAccountInUse
		}
		err = s.linkGoogleIdentity(ctx, user.ID, identity, googleEmail)
		if _, dup := database.UniqueViolation(err); dup {
			// Linked elsewhere since the check.
			return ErrGoogleAccountInUse
		}
		return err
	})
	if err != nil {
		return nil, err
	}

	logger.Info("Google account linked", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionGoogleLinked, user.ID, map[string]any{"via": "settings"})
	_ = s.mailer.SendGoogleLinkedEmail(user.Email, displayName(user), googleEmail)
	s.importGooglePicture(ctx, user, identity)

	info, err := s.userInfo(ctx, user)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// UnlinkGoogle removes the Google account from the caller's account. An
// account without a password must keep it: it would have no way in left.
func (s *AuthService) UnlinkGoogle(ctx context.Context, userID string) (*dto.UserInfo, error) {
	var (
		user        *userDomain.User
		googleEmail string
	)
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		current, err := s.userRepo.FindByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if current == nil || !current.IsActive {
			return ErrUserNotActive
		}
		if current.PasswordHash == "" {
			return ErrNoPassword
		}
		linked, err := s.userIdentityRepo.FindByUser(ctx, userID, userDomain.IdentityProviderGoogle)
		if err != nil {
			return err
		}
		if linked == nil {
			return ErrGoogleNotLinked
		}
		if linked.Email != nil {
			googleEmail = *linked.Email
		}
		if _, err := s.userIdentityRepo.DeleteByUserProvider(ctx, userID, userDomain.IdentityProviderGoogle); err != nil {
			return err
		}
		user = current
		return nil
	})
	if err != nil {
		return nil, err
	}

	logger.Info("Google account unlinked", logger.String("user_id", userID))
	s.record(ctx, audit.ActionGoogleUnlinked, userID, nil)
	_ = s.mailer.SendGoogleUnlinkedEmail(user.Email, displayName(user), googleEmail)

	info := toUserInfo(user, nil)
	return &info, nil
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
		exists, err := s.userRepo.UsernameExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		suffix := "-" + shortHex(4)
		maxBase := validator.UsernameMaxLength - len(suffix)
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

// sanitizeUsername coerces a free-form string to a username that passes
// validator.ValidateUsername: lowercase alnum + dot + hyphen + underscore,
// starting with a letter or digit. Anything else is dropped. Returns ""
// if the result is shorter than 3 chars.
func sanitizeUsername(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	b := make([]byte, 0, len(lower))
	for i := 0; i < len(lower); i++ {
		ch := lower[i]
		alnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		switch {
		case alnum:
			b = append(b, ch)
		case (ch == '.' || ch == '-' || ch == '_') && len(b) > 0:
			b = append(b, ch)
		}
	}
	out := string(b)
	if len(out) > validator.UsernameMaxLength {
		out = out[:validator.UsernameMaxLength]
	}
	if len(out) < validator.UsernameMinLength {
		return ""
	}
	return out
}

// shortHex returns n random bytes as hex, for username suffixes.
func shortHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf) // never fails (crypto/rand, Go 1.24+)
	return hex.EncodeToString(buf)
}

// marshalProfile serializes a verified ID token's claims for storage in
// user_identities.raw_profile. Falls back to "{}" on marshalling error
// so a non-fatal serialization issue doesn't kill the sign-in.
func marshalProfile(identity *googleauth.Identity) []byte {
	b, err := json.Marshal(identity.Claims)
	if err != nil || identity.Claims == nil {
		return []byte("{}")
	}
	return b
}
