// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// OperationHandler реализует operationpb.OperationServiceServer.
//
// Get/Cancel энфорсят владельца операции: владелец — principal, создавший её
// (колонки principal_type/principal_id записи operations). `operation_id` опакен,
// но это прямой объект-референс: без проверки владельца любой аутентифицированный
// caller, узнав чужой id, прочитал бы чужой ресурс (Operation.response несёт его
// целиком) или отменил бы чужую in-flight мутацию. Поэтому ownership-предикат
// энфорсится тут, через ownership-scoped repo (GetOwned/CancelOwned, предикат в
// SQL WHERE). Чужой/несуществующий id отдаёт одинаковый NotFound (no-leak:
// «есть, но не твоя» неотличимо от «нет такой»). Cluster-admin — сквозной доступ.
type OperationHandler struct {
	operationpb.UnimplementedOperationServiceServer
	repo operations.Repo
}

// NewOperationHandler создаёт OperationHandler. В проде repo — pgRepo, который
// реализует operations.OwnedOperationRepo; если не реализует (ошибка wiring'а) —
// ownership-вызовы возвращают INTERNAL (fail-closed, не silent-bypass).
func NewOperationHandler(repo operations.Repo) *OperationHandler {
	return &OperationHandler{repo: repo}
}

func (h *OperationHandler) Get(ctx context.Context, req *operationpb.GetOperationRequest) (*operationpb.Operation, error) {
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id required")
	}
	// Cluster-admin (доверенный x-kacho-admin) — ownership-предикат снимается.
	if tenant.TenantFromCtx(ctx).Admin {
		op, err := h.repo.Get(ctx, req.OperationId)
		if err != nil {
			return nil, mapOpGetErr(err, req.OperationId)
		}
		return pbconv.OperationToProto(op), nil
	}
	owned, ok := operations.AsOwned(h.repo)
	if !ok {
		return nil, status.Error(codes.Internal, "operation get failed")
	}
	owner := operations.OwnerFromPrincipal(operations.PrincipalFromContext(ctx))
	op, err := owned.GetOwned(ctx, req.OperationId, owner)
	if err != nil {
		return nil, mapOpGetErr(err, req.OperationId)
	}
	return pbconv.OperationToProto(op), nil
}

func (h *OperationHandler) Cancel(ctx context.Context, req *operationpb.CancelOperationRequest) (*operationpb.Operation, error) {
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id required")
	}
	owned, ok := operations.AsOwned(h.repo)
	if !ok {
		return nil, status.Error(codes.Internal, "operation cancel failed")
	}

	// Резолв owner-ключа. Для cluster-admin предикат снимается: владельца
	// операции читаем прямым (без предиката) Get'ом и отменяем его же ключом —
	// атомарный CancelOwned возвращает терминальное состояние в RETURNING, так
	// что отдельный reload-Get после отмены не нужен ни на одной из веток.
	owner := operations.OwnerFromPrincipal(operations.PrincipalFromContext(ctx))
	if tenant.TenantFromCtx(ctx).Admin {
		op, err := h.repo.Get(ctx, req.OperationId)
		if err != nil {
			if errors.Is(err, operations.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "operation %s not found", req.OperationId)
			}
			return nil, status.Error(codes.Internal, "operation cancel failed")
		}
		owner = operations.Owner{PrincipalType: op.Principal.Type, PrincipalID: op.Principal.ID}
	}

	op, err := owned.CancelOwned(ctx, req.OperationId, owner)
	if err != nil {
		if errors.Is(err, operations.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "operation %s not found", req.OperationId)
		}
		if errors.Is(err, operations.ErrAlreadyDone) {
			return nil, status.Errorf(codes.FailedPrecondition, "operation %s already completed", req.OperationId)
		}
		return nil, status.Error(codes.Internal, "operation cancel failed")
	}
	return pbconv.OperationToProto(op), nil
}

// mapOpGetErr — маппинг repo-ошибки Get'а в gRPC-код. ErrNotFound (нет записи
// ИЛИ не владелец) → NotFound с эхо-id (no-leak). Прочее → фиксированный
// INTERNAL без leak'а pgx/SQL-detail наружу.
func mapOpGetErr(err error, id string) error {
	if errors.Is(err, operations.ErrNotFound) {
		return status.Errorf(codes.NotFound, "operation %s not found", id)
	}
	return status.Error(codes.Internal, "operation get failed")
}
