package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/user/domain"
	"timebox-be/internal/modules/core/user/dto"
	"timebox-be/pkg/logger"
)

// Returned by Create and Update when the email or username belongs to
// another live account. The partial unique indexes decide, so two
// concurrent requests cannot both get through.
var (
	ErrEmailTaken    = errors.New("email is already in use")
	ErrUsernameTaken = errors.New("username is already in use")
)

type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

// q runs statements on the transaction carried by ctx, if any.
func (r *UserRepository) q(ctx context.Context) database.DBTX {
	return database.Querier(ctx, r.db)
}

// userColumns matches scanUser. A NULL password_hash (an account that
// only signs in through an external provider) reads as "".
const userColumns = `
	id, email, username, COALESCE(password_hash, ''), full_name, phone, avatar_url, timezone,
	is_active, is_email_verified, email_verified_at,
	failed_login_count, locked_until, last_login_at,
	created_at, created_by, updated_at, updated_by, deleted_at, deleted_by`

func scanUser(row pgx.Row) (*domain.User, error) {
	var user domain.User
	err := row.Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&user.FullName, &user.Phone, &user.AvatarURL, &user.Timezone,
		&user.IsActive, &user.IsEmailVerified, &user.EmailVerifiedAt,
		&user.FailedLoginCount, &user.LockedUntil, &user.LastLoginAt,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
		&user.DeletedAt, &user.DeletedBy,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// translateUnique turns a violation of the email or username index into
// ErrEmailTaken / ErrUsernameTaken.
func translateUnique(err error) error {
	switch constraint, _ := database.UniqueViolation(err); constraint {
	case "users_email_active_key":
		return ErrEmailTaken
	case "users_username_active_key":
		return ErrUsernameTaken
	}
	return err
}

// Create inserts a user. An empty PasswordHash is stored as NULL: the
// account signs in only through an external identity provider until a
// local password is set. An empty Timezone becomes DefaultTimezone. A
// duplicate email or username fails with ErrEmailTaken / ErrUsernameTaken.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	if user.Timezone == "" {
		user.Timezone = domain.DefaultTimezone
	}

	query := `
		INSERT INTO core.users (
			id, email, username, password_hash, full_name, phone, avatar_url, timezone,
			is_active, is_email_verified, email_verified_at,
			created_at, created_by, updated_at, updated_by
		) VALUES (
			$1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
	`

	_, err := r.q(ctx).Exec(ctx, query,
		user.ID, user.Email, user.Username, user.PasswordHash,
		user.FullName, user.Phone, user.AvatarURL, user.Timezone,
		user.IsActive, user.IsEmailVerified, user.EmailVerifiedAt,
		user.CreatedAt, user.CreatedBy, user.UpdatedAt, user.UpdatedBy,
	)
	if err != nil {
		if err := translateUnique(err); errors.Is(err, ErrEmailTaken) || errors.Is(err, ErrUsernameTaken) {
			return err
		}
		logger.Error("Failed to create user", logger.Err(err))
		return err
	}

	return nil
}

// findOne returns the live user matching column = value, or nil.
func (r *UserRepository) findOne(ctx context.Context, column, value string) (*domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM core.users WHERE ` + column + ` = $1 AND deleted_at IS NULL`

	user, err := scanUser(r.q(ctx).QueryRow(ctx, query, value))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find user", logger.String("by", column), logger.Err(err))
		return nil, err
	}
	return user, nil
}

// FindByID finds a user by ID
func (r *UserRepository) FindByID(ctx context.Context, id string) (*domain.User, error) {
	return r.findOne(ctx, "id", id)
}

// FindByIDForUpdate finds a live user and locks the row until the
// transaction ctx carries ends; without one the lock is gone at once.
//
// Everything that ends a user's sessions or changes the password updates
// this row first, so it waits for the lock: a session issued or refreshed
// under it is either ended by such a change or issued after it, never
// left behind.
func (r *UserRepository) FindByIDForUpdate(ctx context.Context, id string) (*domain.User, error) {
	// NO KEY: inserts that reference the user (refresh tokens) need not wait.
	query := `SELECT ` + userColumns + ` FROM core.users WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`

	user, err := scanUser(r.q(ctx).QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to lock user", logger.Err(err))
		return nil, err
	}
	return user, nil
}

// FindByEmail finds a user by (normalized) email
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	return r.findOne(ctx, "email", email)
}

// FindByUsername finds a user by (normalized) username
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	return r.findOne(ctx, "username", username)
}

// FindAll returns one page of live users, newest first.
func (r *UserRepository) FindAll(ctx context.Context, params *dto.UserQueryParams) ([]domain.User, int64, error) {
	conditions := []string{"deleted_at IS NULL"}
	var args []any

	if params.Search != "" {
		args = append(args, "%"+params.Search+"%")
		n := len(args)
		conditions = append(conditions, fmt.Sprintf(
			"(email ILIKE $%d OR username ILIKE $%d OR full_name ILIKE $%d)", n, n, n,
		))
	}

	if params.IsActive != nil {
		args = append(args, *params.IsActive)
		conditions = append(conditions, fmt.Sprintf("is_active = $%d", len(args)))
	}

	whereClause := strings.Join(conditions, " AND ")

	var total int64
	countQuery := "SELECT COUNT(*) FROM core.users WHERE " + whereClause
	if err := r.q(ctx).QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		logger.Error("Failed to count users", logger.Err(err))
		return nil, 0, err
	}

	args = append(args, params.Limit, (params.Page-1)*params.Limit)
	dataQuery := fmt.Sprintf(`
		SELECT %s FROM core.users
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, userColumns, whereClause, len(args)-1, len(args))

	rows, err := r.q(ctx).Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find users", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]domain.User, 0, params.Limit)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			logger.Error("Failed to scan user", logger.Err(err))
			return nil, 0, err
		}
		users = append(users, *user)
	}
	if err := rows.Err(); err != nil {
		logger.Error("Failed to read users", logger.Err(err))
		return nil, 0, err
	}

	return users, total, nil
}

