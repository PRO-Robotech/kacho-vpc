// Package handler — tenant_interceptor.go: gRPC unary/stream interceptor
// который извлекает caller-folder identity из metadata и кладёт в context.
//
// Это **scaffolding** для AuthZ: сейчас метаданные читаются как plaintext
// (нет AuthN, нет токенов). Когда будет IAM — вместо metadata будет
// claims из validated JWT/IAM-token, но downstream API (TenantFromCtx,
// AssertFolderOwnership) не изменится — handler'ы используют те же helpers.
//
// Без AuthN сервис открыт (известный gap, см. SECURITY.md). Этот interceptor
// включает path к проверкам — handler'ы должны вызывать AssertFolderOwnership
// перед чтением/записью ресурсов.
package handler

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type tenantCtxKey struct{}

// TenantCtx — caller identity. Сейчас populated из gRPC metadata
// (`x-kacho-folder-id`, `x-kacho-actor`); future — из validated IAM token.
type TenantCtx struct {
	// FolderIDs — folders которые caller'у разрешено читать/писать.
	// Empty = full access (admin / cluster-scoped). Этот семантика
	// нужна для backward-compat когда AuthN не включён.
	FolderIDs map[string]struct{}
	// Actor — для audit log (admin@kacho, или sub-claim из JWT).
	Actor string
	// Admin — true если caller имеет cluster-wide read/write.
	// Сейчас определяется по метаданным; future — по IAM роли.
	Admin bool
}

// HasFolderAccess — может ли caller трогать ресурс из folder'а.
// Empty FolderIDs (или Admin=true) даёт full access. Production-mode
// guard на уровне interceptor отфильтровывает anonymous callers до того,
// как этот метод вызовется.
func (t TenantCtx) HasFolderAccess(folderID string) bool {
	if t.Admin || len(t.FolderIDs) == 0 {
		return true
	}
	_, ok := t.FolderIDs[folderID]
	return ok
}

// IsAnonymous — true если caller не предъявил никакой identity-header'ов.
// Used by production-mode fail-closed guard.
func (t TenantCtx) IsAnonymous() bool {
	return t.Actor == "" && len(t.FolderIDs) == 0 && !t.Admin
}

// TenantFromCtx извлекает TenantCtx из context. Если interceptor не
// сработал — возвращает empty TenantCtx с FolderIDs=nil → backward-compat
// "full access" (anonymous mode).
func TenantFromCtx(ctx context.Context) TenantCtx {
	if v := ctx.Value(tenantCtxKey{}); v != nil {
		if t, ok := v.(TenantCtx); ok {
			return t
		}
	}
	return TenantCtx{}
}

// ErrCrossTenant — sentinel для cross-folder access denied.
// Маппится в gRPC PermissionDenied (verbatim YC: "Permission denied").
var ErrCrossTenant = errors.New("permission denied")

// AssertFolderOwnership — handler-side AuthZ check. Возвращает
// PermissionDenied gRPC status если caller не имеет доступа к folder'у.
//
// Использование в handler'е (Get/Update/Delete после repo.Get):
//
//	resource, err := s.repo.Get(ctx, id)
//	if err != nil { return nil, mapRepoErr(err) }
//	if err := AssertFolderOwnership(ctx, resource.FolderID); err != nil {
//	    return nil, err
//	}
//	return toProto(resource), nil
func AssertFolderOwnership(ctx context.Context, folderID string) error {
	t := TenantFromCtx(ctx)
	if t.HasFolderAccess(folderID) {
		return nil
	}
	return status.Error(codes.PermissionDenied, "Permission denied")
}

// TenantUnaryInterceptor — gRPC unary interceptor. Извлекает caller-folder
// identity из metadata и кладёт в ctx как TenantCtx.
//
// Headers (case-insensitive):
//   - `x-kacho-actor` — actor name для audit (e.g. "admin@kacho").
//   - `x-kacho-folder-id` — folder, к которому caller имеет access. Может
//     повторяться для multi-folder access. Empty = anonymous mode.
//   - `x-kacho-admin` — "true" → cluster-wide admin (минует folder-check).
//
// Когда подключится IAM — этот interceptor заменится на JWT-validating
// interceptor который извлекает folders/admin claims из token. Downstream
// API (TenantFromCtx, AssertFolderOwnership) не изменится.
//
// requireAdmin=true (для internal :9091 listener) — отвергает caller'а без
// admin-flag PermissionDenied. Anonymous-mode (нет AuthN) автоматически
// admin=true → backward-compat. С IAM token — Internal RPC доступны только
// service-account'ам с admin-claim'ом.
//
// productionMode=true — fail-closed гейт: anonymous caller → PermissionDenied
// сразу. Включается через KACHO_VPC_AUTH_MODE=production. Защита от
// misconfigured deploy без IAM sidecar (security M5 closure).
func TenantUnaryInterceptor(requireAdmin, productionMode bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		t := tenantFromMetadata(ctx)
		if productionMode && t.IsAnonymous() {
			return nil, status.Error(codes.PermissionDenied,
				"AuthN required (production mode): set x-kacho-* identity headers via gateway")
		}
		if requireAdmin {
			if err := assertAdminAccess(t, info.FullMethod); err != nil {
				return nil, err
			}
		}
		ctx = context.WithValue(ctx, tenantCtxKey{}, t)
		return handler(ctx, req)
	}
}

// TenantStreamInterceptor — то же для server-stream RPC (для Watch).
func TenantStreamInterceptor(requireAdmin, productionMode bool) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		t := tenantFromMetadata(ss.Context())
		if productionMode && t.IsAnonymous() {
			return status.Error(codes.PermissionDenied,
				"AuthN required (production mode): set x-kacho-* identity headers via gateway")
		}
		if requireAdmin {
			if err := assertAdminAccess(t, info.FullMethod); err != nil {
				return err
			}
		}
		ctx := context.WithValue(ss.Context(), tenantCtxKey{}, t)
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

// assertAdminAccess — internal :9091 listener gate. Отвергает не-admin caller'а.
// Anonymous (нет AuthN) → admin=true автоматически (backward-compat).
// Имя метода используется для audit.
func assertAdminAccess(t TenantCtx, fullMethod string) error {
	// Anonymous mode (no metadata) → нет AuthN, нет AuthZ — backward-compat.
	if t.Actor == "" && len(t.FolderIDs) == 0 && !t.Admin {
		return nil
	}
	// AuthN включён, но caller не admin — это RPC только для admin.
	if !t.Admin {
		// Дополнительная защита: если method не /Internal* — вернём NotFound,
		// чтобы не светить структуру (admin-only listener вообще не должен
		// показывать наличие internal сервисов).
		if !strings.Contains(fullMethod, "/Internal") {
			return status.Error(codes.NotFound, "not found")
		}
		return status.Error(codes.PermissionDenied, "Permission denied")
	}
	return nil
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// tenantFromMetadata — internal helper, извлекает TenantCtx из gRPC md.
func tenantFromMetadata(ctx context.Context) TenantCtx {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return TenantCtx{}
	}
	t := TenantCtx{}
	if v := md.Get("x-kacho-actor"); len(v) > 0 {
		t.Actor = v[0]
	}
	if v := md.Get("x-kacho-admin"); len(v) > 0 && v[0] == "true" {
		t.Admin = true
	}
	if folders := md.Get("x-kacho-folder-id"); len(folders) > 0 {
		t.FolderIDs = make(map[string]struct{}, len(folders))
		for _, f := range folders {
			if f != "" {
				t.FolderIDs[f] = struct{}{}
			}
		}
	}
	return t
}
