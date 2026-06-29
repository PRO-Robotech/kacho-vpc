// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package fgaboot встраивает corelib-овый fail-closed boot-gate в gRPC-сервер
// kacho-vpc: unary-интерсептор отклоняет мутирующие Create-RPC tenant-ресурсов,
// когда взведен --require-iam, а связанный с IAM register-drainer еще не поднят —
// чтобы ни один ресурс не создавался без доставляемого owner-tuple intent.
// Read-RPC и Internal-admin Create (без owner-tuple) под gate не попадают.
package fgaboot

import (
	"context"
	"strings"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/outbox/bootgate"
)

// GuardCreateUnary — unary-интерсептор, который сверяется с boot-gate на
// Create-RPC tenant-ресурсов. При отказе возвращает UNAVAILABLE-ошибку gate и не
// вызывает handler (ресурс не создается).
func GuardCreateUnary(gate *bootgate.Gate) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if IsGatedCreate(info.FullMethod) {
			if err := gate.GuardMutation(); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}
}

// IsGatedCreate сообщает, является ли полный gRPC-метод Create tenant-ресурса,
// записывающим owner-tuple intent (значит, под gate). Матчит
// "/<pkg>.<Service>/Create", но исключает Internal*-admin сервисы (AddressPool —
// без owner-tuple). Полный метод — "/kacho.cloud.vpc.v1.<Service>/Create"; имя
// сервиса лежит между последней '.' и хвостовым '/Create', поэтому Internal*
// распознается по подстроке ".Internal".
func IsGatedCreate(fullMethod string) bool {
	if !strings.HasSuffix(fullMethod, "/Create") {
		return false
	}
	return !strings.Contains(fullMethod, ".Internal")
}