// Update writes the fields an administrator may change. The avatar is not
// one of them: it changes only through SetAvatar. Returns
// database.ErrNotFound when the user does not exist (any more), and
// ErrEmailTaken / ErrUsernameTaken on a duplicate.
func (r *UserRepository) Update(ctx context.Context, user *domain.User) error {
	query := `
		UPDATE core.users
		SET email = $2, username = $3, full_name = $4, phone = $5,
		    is_active = $6, is_email_verified = $7, email_verified_at = $8,
		    updated_at = $9, updated_by = $10, timezone = $11
		WHERE id = $1 AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query,
		user.ID, user.Email, user.Username, user.FullName, user.Phone,
		user.IsActive, user.IsEmailVerified, user.EmailVerifiedAt,
		user.UpdatedAt, user.UpdatedBy, user.Timezone,
	)
	if err != nil {
		if err := translateUnique(err); errors.Is(err, ErrEmailTaken) || errors.Is(err, ErrUsernameTaken) {
			return err
		}
		logger.Error("Failed to update user", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// UpdateProfile changes only a user's own profile fields; a nil value
// keeps the current one. It never touches status or login columns, so it
// cannot undo a concurrent deactivation.
func (r *UserRepository) UpdateProfile(ctx context.Context, id string, fullName, phone, timezone *string) error {
	query := `
		UPDATE core.users
		SET full_name = COALESCE($2, full_name),
		    phone = COALESCE($3, phone),
		    timezone = COALESCE($4, timezone),
		    updated_at = $5, updated_by = $1
		WHERE id = $1 AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, fullName, phone, timezone, time.Now())
	if err != nil {
		logger.Error("Failed to update user profile", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// SetAvatar sets the user's avatar URL — nil removes it — and returns the
// one it replaced, so the caller can delete that picture. The row is
// locked while the old value is read: of two changes racing each other,
// the second gets back the URL the first one set, so every replaced URL is
// returned exactly once. Returns database.ErrNotFound when the user does
// not exist (any more).
func (r *UserRepository) SetAvatar(ctx context.Context, id string, avatarURL *string) (previous *string, err error) {
	// NO KEY: inserts that reference the user (refresh tokens) need not wait.
	query := `
		WITH old AS (
			SELECT id, avatar_url FROM core.users
			WHERE id = $1 AND deleted_at IS NULL
			FOR NO KEY UPDATE
		)
		UPDATE core.users AS u
		SET avatar_url = $2, updated_at = $3, updated_by = $1
		FROM old
		WHERE u.id = old.id
		RETURNING old.avatar_url
	`

	err = r.q(ctx).QueryRow(ctx, query, id, avatarURL, time.Now()).Scan(&previous)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		logger.Error("Failed to set avatar", logger.Err(err))
		return nil, err
	}

	return previous, nil
}

// SoftDelete soft deletes a user. Returns database.ErrNotFound when the
// user does not exist (any more).
func (r *UserRepository) SoftDelete(ctx context.Context, id, deletedBy string) error {
	query := `
		UPDATE core.users
		SET deleted_at = $2, deleted_by = $3, updated_at = $2, updated_by = $3
		WHERE id = $1 AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, time.Now(), deletedBy)
	if err != nil {
		logger.Error("Failed to soft delete user", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// ChangePassword replaces the password whose hash was checkedHash with
// passwordHash: the owner chose it knowing the current one, checked as
// password attempt number attempt. It ends the run of wrong passwords
// like ClearPasswordAttempts. It reports false, changing nothing, when
// another attempt has locked the account since, the password was changed
// or reset meanwhile, or the account is inactive or gone.
func (r *UserRepository) ChangePassword(ctx context.Context, id, checkedHash, passwordHash string, attempt int, at time.Time) (bool, error) {
	query := `
		UPDATE core.users
		SET password_hash = $2, failed_login_count = 0, locked_until = NULL,
		    updated_at = $3, updated_by = $1
		WHERE id = $1 AND is_active AND deleted_at IS NULL AND password_hash = $5
		  AND (locked_until IS NULL OR locked_until <= $3 OR failed_login_count = $4)
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, passwordHash, at, attempt, checkedHash)
	if err != nil {
		logger.Error("Failed to change password", logger.Err(err))
		return false, err
	}

	return tag.RowsAffected() == 1, nil
}

