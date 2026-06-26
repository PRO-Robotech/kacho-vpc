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
)

// OperationHandler реализует operationpb.OperationServiceServer.
type OperationHandler struct {
	operationpb.UnimplementedOperationServiceServer
	repo operations.Repo
}

// NewOperationHandler создает OperationHandler.
func NewOperationHandler(repo operations.Repo) *OperationHandler {
	return &OperationHandler{repo: repo}
}

func (h *OperationHandler) Get(ctx context.Context, req *operationpb.GetOperationRequest) (*operationpb.Operation, error) {
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id required")
	}
	op, err := h.repo.Get(ctx, req.OperationId)
	if err != nil {
		if errors.Is(err, operations.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "operation %s not found", req.OperationId)
		}
		// Generic Internal без leak'а pgx-detail (hostname/dsn в тексте ошибки наружу не уходят).
		return nil, status.Error(codes.Internal, "operation get failed")
	}
	return pbconv.OperationToProto(op), nil
}

func (h *OperationHandler) Cancel(ctx context.Context, req *operationpb.CancelOperationRequest) (*operationpb.Operation, error) {
	if req.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id required")
	}
	if err := h.repo.Cancel(ctx, req.OperationId); err != nil {
		if errors.Is(err, operations.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "operation %s not found", req.OperationId)
		}
		if errors.Is(err, operations.ErrAlreadyDone) {
			return nil, status.Errorf(codes.FailedPrecondition, "operation %s already completed", req.OperationId)
		}
		return nil, status.Error(codes.Internal, "operation cancel failed")
	}
	op, err := h.repo.Get(ctx, req.OperationId)
	if err != nil {
		if errors.Is(err, operations.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "operation %s not found", req.OperationId)
		}
		return nil, status.Error(codes.Internal, "operation reload after cancel failed")
	}
	return pbconv.OperationToProto(op), nil
}
