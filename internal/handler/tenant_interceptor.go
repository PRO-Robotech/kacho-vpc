// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package handler — gRPC unary/stream interceptor, извлекающий caller-identity
// из metadata и кладущий ее в context через `internal/tenant`.
//
// AuthZ-обвязка: метаданные читаются как plaintext (без AuthN/токенов). С
// интеграцией IAM здесь появится JWT-validating interceptor, достающий
// projects/admin claims из token; downstream API (`tenant.TenantFromCtx`,
// `tenant.AssertProjectOwnership`) при этом не меняется.
//
// Identity-носитель и handler-side AuthZ-хелперы живут в `internal/tenant`,
// чтобы use-case-пакеты не зависели от транспорта.
package handler

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// TenantUnaryInterceptor — gRPC unary interceptor. Извлекает caller-identity из
// metadata и кладет в ctx через tenant.WithTenant.
//
// Headers (case-insensitive):
//   - `x-kacho-actor` — actor name для audit (e.g. "admin@kacho").
//   - `x-kacho-project-id` — project, к которому caller имеет access (повторяемый).
//   - `x-kacho-admin` — "true" → cluster-wide admin.
//
// requireAdmin=true (internal :9091 listener) — отвергает не-admin caller'а.
// productionMode=true — fail-closed: anonymous caller → PermissionDenied сразу
// (KACHO_VPC_AUTH_MODE=production; защита от deploy без IAM sidecar).
//
// x-kacho-admin honored ТОЛЬКО на internal listener (honorAdmin=requireAdmin):
// на public listener'е client-supplied admin-заголовок игнорируется, чтобы
// подделанный `x-kacho-admin:true` не превращал AssertProjectOwnership в no-op
// cluster-wide (SEC hardening — defense-in-depth: admin-полномочия не выводятся
// из plaintext-заголовка на tenant-facing поверхности).
func TenantUnaryInterceptor(requireAdmin, productionMode bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		t := tenantFromMetadata(ctx, requireAdmin)
		if productionMode && t.IsAnonymous() {
			return nil, status.Error(codes.PermissionDenied,
				"AuthN required (production mode): set x-kacho-* identity headers via gateway")
		}
		if requireAdmin {
			if err := assertAdminAccess(t, info.FullMethod); err != nil {
				return nil, err
			}
		}
		return handler(tenant.WithTenant(ctx, t), req)
	}
}

// TenantStreamInterceptor — то же для server-stream RPC. x-kacho-admin honored
// только на internal listener (honorAdmin=requireAdmin), см. TenantUnaryInterceptor.
func TenantStreamInterceptor(requireAdmin, productionMode bool) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		t := tenantFromMetadata(ss.Context(), requireAdmin)
		if productionMode && t.IsAnonymous() {
			return status.Error(codes.PermissionDenied,
				"AuthN required (production mode): set x-kacho-* identity headers via gateway")
		}
		if requireAdmin {
			if err := assertAdminAccess(t, info.FullMethod); err != nil {
				return err
			}
		}
		ctx := tenant.WithTenant(ss.Context(), t)
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

// assertAdminAccess — internal :9091 listener gate. Отвергает не-admin caller'а.
// Anonymous (нет AuthN) → пропускается в dev-mode (backward-compat); в
// production-mode anonymous уже отвергнут вышестоящим productionMode-guard'ом.
func assertAdminAccess(t tenant.TenantCtx, fullMethod string) error {
	if t.IsAnonymous() {
		return nil
	}
	if !t.Admin {
		// Не-/Internal* method для не-admin → NotFound (не светить структуру
		// admin-only listener'а). HasPrefix безопаснее Contains.
		if !strings.HasPrefix(fullMethod, "/kacho.cloud.vpc.v1.Internal") {
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

// tenantFromMetadata — извлекает tenant.TenantCtx из gRPC metadata.
//
// honorAdmin=false (public listener) — client-supplied x-kacho-admin
// игнорируется: t.Admin остается false, чтобы подделанный заголовок не давал
// cluster-wide bypass AssertProjectOwnership. honorAdmin=true — только internal
// admin-listener (:9091), где admin-полномочия легитимны.
func tenantFromMetadata(ctx context.Context, honorAdmin bool) tenant.TenantCtx {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return tenant.TenantCtx{}
	}
	t := tenant.TenantCtx{}
	if v := md.Get("x-kacho-actor"); len(v) > 0 {
		t.Actor = v[0]
	}
	if honorAdmin {
		if v := md.Get("x-kacho-admin"); len(v) > 0 && v[0] == "true" {
			t.Admin = true
		}
	}
	// x-kacho-project-id — projects, к которым caller имеет access (повторяемый).
	if projectIDs := md.Get("x-kacho-project-id"); len(projectIDs) > 0 {
		t.ProjectIDs = make(map[string]struct{}, len(projectIDs))
		for _, p := range projectIDs {
			if p != "" {
				t.ProjectIDs[p] = struct{}{}
			}
		}
	}
	return t
}
