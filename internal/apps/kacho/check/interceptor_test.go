package check_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// principalCtx — helper для test-fixture'ов.
func principalCtx(typ, id string) context.Context {
	return operations.WithPrincipal(context.Background(), operations.Principal{
		Type:        typ,
		ID:          id,
		DisplayName: "test",
	})
}

// newTestInterceptor — фабрика interceptor'а с подменным CheckClient'ом.
// Возвращает interceptor + указатель на counter "сколько раз вызывался Check"
// — для верификации cache-hit / call-count.
func newTestInterceptor(t *testing.T, fn func(ctx context.Context, subject, relation, object string) (bool, error)) (*authz.Interceptor, *int) {
	t.Helper()
	calls := 0
	wrapped := authz.CheckClientFunc(func(ctx context.Context, subject, relation, object string) (bool, error) {
		calls++
		return fn(ctx, subject, relation, object)
	})
	intr := authz.NewInterceptor(authz.InterceptorOptions{
		ServiceName: "kacho-vpc-test",
		Map:         check.PermissionMap(),
		Client:      wrapped,
	})
	return intr, &calls
}

// TestInterceptor_Unary_Allow_NetworkCreate — happy-path: Check вернул allowed=true
// → handler выполняется.
func TestInterceptor_Unary_Allow_NetworkCreate(t *testing.T) {
	intr, calls := newTestInterceptor(t, func(_ context.Context, subject, relation, object string) (bool, error) {
		require.Equal(t, "user:usr_alice", subject)
		require.Equal(t, "editor", relation)
		require.Equal(t, "project:prj_demo", object)
		return true, nil
	})
	uIntr := intr.Unary()

	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Create"}
	ctx := principalCtx("user", "usr_alice")
	req := &vpcv1.CreateNetworkRequest{ProjectId: "prj_demo", Name: "n1"}

	resp, err := uIntr(ctx, req, info, handler)
	require.NoError(t, err)
	require.Equal(t, "ok", resp)
	require.True(t, called, "handler must be invoked when authorized")
	require.Equal(t, 1, *calls)
}

// TestInterceptor_Unary_Deny_NetworkDelete — Check вернул allowed=false →
// PermissionDenied, handler НЕ вызывается.
func TestInterceptor_Unary_Deny_NetworkDelete(t *testing.T) {
	intr, calls := newTestInterceptor(t, func(_ context.Context, subject, relation, object string) (bool, error) {
		require.Equal(t, "user:usr_bob", subject)
		require.Equal(t, "editor", relation)
		require.Equal(t, "vpc_network:enp_xxx", object)
		return false, nil
	})
	uIntr := intr.Unary()

	handlerCalled := false
	handler := func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		return "should not be returned", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Delete"}
	ctx := principalCtx("user", "usr_bob")
	req := &vpcv1.DeleteNetworkRequest{NetworkId: "enp_xxx"}

	_, err := uIntr(ctx, req, info, handler)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.PermissionDenied, st.Code())
	require.False(t, handlerCalled, "handler must NOT be invoked on DENY")
	require.Equal(t, 1, *calls)
}

// TestInterceptor_Unary_Unavailable_FailClosed — Check вернул transport-error
// (например iam недоступен) → fail-closed PermissionDenied (acceptance D-6).
func TestInterceptor_Unary_Unavailable_FailClosed(t *testing.T) {
	intr, _ := newTestInterceptor(t, func(_ context.Context, subject, relation, object string) (bool, error) {
		return false, errors.New("iam unavailable: connection refused")
	})
	uIntr := intr.Unary()

	handler := func(ctx context.Context, req any) (any, error) {
		t.Fatal("handler must not be called on Unavailable")
		return nil, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Create"}
	ctx := principalCtx("user", "usr_alice")
	req := &vpcv1.CreateNetworkRequest{ProjectId: "prj_demo"}

	_, err := uIntr(ctx, req, info, handler)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.PermissionDenied, st.Code(), "Unavailable должен мапиться в PermissionDenied (fail-closed)")
}

// TestInterceptor_Unary_NoPrincipal_Denied — нет Principal'а в ctx → fail-closed.
func TestInterceptor_Unary_NoPrincipal_Denied(t *testing.T) {
	intr, calls := newTestInterceptor(t, func(_ context.Context, _ string, _ string, _ string) (bool, error) {
		t.Fatal("Check must not be called when principal is empty")
		return false, nil
	})
	uIntr := intr.Unary()

	handler := func(ctx context.Context, req any) (any, error) {
		t.Fatal("handler must not be called")
		return nil, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Get"}

	// Forge a Principal with empty ID — defaultSubjectExtractor returns ok=false.
	ctx := operations.WithPrincipal(context.Background(), operations.Principal{Type: "user", ID: ""})
	req := &vpcv1.GetNetworkRequest{NetworkId: "enp_x"}

	_, err := uIntr(ctx, req, info, handler)
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.PermissionDenied, st.Code())
	require.Equal(t, 0, *calls)
}

