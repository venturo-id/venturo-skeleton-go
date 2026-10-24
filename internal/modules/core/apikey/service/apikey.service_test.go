package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"timebox-be/internal/shared/authz"
)

func TestCheckScopes(t *testing.T) {
	owner := authz.Subject{Active: true, Permissions: []string{"users:read", "roles:read"}}
	superAdmin := authz.Subject{Active: true, SuperAdmin: true}

	tests := []struct {
		name   string
		scopes []string
		owner  authz.Subject
		want   []string
	}{
		{"held permissions, duplicates dropped", []string{"users:read", "roles:read", "users:read"}, owner, []string{"users:read", "roles:read"}},
		{"permission the owner lacks", []string{"users:delete"}, owner, nil},
		{"unknown resource", []string{"invoices:read"}, superAdmin, nil},
		{"unknown action", []string{"users:approve"}, superAdmin, nil},
		{"not a permission code", []string{"users"}, superAdmin, nil},
		{"super admin may scope any real permission", []string{"roles:delete"}, superAdmin, []string{"roles:delete"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkScopes(tt.scopes, tt.owner)
			if tt.want == nil {
				if !errors.Is(err, ErrAPIKeyInvalidScope) {
					t.Fatalf("checkScopes() error = %v, want ErrAPIKeyInvalidScope", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkScopes() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checkScopes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseExpiry(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	empty := ""

	if got, err := parseExpiry(nil); got != nil || err != nil {
		t.Errorf("nil: got %v, %v; want no expiry", got, err)
	}
	if got, err := parseExpiry(&empty); got != nil || err != nil {
		t.Errorf("empty: got %v, %v; want no expiry", got, err)
	}
	if got, err := parseExpiry(&future); got == nil || err != nil {
		t.Errorf("future: got %v, %v; want a time", got, err)
	}
	if _, err := parseExpiry(&past); !errors.Is(err, ErrInvalidExpiry) {
		t.Errorf("past: err = %v, want ErrInvalidExpiry", err)
	}
}
