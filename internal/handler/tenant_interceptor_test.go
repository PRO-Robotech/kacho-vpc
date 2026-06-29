// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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

// TestTenantUnary_ActorOnlyProductionRejected — caller с x-kacho-actor без
// project/admin не должен обходить fail-closed гейт (Actor — audit-only, не AuthN).
func TestTenantUnary_ActorOnlyProductionRejected(t *testing.T) {
	md := metadata.MD{"x-kacho-actor": []string{"evil@attacker"}}
	err := callInterceptor(t, true, false, "/svc/M", md)
	if err == nil {
		t.Fatal("actor-only metadata не должен проходить fail-closed в production-mode (Actor — audit-only, не AuthN)")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ожидался PermissionDenied, got: %v", err)
	}
}

// TestTenantUnary_ProjectProductionPasses — caller с project claim → пропускается.
func TestTenantUnary_ProjectProductionPasses(t *testing.T) {
	md := metadata.MD{"x-kacho-project-id": []string{"f1"}}
	if err := callInterceptor(t, true, false, "/svc/M", md); err != nil {
		t.Fatalf("project-claim caller должен пройти в production, got: %v", err)
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
	md := metadata.MD{"x-kacho-project-id": []string{"f1"}}
	err := callInterceptor(t, false, true, "/kacho.cloud.vpc.v1.InternalNetworkService/Foo", md)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ожидался PermissionDenied для non-admin на /Internal*, got: %v", err)
	}
}

// TestTenantUnary_RequireAdminNonInternalNotFound — :9091 + non-/Internal path → NotFound (no service-tree fingerprint).
func TestTenantUnary_RequireAdminNonInternalNotFound(t *testing.T) {
	md := metadata.MD{"x-kacho-project-id": []string{"f1"}}
	err := callInterceptor(t, false, true, "/kacho.cloud.vpc.v1.NetworkService/Get", md)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("ожидался NotFound для non-/Internal на admin-listener, got: %v", err)
	}
}
