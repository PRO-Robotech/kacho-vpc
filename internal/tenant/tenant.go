// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package tenant — нейтральный (transport-free) носитель caller-identity и
// handler-side AuthZ-проверки. Это leaf-пакет: use-case-слой
// `internal/apps/kacho/api/*` зовет AssertProjectOwnership, не завися от
// транспорта; identity в ctx кладет `internal/handler` через WithTenant из
// своих gRPC-интерсепторов.
//
// Identity сейчас читается из gRPC metadata (без токенов). С приходом IAM
// источником станут claims из validated JWT, но downstream API (TenantFromCtx,
// AssertProjectOwnership) при этом не меняется.
package tenant

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type tenantCtxKey struct{}

// TenantCtx — caller identity. Заполняется из gRPC metadata интерсептором
// (`internal/handler`); в перспективе — из validated IAM token.
type TenantCtx struct {
	// ProjectIDs — projects, которые caller'у разрешено читать/писать.
	// Empty = full access (admin / cluster-scoped) — backward-compat без AuthN.
	ProjectIDs map[string]struct{}
	// Admin — true, если caller имеет cluster-wide read/write.
	Admin bool
}

// HasProjectAccess — может ли caller трогать ресурс из project'а. Empty ProjectIDs
// → true (backward-compat dev-mode); в production-mode guard в интерсепторе
// отвергает anonymous раньше, чем дойдет сюда.
func (t TenantCtx) HasProjectAccess(projectID string) bool {
	if t.Admin || len(t.ProjectIDs) == 0 {
		return true
	}
	_, ok := t.ProjectIDs[projectID]
	return ok
}

// IsAnonymous — true, если caller не предъявил authorization-claims (ни Admin,
// ни ProjectIDs).
func (t TenantCtx) IsAnonymous() bool {
	return !t.Admin && len(t.ProjectIDs) == 0
}

// WithTenant кладет TenantCtx в context (вызывается gRPC-интерсептором).
func WithTenant(ctx context.Context, t TenantCtx) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, t)
}

// TenantFromCtx извлекает TenantCtx; если интерсептор не сработал — возвращает
// empty (ProjectIDs=nil → backward-compat "full access" anonymous mode).
func TenantFromCtx(ctx context.Context) TenantCtx {
	if v := ctx.Value(tenantCtxKey{}); v != nil {
		if t, ok := v.(TenantCtx); ok {
			return t
		}
	}
	return TenantCtx{}
}

// AssertProjectOwnership — handler-side AuthZ: PermissionDenied, если caller не
// имеет доступа к project'у.
func AssertProjectOwnership(ctx context.Context, projectID string) error {
	if TenantFromCtx(ctx).HasProjectAccess(projectID) {
		return nil
	}
	return status.Error(codes.PermissionDenied, "Permission denied")
}