// ChangeEmail moves an active account from email to newEmail and marks
// newEmail verified: its owner followed a link sent to it. It returns
// database.ErrNotFound when the account's email is no longer email, and
// ErrEmailTaken when another account uses newEmail by now.
func (r *UserRepository) ChangeEmail(ctx context.Context, id, email, newEmail string, at time.Time) error {
	query := `
		UPDATE core.users
		SET email = $3, is_email_verified = TRUE, email_verified_at = $4,
		    updated_at = $4, updated_by = $1
		WHERE id = $1 AND email = $2 AND is_active AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, email, newEmail, at)
	if err != nil {
		if err := translateUnique(err); errors.Is(err, ErrEmailTaken) {
			return err
		}
		logger.Error("Failed to change email", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// Anonymize closes an account at its owner's request: the row stays, since
// other records refer to it, but everything about the person is erased —
// email and username become placeholders derived from the id, and the
// name, phone, avatar, password and login history are cleared. The account
// is soft deleted and every access token it issued is refused. Returns
// database.ErrNotFound when the account is gone already.
func (r *UserRepository) Anonymize(ctx context.Context, id string, at time.Time) error {
	query := `
		UPDATE core.users
		SET email = id::text || '@closed.invalid',
		    username = 'closed-' || replace(id::text, '-', ''),
		    password_hash = NULL, full_name = NULL, phone = NULL, avatar_url = NULL,
		    timezone = DEFAULT,
		    is_active = FALSE, is_email_verified = FALSE, email_verified_at = NULL,
		    failed_login_count = 0, locked_until = NULL, last_login_at = NULL,
		    tokens_valid_after = $2,
		    updated_at = $2, updated_by = $1, deleted_at = $2, deleted_by = $1
		WHERE id = $1 AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, at.Truncate(time.Millisecond))
	if err != nil {
		logger.Error("Failed to anonymize user", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// VerifyEmail marks the user's email verified, provided it is still
// email (the address a verification link was sent to) and the account is
// active. It reports whether the email was unverified until now.
func (r *UserRepository) VerifyEmail(ctx context.Context, id, email string, at time.Time) (newlyVerified bool, err error) {
	query := `
		UPDATE core.users AS u
		SET is_email_verified = TRUE,
		    email_verified_at = COALESCE(u.email_verified_at, $3)
		FROM (SELECT is_email_verified FROM core.users WHERE id = $1) AS before
		WHERE u.id = $1 AND u.email = $2 AND u.is_active AND u.deleted_at IS NULL
		RETURNING NOT before.is_email_verified
	`

	err = r.q(ctx).QueryRow(ctx, query, id, email, at).Scan(&newlyVerified)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, database.ErrNotFound
		}
		logger.Error("Failed to verify email", logger.Err(err))
		return false, err
	}

	return newlyVerified, nil
}

// ResetPassword sets a new password after the owner proved control of the
// mailbox email: that also verifies the email and lifts any sign-in lock.
// Returns database.ErrNotFound when the account's email is no longer
// email (the link went to an address the account has left), or it is
// inactive or gone.
func (r *UserRepository) ResetPassword(ctx context.Context, id, email, passwordHash string, at time.Time) error {
	query := `
		UPDATE core.users
		SET password_hash = $2,
		    is_email_verified = TRUE,
		    email_verified_at = COALESCE(email_verified_at, $3),
		    failed_login_count = 0, locked_until = NULL,
		    updated_at = $3, updated_by = $1
		WHERE id = $1 AND email = $4 AND is_active AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, passwordHash, at, email)
	if err != nil {
		logger.Error("Failed to reset password", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// ClaimUnverified hands an unverified account to whoever just proved they
// own its email through an external provider: the password set by the
// registrant is removed and the email marked verified. It reports false
// when the account is verified (or gone) by now, and leaves it untouched.
func (r *UserRepository) ClaimUnverified(ctx context.Context, id string, at time.Time) (bool, error) {
	query := `
		UPDATE core.users
		SET password_hash = NULL,
		    is_email_verified = TRUE, email_verified_at = $2,
		    failed_login_count = 0, locked_until = NULL,
		    updated_at = $2
		WHERE id = $1 AND NOT is_email_verified AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, at)
	if err != nil {
		logger.Error("Failed to claim account", logger.Err(err))
		return false, err
	}

	return tag.RowsAffected() == 1, nil
}

