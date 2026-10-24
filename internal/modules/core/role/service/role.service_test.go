package service

import (
	"errors"
	"testing"

	"timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/shared/authz"
)

func TestCheckSuperAdminRole(t *testing.T) {
	superAdmin := &domain.Role{Code: "super_admin", IsSystem: true}
	administrator := &domain.Role{Code: "administrator", IsSystem: true}

	tests := []struct {
		name                         string
		role                         *domain.Role
		setsPermissions, deactivates bool
		want                         error
	}{
		{"rename super_admin", superAdmin, false, false, nil},
		{"deactivate super_admin", superAdmin, false, true, ErrSuperAdminRoleLocked},
		{"give super_admin permissions", superAdmin, true, false, ErrSuperAdminRoleLocked},
		{"deactivate administrator", administrator, false, true, nil},
		{"edit administrator permissions", administrator, true, false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkSuperAdminRole(tt.role, tt.setsPermissions, tt.deactivates); !errors.Is(err, tt.want) {
				t.Errorf("checkSuperAdminRole() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidatePermissions(t *testing.T) {
	tests := []struct {
		name  string
		perms map[string]string
		ok    bool
	}{
		{"registered resources and levels", map[string]string{"users": "editor", "roles": "viewer"}, true},
		{"empty map", map[string]string{}, true},
		{"nil", nil, false},
		{"unknown resource (typo)", map[string]string{"user": "admin"}, false},
		{"unknown level", map[string]string{"users": "owner"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePermissions(tt.perms)
			if (err == nil) != tt.ok {
				t.Fatalf("validatePermissions() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidPermissions) {
				t.Errorf("error %v does not wrap ErrInvalidPermissions", err)
			}
		})
	}
}

func TestCheckManageable(t *testing.T) {
	usersEditor := authz.Subject{Active: true, Permissions: domain.Permissions{"users": "editor", "roles": "admin"}.ToStringList()}
	system := &domain.Role{Code: "administrator", IsSystem: true}
	broader := &domain.Role{Code: "user_admin", Permissions: domain.Permissions{"users": "admin"}}
	narrower := &domain.Role{Code: "user_viewer", Permissions: domain.Permissions{"users": "viewer"}}

	tests := []struct {
		name   string
		role   *domain.Role
		caller authz.Subject
		want   error
	}{
		{"system role, not a super admin", system, usersEditor, ErrCannotModifySystem},
		{"system role, super admin", system, authz.Subject{Active: true, SuperAdmin: true}, nil},
		{"role granting more than the caller holds", broader, usersEditor, ErrExceedsCaller},
		{"role within the caller's permissions", narrower, usersEditor, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkManageable(tt.role, tt.caller); !errors.Is(err, tt.want) {
				t.Errorf("checkManageable() = %v, want %v", err, tt.want)
			}
		})
	}
}
