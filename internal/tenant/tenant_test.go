// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package tenant

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestHasProjectAccess_DevAnonymousPasses — empty ProjectIDs (dev-mode) дает full access.
func TestHasProjectAccess_DevAnonymousPasses(t *testing.T) {
	tc := TenantCtx{}
	if !tc.HasProjectAccess("any") {
		t.Fatal("dev-mode anonymous (empty ProjectIDs) должен давать full access")
	}
}

// TestHasProjectAccess_ProjectMatch — caller'у разрешен только свой project.
func TestHasProjectAccess_ProjectMatch(t *testing.T) {
	tc := TenantCtx{ProjectIDs: map[string]struct{}{"f1": {}}}
	if !tc.HasProjectAccess("f1") {
		t.Fatal("свой project должен пропускаться")
	}
	if tc.HasProjectAccess("f2") {
		t.Fatal("чужой project должен быть запрещен")
	}
}

// TestHasProjectAccess_AdminAlwaysPasses — admin минует project check.
func TestHasProjectAccess_AdminAlwaysPasses(t *testing.T) {
	tc := TenantCtx{Admin: true}
	if !tc.HasProjectAccess("any") {
		t.Fatal("admin должен иметь access ко всем projects")
	}
}

// TestIsAnonymous — anonymous = ни Admin, ни ProjectIDs.
func TestIsAnonymous(t *testing.T) {
	cases := []struct {
		name string
		tc   TenantCtx
		want bool
	}{
		{"empty", TenantCtx{}, true},
		{"project", TenantCtx{ProjectIDs: map[string]struct{}{"f1": {}}}, false},
		{"admin", TenantCtx{Admin: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tc.IsAnonymous(); got != c.want {
				t.Fatalf("IsAnonymous=%v, want %v (case %s)", got, c.want, c.name)
			}
		})
	}
}

// TestAssertProjectOwnership_RejectsCrossTenant — handler-side AuthZ check.
func TestAssertProjectOwnership_RejectsCrossTenant(t *testing.T) {
	ctx := WithTenant(context.Background(), TenantCtx{ProjectIDs: map[string]struct{}{"f1": {}}})
	if err := AssertProjectOwnership(ctx, "f1"); err != nil {
		t.Fatalf("свой project: %v", err)
	}
	err := AssertProjectOwnership(ctx, "f2")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("чужой project должен дать PermissionDenied, got: %v", err)
	}
}
