// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server-side panic-recovery interceptor'ы (unary + stream).
//
// Мотивация — availability (CWE-248 / CWE-703): grpc-go НЕ восстанавливает
// panic из handler'ов — паника в любом sync request-path handler'е, интерсепторе
// (principal-extract, tenant, authz scope-extractor) или DTO/toproto-конверсии
// уходит в serving-горутину и роняет ВЕСЬ процесс (вместе с in-flight запросами,
// LRO-worker'ом и FGA register-drainer'ом) — DoS от одного nil-deref. Async-путь
// уже доказывает необходимость panic-containment (operations.Run маскирует panic
// worker'а; network/create.go имеет явный defer recover()) — sync-цепочки обязаны
// нести симметричный guard.
//
// Recovery-interceptor ставится ПЕРВЫМ (outermost) в цепочке обоих листенеров,
// чтобы ловить panic и из вложенных интерсепторов, и из handler'а. Панический
// текст НИКОГДА не течёт наружу: gRPC-сообщение — фиксированный opaque «internal
// error» (security.md hardening-инвариант #1: INTERNAL не эхает внутренние
// детали). Реальная причина + stack пишутся в лог для диагностики.

// recoverErrorMsg — единый opaque-текст, отдаваемый клиенту при восстановленной
// панике (leak-safe: driver/PII/внутренние детали из panic-значения не текут).
const recoverErrorMsg = "internal error"

// UnaryRecoveryInterceptor восстанавливает панику unary-handler'а/интерсепторов,
// логирует причину+stack и возвращает opaque codes.Internal. logger==nil → без
// логирования (recovery всё равно работает).
func UnaryRecoveryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logPanic(logger, methodOf(info), r)
				resp = nil
				err = status.Error(codes.Internal, recoverErrorMsg)
			}
		}()
		return handler(ctx, req)
	}
}

// StreamRecoveryInterceptor — stream-аналог: восстанавливает панику stream-handler'а.
func StreamRecoveryInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if r := recover(); r != nil {
				method := ""
				if info != nil {
					method = info.FullMethod
				}
				logPanic(logger, method, r)
				err = status.Error(codes.Internal, recoverErrorMsg)
			}
		}()
		return handler(srv, ss)
	}
}

func methodOf(info *grpc.UnaryServerInfo) string {
	if info == nil {
		return ""
	}
	return info.FullMethod
}

func logPanic(logger *slog.Logger, method string, r any) {
	if logger == nil {
		return
	}
	logger.Error("gRPC handler panic recovered",
		slog.String("method", method),
		slog.Any("panic", r),
		slog.String("stack", string(debug.Stack())),
	)
}
