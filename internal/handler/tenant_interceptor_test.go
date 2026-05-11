package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// callInterceptor — helper: прогон unary interceptor с заданными metadata.
func callInterceptor(t *testing.T, productionMode bool, requireAdmin bool, fullMethod string, md metadata.MD) error {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), md)
	interceptor := TenantUnaryInterceptor(requireAdmin, productionMode)
	noopHandler := func(ctx context.Context, req any) (any, error) { return nil, nil }
	info := &grpc.UnaryServerInfo{FullMethod: fullMethod}
	_, err := interceptor(ctx, struct{}{}, info, noopHandler)
	return err
}

// TestTenantUnary_AnonymousDevPasses — dev-mode пропускает anonymous (backward-compat).
func TestTenantUnary_AnonymousDevPasses(t *testing.T) {
	if err := callInterceptor(t, false, false, "/svc/M", metadata.MD{}); err != nil {
		t.Fatalf("dev-mode anonymous должен пройти, got: %v", err)
	}
}

// TestTenantUnary_AnonymousProductionRejected — production-mode anonymous → PermissionDenied.
func TestTenantUnary_AnonymousProductionRejected(t *testing.T) {
	err := callInterceptor(t, true, false, "/svc/M", metadata.MD{})
	if err == nil {
		t.Fatal("production-mode anonymous должен быть отвергнут")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ожидался PermissionDenied, got: %v", err)
	}
}

// TestTenantUnary_ActorOnlyProductionRejected — это ключевой R9 critical:
// caller с x-kacho-actor: <anything> без folder/admin не должен бypass'ить
// fail-closed гейт (R10 IsAnonymous fix: Actor — audit-only, не AuthN).
func TestTenantUnary_ActorOnlyProductionRejected(t *testing.T) {
	md := metadata.MD{"x-kacho-actor": []string{"evil@attacker"}}
	err := callInterceptor(t, true, false, "/svc/M", md)
	if err == nil {
		t.Fatal("R9 CRITICAL: actor-only metadata НЕ должен пропускать в production-mode")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ожидался PermissionDenied, got: %v", err)
	}
}

// TestTenantUnary_FolderProductionPasses — caller с folder claim → пропускается.
func TestTenantUnary_FolderProductionPasses(t *testing.T) {
	md := metadata.MD{"x-kacho-folder-id": []string{"f1"}}
	if err := callInterceptor(t, true, false, "/svc/M", md); err != nil {
		t.Fatalf("folder-claim caller должен пройти в production, got: %v", err)
	}
}

// TestTenantUnary_AdminProductionPasses — admin claim → пропускается.
func TestTenantUnary_AdminProductionPasses(t *testing.T) {
	md := metadata.MD{"x-kacho-admin": []string{"true"}}
	if err := callInterceptor(t, true, false, "/svc/M", md); err != nil {
		t.Fatalf("admin caller должен пройти в production, got: %v", err)
	}
}

// TestTenantUnary_RequireAdminInternalNonAdminRejected — :9091 без admin → PermissionDenied.
func TestTenantUnary_RequireAdminInternalNonAdminRejected(t *testing.T) {
	md := metadata.MD{"x-kacho-folder-id": []string{"f1"}}
	err := callInterceptor(t, false, true, "/kacho.cloud.vpc.v1.InternalCloudService/Foo", md)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ожидался PermissionDenied для non-admin на /Internal*, got: %v", err)
	}
}

// TestTenantUnary_RequireAdminNonInternalNotFound — :9091 + non-/Internal path → NotFound (no service-tree fingerprint).
func TestTenantUnary_RequireAdminNonInternalNotFound(t *testing.T) {
	md := metadata.MD{"x-kacho-folder-id": []string{"f1"}}
	err := callInterceptor(t, false, true, "/kacho.cloud.vpc.v1.NetworkService/Get", md)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("ожидался NotFound для non-/Internal на admin-listener, got: %v", err)
	}
}

// TestHasFolderAccess_DevAnonymousPasses — empty FolderIDs (dev-mode tenantCtx) даёт full access.
func TestHasFolderAccess_DevAnonymousPasses(t *testing.T) {
	tc := TenantCtx{}
	if !tc.HasFolderAccess("any") {
		t.Fatal("dev-mode anonymous (empty FolderIDs) должен давать full access")
	}
}

// TestHasFolderAccess_FolderMatch — caller'у разрешён только свой folder.
func TestHasFolderAccess_FolderMatch(t *testing.T) {
	tc := TenantCtx{FolderIDs: map[string]struct{}{"f1": {}}}
	if !tc.HasFolderAccess("f1") {
		t.Fatal("свой folder должен пропускаться")
	}
	if tc.HasFolderAccess("f2") {
		t.Fatal("чужой folder должен быть запрещён")
	}
}

// TestHasFolderAccess_AdminAlwaysPasses — admin минует folder check.
func TestHasFolderAccess_AdminAlwaysPasses(t *testing.T) {
	tc := TenantCtx{Admin: true}
	if !tc.HasFolderAccess("any") {
		t.Fatal("admin должен иметь access ко всем folders")
	}
}

// TestIsAnonymous_R9CriticalFix — R10 семантика: Actor — audit-only.
func TestIsAnonymous_R9CriticalFix(t *testing.T) {
	cases := []struct {
		name string
		tc   TenantCtx
		want bool
	}{
		{"empty", TenantCtx{}, true},
		{"actor-only", TenantCtx{Actor: "alice"}, true}, // R10 fix: actor-only = anonymous
		{"folder", TenantCtx{FolderIDs: map[string]struct{}{"f1": {}}}, false},
		{"admin", TenantCtx{Admin: true}, false},
		{"admin+actor", TenantCtx{Actor: "x", Admin: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tc.IsAnonymous(); got != c.want {
				t.Fatalf("IsAnonymous=%v, want %v (case %s)", got, c.want, c.name)
			}
		})
	}
}

// TestAssertFolderOwnership_RejectsCrossTenant — handler-side AuthZ check.
func TestAssertFolderOwnership_RejectsCrossTenant(t *testing.T) {
	ctx := context.WithValue(context.Background(), tenantCtxKey{},
		TenantCtx{FolderIDs: map[string]struct{}{"f1": {}}})
	if err := AssertFolderOwnership(ctx, "f1"); err != nil {
		t.Fatalf("свой folder: %v", err)
	}
	err := AssertFolderOwnership(ctx, "f2")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("чужой folder должен дать PermissionDenied, got: %v", err)
	}
}