// RevokeTokensIssuedBefore makes every access token issued before at
// invalid for this user (see authz.Subject.TokensValidAfter). The value
// is kept at milliseconds, the precision of a token's issue time.
func (r *UserRepository) RevokeTokensIssuedBefore(ctx context.Context, id string, at time.Time) error {
	query := `UPDATE core.users SET tokens_valid_after = $2 WHERE id = $1`

	if _, err := r.q(ctx).Exec(ctx, query, id, at.Truncate(time.Millisecond)); err != nil {
		logger.Error("Failed to revoke access tokens", logger.Err(err))
		return err
	}

	return nil
}

// ReservePasswordAttempt counts a password check before it is made, so
// that guesses sent in parallel cannot all be checked before a lock lands.
// It returns the attempt's number in the current run of wrong passwords,
// or 0 when the account is locked (or gone) and no check may be made. The
// attempt that reaches maxAttempts locks the account until lockUntil at
// once; if its password turns out right, ClearPasswordAttempts lifts the
// lock again. A lock that has expired starts a new run.
func (r *UserRepository) ReservePasswordAttempt(ctx context.Context, id string, maxAttempts int, now, lockUntil time.Time) (int, error) {
	// SET reads the row as it was, re-read after waiting for a concurrent
	// reservation: the attempts of a burst are numbered one by one.
	query := `
		UPDATE core.users
		SET failed_login_count = CASE WHEN locked_until IS NULL THEN failed_login_count + 1 ELSE 1 END,
		    locked_until = CASE
		        WHEN (CASE WHEN locked_until IS NULL THEN failed_login_count + 1 ELSE 1 END) >= $3 THEN $4::timestamptz
		    END
		WHERE id = $1 AND deleted_at IS NULL
		  AND (locked_until IS NULL OR locked_until <= $2)
		RETURNING failed_login_count
	`

	var attempt int
	err := r.q(ctx).QueryRow(ctx, query, id, now, maxAttempts, lockUntil).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		logger.Error("Failed to reserve password attempt", logger.Err(err))
		return 0, err
	}

	return attempt, nil
}

// ClearPasswordAttempts ends the run of wrong passwords after password
// attempt number attempt was right, checked against the hash checkedHash.
// It reports false when another attempt has locked the account since, or
// the password was changed or reset since it was read: then the right
// password must lose too. A lock whose count is still attempt is the one
// this attempt set by reaching the limit, and is lifted.
//
// It locks the row (see FindByIDForUpdate): run in the transaction that
// issues the session, a password reset or claim cannot slip in between.
func (r *UserRepository) ClearPasswordAttempts(ctx context.Context, id, checkedHash string, attempt int) (bool, error) {
	query := `
		UPDATE core.users
		SET failed_login_count = 0, locked_until = NULL
		WHERE id = $1 AND deleted_at IS NULL AND password_hash = $4
		  AND (locked_until IS NULL OR locked_until <= $2 OR failed_login_count = $3)
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, time.Now(), attempt, checkedHash)
	if err != nil {
		logger.Error("Failed to clear password attempts", logger.Err(err))
		return false, err
	}

	return tag.RowsAffected() == 1, nil
}

// TouchLastLogin stamps a sign-in that did not use the password (e.g.
// Google). It leaves the failure count and any lock alone: those guard
// the password.
func (r *UserRepository) TouchLastLogin(ctx context.Context, id string) error {
	query := `UPDATE core.users SET last_login_at = $2 WHERE id = $1 AND deleted_at IS NULL`

	if _, err := r.q(ctx).Exec(ctx, query, id, time.Now()); err != nil {
		logger.Error("Failed to update last login", logger.Err(err))
		return err
	}

	return nil
}

// UsernameExists reports whether a live account uses username. Only for
// picking a free username; writes rely on ErrUsernameTaken instead.
func (r *UserRepository) UsernameExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM core.users WHERE username = $1 AND deleted_at IS NULL)`,
		username,
	).Scan(&exists)
	if err != nil {
		logger.Error("Failed to check username existence", logger.Err(err))
		return false, err
	}

	return exists, nil
}
