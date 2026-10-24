package authz

import (
	"testing"
	"time"
)

func TestSubject_AcceptsTokenIssuedAt(t *testing.T) {
	revokedAt := time.Date(2026, 10, 23, 12, 0, 30, 500_000_000, time.UTC)
	subject := Subject{Active: true, TokensValidAfter: revokedAt.UnixMilli()}

	if subject.AcceptsTokenIssuedAt(revokedAt.Add(-100 * time.Millisecond)) {
		t.Error("accepted a token issued earlier in the same second as the revocation")
	}
	if subject.AcceptsTokenIssuedAt(revokedAt.Add(-2 * time.Millisecond)) {
		t.Error("accepted a token issued 2 ms before the revocation")
	}
	if !subject.AcceptsTokenIssuedAt(revokedAt.Add(-time.Millisecond)) {
		t.Error("rejected a token whose issue time read back 1 ms early")
	}
	if !subject.AcceptsTokenIssuedAt(revokedAt) {
		t.Error("rejected a token issued at the revocation instant")
	}
	if !subject.AcceptsTokenIssuedAt(revokedAt.Add(time.Millisecond)) {
		t.Error("rejected a token issued after the revocation")
	}
	if !(Subject{Active: true}).AcceptsTokenIssuedAt(time.Unix(1, 0)) {
		t.Error("a subject that never revoked its sessions rejected a token")
	}
}

func TestSplitPermission(t *testing.T) {
	tests := []struct {
		perm, resource, action string
		ok                     bool
	}{
		{"users:read", "users", "read", true},
		{"users", "users", "", false},
		{":read", "", "read", false},
		{"users:", "users", "", false},
	}
	for _, tt := range tests {
		resource, action, ok := SplitPermission(tt.perm)
		if resource != tt.resource || action != tt.action || ok != tt.ok {
			t.Errorf("SplitPermission(%q) = %q, %q, %v; want %q, %q, %v",
				tt.perm, resource, action, ok, tt.resource, tt.action, tt.ok)
		}
	}
}

func TestResources(t *testing.T) {
	if !IsResource(ResourceUsers) || !IsResource(ResourceRoles) {
		t.Error("a registered resource is not recognised")
	}
	if IsResource("user-management") {
		t.Error("the old resource name is still accepted")
	}
	list := Resources()
	list[0].Code = "changed"
	if Resources()[0].Code == "changed" {
		t.Error("Resources() exposes the registry for modification")
	}
}

func TestSubject_Has(t *testing.T) {
	tests := []struct {
		name    string
		subject Subject
		perm    string
		want    bool
	}{
		{"held permission", Subject{Active: true, Permissions: []string{"roles:read"}}, "roles:read", true},
		{"missing permission", Subject{Active: true, Permissions: []string{"roles:read"}}, "roles:update", false},
		{"super admin holds everything", Subject{Active: true, SuperAdmin: true}, "roles:update", true},
		{"inactive user holds nothing", Subject{Active: false, Permissions: []string{"roles:read"}}, "roles:read", false},
		{"inactive super admin holds nothing", Subject{Active: false, SuperAdmin: true}, "roles:read", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.subject.Has(tt.perm); got != tt.want {
				t.Errorf("Has(%q) = %v, want %v", tt.perm, got, tt.want)
			}
		})
	}
}

func TestSubject_Covers(t *testing.T) {
	editor := Subject{Active: true, Permissions: []string{"users:read", "users:update"}}

	tests := []struct {
		name    string
		subject Subject
		perms   []string
		want    bool
	}{
		{"subset", editor, []string{"users:read"}, true},
		{"equal set", editor, []string{"users:read", "users:update"}, true},
		{"empty set", editor, nil, true},
		{"one permission beyond the caller", editor, []string{"users:read", "roles:create"}, false},
		{"super admin covers anything", Subject{Active: true, SuperAdmin: true}, []string{"roles:create"}, true},
		{"inactive covers nothing", Subject{Active: false, SuperAdmin: true}, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.subject.Covers(tt.perms); got != tt.want {
				t.Errorf("Covers(%v) = %v, want %v", tt.perms, got, tt.want)
			}
		})
	}
}
