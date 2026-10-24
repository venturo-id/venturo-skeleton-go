// Command seed-admin creates the initial super admin from environment
// variables, so no credential has to live in a seeder file or in git.
//
//	SEED_ADMIN_EMAIL     login email — use an address you control
//	SEED_ADMIN_USERNAME  login username
//	SEED_ADMIN_PASSWORD  initial password; change it after the first sign-in
//
// The super_admin role is created by the migrations (make migrate-up).
// Running the command again is safe: an existing account keeps its
// password and only has the super_admin role ensured.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/config"
	"timebox-be/internal/database"
	roleRepo "timebox-be/internal/modules/core/role/repository"
	userDomain "timebox-be/internal/modules/core/user/domain"
	userRepo "timebox-be/internal/modules/core/user/repository"
	"timebox-be/pkg/crypto"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/validator"
)

func init() {
	// Same as the API: every time.Now() is UTC.
	time.Local = time.UTC
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	email := validator.NormalizeEmail(os.Getenv("SEED_ADMIN_EMAIL"))
	username := validator.NormalizeUsername(os.Getenv("SEED_ADMIN_USERNAME"))
	password := os.Getenv("SEED_ADMIN_PASSWORD")

	if email == "" || username == "" || password == "" {
		return errors.New("SEED_ADMIN_EMAIL, SEED_ADMIN_USERNAME and SEED_ADMIN_PASSWORD must all be set")
	}
	if err := validator.ValidateEmail(email); err != nil {
		return fmt.Errorf("SEED_ADMIN_EMAIL: %w", err)
	}
	if err := validator.ValidateUsername(username); err != nil {
		return fmt.Errorf("SEED_ADMIN_USERNAME: %w", err)
	}
	if err := validator.ValidatePasswordWithEmail(password, email); err != nil {
		return fmt.Errorf("SEED_ADMIN_PASSWORD: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := logger.Initialize(cfg.Server.Env); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}
	defer logger.Sync()

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()

	users := userRepo.NewUserRepository(db.Pool)
	roles := roleRepo.NewRoleRepository(db.Pool)

	superAdmin, err := roles.FindByCode(ctx, jwt.RoleSuperAdmin)
	if err != nil {
		return fmt.Errorf("look up %s role: %w", jwt.RoleSuperAdmin, err)
	}
	if superAdmin == nil {
		return fmt.Errorf("role %q not found — run the migrations first (make migrate-up)", jwt.RoleSuperAdmin)
	}

	existing, err := users.FindByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("look up user: %w", err)
	}

	var userID string
	if existing != nil {
		// A self-registered account has an unproven email: whoever signed
		// up first chose its password. Granting it super_admin would hand
		// the system to them.
		if !existing.IsEmailVerified {
			return fmt.Errorf("user %s already exists but its email is unverified (self-registered); "+
				"refusing to grant %s — delete that account first", email, jwt.RoleSuperAdmin)
		}
		if !existing.IsActive {
			return fmt.Errorf("user %s already exists but is inactive", email)
		}
		userID = existing.ID
	}

	err = database.WithTx(ctx, db.Pool, func(ctx context.Context) error {
		if existing == nil {
			passwordHash, err := crypto.HashPassword(password)
			if err != nil {
				return fmt.Errorf("hash password: %w", err)
			}

			now := time.Now()
			user := &userDomain.User{
				ID:           uuid.New().String(),
				Email:        email,
				Username:     username,
				PasswordHash: passwordHash,
				FullName:     &username,
				IsActive:     true,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			user.MarkEmailVerified(true, now)
			if err := users.Create(ctx, user); err != nil {
				return fmt.Errorf("create user: %w", err)
			}
			userID = user.ID
		}

		// created_by stays NULL: the assignment comes from the system, not a user.
		if _, err := database.Querier(ctx, db.Pool).Exec(ctx,
			`INSERT INTO core.user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT (user_id, role_id) DO NOTHING`,
			userID, superAdmin.ID,
		); err != nil {
			return fmt.Errorf("assign %s role: %w", jwt.RoleSuperAdmin, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if existing != nil {
		fmt.Printf("%s role ensured for existing user %s (%s); password left unchanged\n", jwt.RoleSuperAdmin, email, userID)
	} else {
		fmt.Printf("created %s %s (%s); change the password after the first sign-in\n", jwt.RoleSuperAdmin, email, userID)
	}
	return nil
}
