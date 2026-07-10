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

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// mockServerStream — минимальный grpc.ServerStream с настраиваемым ctx для
// прогона stream-interceptor'а в тестах.
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context { return m.ctx }

// TestTenantUnary_PrincipalProductionPasses — regression против fe3455 production-auth
// бага: api-gateway форвардит identity как x-kacho-principal-* (operations.WithPrincipal),
// а НЕ legacy x-kacho-project-id. Запрос с реальным forwarded-принципалом НЕ должен
// считаться anonymous в production — он проходит tenant-guard дальше, к per-object
// authz-интерсептору (реальный гейт). До фикса guard отвергал его (учитывался только
// x-kacho-project-id), и аутентифицированный+авторизованный юзер получал 403.
func TestTenantUnary_PrincipalProductionPasses(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	ctx = operations.WithPrincipal(ctx, operations.Principal{Type: "user", ID: "usr7j2yp1v24tx90tcv7"})
	interceptor := TenantUnaryInterceptor(false, true) // public listener, production
	called := false
	h := func(ctx context.Context, req any) (any, error) { called = true; return nil, nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/List"}
	if _, err := interceptor(ctx, struct{}{}, info, h); err != nil {
		t.Fatalf("production-запрос с forwarded-принципалом обязан пройти tenant-guard, got: %v", err)
	}
	if !called {
		t.Fatal("downstream handler не был вызван")
	}
}

// TestTenantStream_PrincipalProductionPasses — то же для server-stream RPC.
func TestTenantStream_PrincipalProductionPasses(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	ctx = operations.WithPrincipal(ctx, operations.Principal{Type: "service_account", ID: "sa9kx2"})
	interceptor := TenantStreamInterceptor(false, true)
	called := false
	h := func(srv any, ss grpc.ServerStream) error { called = true; return nil }
	info := &grpc.StreamServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/List"}
	if err := interceptor(nil, &mockServerStream{ctx: ctx}, info, h); err != nil {
		t.Fatalf("production stream-запрос с forwarded-принципалом обязан пройти, got: %v", err)
	}
	if !called {
		t.Fatal("downstream stream-handler не был вызван")
	}
}

// TestTenantUnary_TrulyAnonymousProductionStillRejected — negative: без principal И
// без x-kacho-project-id (ctx-fallback → system:bootstrap) production по-прежнему
// fail-closed. Локает, что фикс не открыл дыру для настоящего anonymous.
func TestTenantUnary_TrulyAnonymousProductionStillRejected(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	interceptor := TenantUnaryInterceptor(false, true)
	h := func(ctx context.Context, req any) (any, error) { return nil, nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/List"}
	_, err := interceptor(ctx, struct{}{}, info, h)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("настоящий anonymous в production обязан быть отвергнут, got: %v", err)
	}
}

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

// runInterceptorCapture — прогон unary interceptor, возвращает TenantCtx,
// доставшийся downstream-хендлеру (zero, если interceptor отверг запрос).
func runInterceptorCapture(t *testing.T, productionMode, requireAdmin bool, fullMethod string, md metadata.MD) (tenant.TenantCtx, error) {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), md)
	interceptor := TenantUnaryInterceptor(requireAdmin, productionMode)
	var captured tenant.TenantCtx
	h := func(ctx context.Context, req any) (any, error) {
		captured = tenant.TenantFromCtx(ctx)
		return nil, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: fullMethod}
	_, err := interceptor(ctx, struct{}{}, info, h)
	return captured, err
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

