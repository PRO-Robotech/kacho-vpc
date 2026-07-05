// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

// Server-side request-deadline interceptor'ы (unary + stream).
//
// Мотивация — защита от bounded-pool exhaustion / DoS (CWE-770 / CWE-400):
// gRPC по умолчанию не навязывает deadline, а pgxpool ограничен MaxConns.
// Deadline-less (или намеренно долгий) RPC держит pooled-connection столько,
// сколько выполняется его запрос; MaxConns таких запросов исчерпывают pool и
// весь сервис отказывает. Interceptor кладёт верхнюю границу на обработку
// каждого RPC: если у входящего ctx нет deadline (или он дальше лимита) — ctx
// оборачивается context.WithDeadline(now+timeout). Более строгий client-deadline
// уважается (окно не расширяем). Дополняет DB-level statement_timeout.
//
// timeout<=0 → interceptor — no-op (без границы); composition root в этом случае
// просто не навешивает его.

// capDeadline возвращает ctx с deadline не позднее now+timeout. Если у ctx уже
// есть более ранний (строгий) deadline — возвращает его как есть (cancel — no-op).
func capDeadline(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	limit := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && !dl.After(limit) {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, limit)
}

// UnaryTimeoutInterceptor ограничивает обработку unary-RPC верхней границей
// timeout. timeout<=0 → passthrough (deadline не навязывается).
func UnaryTimeoutInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if timeout <= 0 {
			return handler(ctx, req)
		}
		ctx, cancel := capDeadline(ctx, timeout)
		defer cancel()
		return handler(ctx, req)
	}
}

// StreamTimeoutInterceptor — stream-аналог: оборачивает ss.Context() границей
// timeout. timeout<=0 → passthrough.
func StreamTimeoutInterceptor(timeout time.Duration) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if timeout <= 0 {
			return handler(srv, ss)
		}
		ctx, cancel := capDeadline(ss.Context(), timeout)
		defer cancel()
		return handler(srv, &timeoutServerStream{ServerStream: ss, ctx: ctx})
	}
}

// timeoutServerStream подменяет Context() у обёрнутого stream'а на deadline-bounded.
type timeoutServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *timeoutServerStream) Context() context.Context { return s.ctx }