// TestInterceptor_Unary_UnmappedRPC_Denied — RPC не в PermissionMap → fail-closed.
//
// Это защита от drift'а: если в proto добавится новый RPC, но в permission_map.go
// его забудут — interceptor вернёт PermissionDenied (а не разрешит из-за «не нашёл»).
func TestInterceptor_Unary_UnmappedRPC_Denied(t *testing.T) {
	intr, _ := newTestInterceptor(t, func(_ context.Context, _, _, _ string) (bool, error) {
		t.Fatal("Check не должен вызываться для unmapped RPC")
		return false, nil
	})
	uIntr := intr.Unary()

	handler := func(ctx context.Context, req any) (any, error) {
		t.Fatal("handler не должен вызываться для unmapped RPC")
		return nil, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/SomeNewMethodWithoutMapping"}
	ctx := principalCtx("user", "usr_alice")

	_, err := uIntr(ctx, struct{}{}, info, handler)
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.PermissionDenied, st.Code())
}

// TestInterceptor_Unary_InternalRPC_Bypass — methodIsInternal в имени → пропуск.
// Internal-RPC'и слушают на :9091 (отдельный listener), public listener'а не
// касаются; защитный fallback бери ИМЯ.
func TestInterceptor_Unary_InternalRPC_Bypass(t *testing.T) {
	intr, calls := newTestInterceptor(t, func(_ context.Context, _, _, _ string) (bool, error) {
		t.Fatal("Check не должен вызываться для Internal* RPC")
		return false, nil
	})
	uIntr := intr.Unary()

	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.InternalNetworkService/SetDefaultSecurityGroupId"}
	ctx := principalCtx("user", "usr_alice")

	resp, err := uIntr(ctx, struct{}{}, info, handler)
	require.NoError(t, err)
	require.Equal(t, "ok", resp)
	require.True(t, called)
	require.Equal(t, 0, *calls, "Check не должен вызываться для Internal*")
}

// TestInterceptor_Unary_CacheHit — second Check на ту же (subject, relation,
// object) тройку — should hit positive-cache (TTL 5s); calls счётчик не растёт.
func TestInterceptor_Unary_CacheHit(t *testing.T) {
	intr, calls := newTestInterceptor(t, func(_ context.Context, _, _, _ string) (bool, error) {
		return true, nil
	})
	uIntr := intr.Unary()
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Get"}
	ctx := principalCtx("user", "usr_alice")
	req := &vpcv1.GetNetworkRequest{NetworkId: "enp_x"}

	// 1st call — Check invoked.
	_, err := uIntr(ctx, req, info, handler)
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	// 2nd call — должен взять из cache (calls остаётся 1).
	_, err = uIntr(ctx, req, info, handler)
	require.NoError(t, err)
	require.Equal(t, 1, *calls, "повторный Check на ту же (subject,relation,object) должен попасть в cache (TTL 5s)")
}

// TestInterceptor_Unary_Breakglass_AllowsAll — Breakglass=true пропускает всё
// без Check (acceptance D-6).
func TestInterceptor_Unary_Breakglass_AllowsAll(t *testing.T) {
	intr := authz.NewInterceptor(authz.InterceptorOptions{
		ServiceName: "kacho-vpc-test",
		Map:         check.PermissionMap(),
		Breakglass:  true,
	})
	uIntr := intr.Unary()
	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Delete"}
	ctx := principalCtx("user", "usr_bob")
	req := &vpcv1.DeleteNetworkRequest{NetworkId: "enp_x"}

	resp, err := uIntr(ctx, req, info, handler)
	require.NoError(t, err)
	require.Equal(t, "ok", resp)
	require.True(t, called)
}

// TestPermissionMap_CoverageSnapshot — drift-guard: должно быть >= 50 entries
// (9 services × ~5-10 методов + Operation). Если карта схлопнулась —
// видимо забыли регистрировать новый RPC.
func TestPermissionMap_CoverageSnapshot(t *testing.T) {
	m := check.PermissionMap()
	if len(m) < 50 {
		t.Errorf("PermissionMap слишком мала (%d entries): подозрение на drift регистраций", len(m))
	}
}

// TestFactory_NoIAMConn_NoBreakglass_Error — без IAMConn и без Breakglass
// фабрика возвращает ErrIAMConnNotConfigured.
func TestFactory_NoIAMConn_NoBreakglass_Error(t *testing.T) {
	_, err := check.NewInterceptor(check.Options{
		ServiceName: "kacho-vpc-test",
		IAMConn:     nil,
		Breakglass:  false,
	})
	require.ErrorIs(t, err, check.ErrIAMConnNotConfigured)
}

// TestFactory_Breakglass_NoIAMConn_OK — Breakglass=true позволяет nil-IAMConn.
func TestFactory_Breakglass_NoIAMConn_OK(t *testing.T) {
	intr, err := check.NewInterceptor(check.Options{
		ServiceName: "kacho-vpc-test",
		IAMConn:     nil,
		Breakglass:  true,
	})
	require.NoError(t, err)
	require.NotNil(t, intr)
}