// TestTenantUnary_NonIdentityHeaderProductionRejected — caller, предъявивший
// произвольный НЕ-identity header (без x-kacho-project-id / x-kacho-admin), не
// должен обходить fail-closed гейт: такой header не несет authz-claims →
// caller остается anonymous → PermissionDenied.
func TestTenantUnary_NonIdentityHeaderProductionRejected(t *testing.T) {
	md := metadata.MD{"x-some-header": []string{"evil@attacker"}}
	err := callInterceptor(t, true, false, "/svc/M", md)
	if err == nil {
		t.Fatal("non-identity metadata не должен проходить fail-closed в production-mode")
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

// TestTenantUnary_AdminHeaderNotHonoredOnPublic — на public-listener'е
// (requireAdmin=false) client-supplied x-kacho-admin НЕ должен давать
// cluster-admin: t.Admin остается false, поэтому AssertProjectOwnership не
// обходится подделанным заголовком (SEC-low hardening). Admin-only header в
// production → без реального project-claim'а caller anonymous → PermissionDenied.
func TestTenantUnary_AdminHeaderNotHonoredOnPublic(t *testing.T) {
	// dev-mode: admin header игнорируется на public, t.Admin=false.
	captured, err := runInterceptorCapture(t, false, false, "/svc/M",
		metadata.MD{"x-kacho-admin": []string{"true"}})
	if err != nil {
		t.Fatalf("dev public admin-header: unexpected err %v", err)
	}
	if captured.Admin {
		t.Fatal("public listener не должен доверять client x-kacho-admin (t.Admin=true) — обход AssertProjectOwnership")
	}
	// production-mode: admin-only header не авторизует → anonymous → reject.
	_, perr := runInterceptorCapture(t, true, false, "/svc/M",
		metadata.MD{"x-kacho-admin": []string{"true"}})
	if status.Code(perr) != codes.PermissionDenied {
		t.Fatalf("public admin-only header в production должен быть отвергнут, got %v", perr)
	}
}

// TestTenantUnary_AdminHeaderHonoredOnInternal — на :9091 (requireAdmin=true)
// x-kacho-admin по-прежнему честен: t.Admin=true, admin-gate пропускает.
func TestTenantUnary_AdminHeaderHonoredOnInternal(t *testing.T) {
	captured, err := runInterceptorCapture(t, true, true,
		"/kacho.cloud.vpc.v1.InternalNetworkService/Foo",
		metadata.MD{"x-kacho-admin": []string{"true"}})
	if err != nil {
		t.Fatalf("internal admin caller должен пройти, got %v", err)
	}
	if !captured.Admin {
		t.Fatal("internal listener должен доверять x-kacho-admin (t.Admin=true)")
	}
}

// TestTenantUnary_PublicProjectClaimStillHonored — hardening не ломает
// легитимного tenant'а: project-claim на public по-прежнему авторизует.
func TestTenantUnary_PublicProjectClaimStillHonored(t *testing.T) {
	captured, err := runInterceptorCapture(t, true, false, "/svc/M",
		metadata.MD{"x-kacho-project-id": []string{"f1"}})
	if err != nil {
		t.Fatalf("public project-claim caller должен пройти, got %v", err)
	}
	if captured.Admin {
		t.Fatal("project-claim не должен давать Admin")
	}
	if !captured.HasProjectAccess("f1") || captured.HasProjectAccess("f2") {
		t.Fatal("project-scope нарушен: должен видеть f1, не f2")
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

// TestTenantUnary_RequireAdminObjectScopedInternalPasses — object-scoped
// internal RPC (InternalAddressService.AllocateInternalIP, per
// permission_map.go — v_update на vpc_address:<id>, зеркало публичного
// AddressService.Update) не должен быть заблокирован blanket admin-gate'ом:
// non-admin caller (nlb->vpc IPAM edge форвардит только
// x-kacho-principal-*, БЕЗ x-kacho-admin) обязан дойти до object-scoped
// authz-Check'а (authzIntr), а не получить PermissionDenied на tenant-gate
// раньше, чем FGA вообще успеет посмотреть на объект.
func TestTenantUnary_RequireAdminObjectScopedInternalPasses(t *testing.T) {
	md := metadata.MD{"x-kacho-project-id": []string{"f1"}}
	err := callInterceptor(t, false, true,
		"/kacho.cloud.vpc.v1.InternalAddressService/AllocateInternalIP", md)
	if err != nil {
		t.Fatalf("non-admin caller на object-scoped internal RPC должен пройти tenant-gate (authz-Check решает per-object v_update), got: %v", err)
	}
}

// TestTenantUnary_RequireAdminClusterScopedInternalStillRejected — admin-only
// cluster-scoped internal RPC (InternalAddressPoolService.Create,
// system_admin@cluster) остается admin-gated на tenant-interceptor'е:
// non-admin caller получает PermissionDenied ДО authz-Check'а (нет смысла
// звать FGA для RPC, которого ни один non-admin принципал не должен видеть).
func TestTenantUnary_RequireAdminClusterScopedInternalStillRejected(t *testing.T) {
	md := metadata.MD{"x-kacho-project-id": []string{"f1"}}
	err := callInterceptor(t, false, true,
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/Create", md)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ожидался PermissionDenied для non-admin на admin-only cluster-scoped internal RPC, got: %v", err)
	}
}
