// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"

	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// Handler — реализация vpcv1.AnycastAddressPoolServiceServer на основе use-case'ов.
// Тонкий transport-слой: proto-request → use-case → proto-response. Read (Get/List)
// — sync; мутации (Create/Update/Delete/AttachNetwork/DetachNetwork) → Operation.
//
// Existence-hiding: Update/Delete/AttachNetwork/DetachNetwork сперва читают пул
// через get-use-case (per-object no-leak → deny→404), затем AssertProjectOwnership.
type Handler struct {
	vpcv1.UnimplementedAnycastAddressPoolServiceServer

	create *CreateAnycastAddressPoolUseCase
	update *UpdateAnycastAddressPoolUseCase
	delete *DeleteAnycastAddressPoolUseCase
	get    *GetAnycastAddressPoolUseCase
	list   *ListAnycastAddressPoolsUseCase
	attach *AttachNetworkUseCase
	detach *DetachNetworkUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateAnycastAddressPoolUseCase,
	update *UpdateAnycastAddressPoolUseCase,
	deleteUC *DeleteAnycastAddressPoolUseCase,
	get *GetAnycastAddressPoolUseCase,
	list *ListAnycastAddressPoolsUseCase,
	attach *AttachNetworkUseCase,
	detach *DetachNetworkUseCase,
) *Handler {
	return &Handler{
		create: create,
		update: update,
		delete: deleteUC,
		get:    get,
		list:   list,
		attach: attach,
		detach: detach,
	}
}

// Get — sync read + AuthZ + per-object no-leak.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetAnycastAddressPoolRequest) (*vpcv1.AnycastAddressPool, error) {
	subject := pbconv.SubjectFromContext(ctx)
	rec, err := h.get.Execute(ctx, subject, req.AnycastAddressPoolId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, rec.ProjectID); err != nil {
		return nil, err
	}
	return toProto(rec), nil
}

// List — project_id required + AuthZ + FGA list-filter.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListAnycastAddressPoolsRequest) (*vpcv1.ListAnycastAddressPoolsResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
	pools, nextToken, err := h.list.Execute(ctx, subject, AnycastAddressPoolFilter{
		ProjectID: req.ProjectId,
		Name:      req.Filter,
		NetworkID: req.NetworkId,
		Scope:     scopeFromProto(req.Scope),
		IPVersion: ipVersionFromProto(req.IpVersion),
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAnycastAddressPoolsResponse{NextPageToken: nextToken}
	for _, p := range pools {
		resp.AnycastAddressPools = append(resp.AnycastAddressPools, toProto(p))
	}
	return resp, nil
}

// Create — AuthZ → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateAnycastAddressPoolRequest) (*operationpb.Operation, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	op, err := h.create.Execute(ctx, CreateInput{
		ProjectID:   req.ProjectId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		Scope:       scopeFromProto(req.Scope),
		IPVersion:   ipVersionFromProto(req.IpVersion),
		CIDRBlocks:  req.CidrBlocks,
	})
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Update — existence-hiding Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateAnycastAddressPoolRequest) (*operationpb.Operation, error) {
	if err := h.assertVisibleOwned(ctx, req.AnycastAddressPoolId); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.update.Execute(ctx, UpdateInput{
		ID:          req.AnycastAddressPoolId,
		UpdateMask:  mask,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
	})
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Delete — existence-hiding Get + AuthZ + use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteAnycastAddressPoolRequest) (*operationpb.Operation, error) {
	if err := h.assertVisibleOwned(ctx, req.AnycastAddressPoolId); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.AnycastAddressPoolId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// AttachNetwork — existence-hiding Get (на пул) + AuthZ + use-case.
func (h *Handler) AttachNetwork(ctx context.Context, req *vpcv1.AttachNetworkRequest) (*operationpb.Operation, error) {
	if err := h.assertVisibleOwned(ctx, req.AnycastAddressPoolId); err != nil {
		return nil, err
	}
	op, err := h.attach.Execute(ctx, req.AnycastAddressPoolId, req.NetworkId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// DetachNetwork — existence-hiding Get (на пул) + AuthZ + use-case.
func (h *Handler) DetachNetwork(ctx context.Context, req *vpcv1.DetachNetworkRequest) (*operationpb.Operation, error) {
	if err := h.assertVisibleOwned(ctx, req.AnycastAddressPoolId); err != nil {
		return nil, err
	}
	op, err := h.detach.Execute(ctx, req.AnycastAddressPoolId, req.NetworkId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// assertVisibleOwned — общий existence-hiding гейт мутаций: Get (per-object
// no-leak → deny→404) + AssertProjectOwnership. malformed id ловится sync внутри
// get-use-case (ResourceID первым стейтментом).
func (h *Handler) assertVisibleOwned(ctx context.Context, poolID string) error {
	subject := pbconv.SubjectFromContext(ctx)
	rec, err := h.get.Execute(ctx, subject, poolID)
	if err != nil {
		return err
	}
	return tenant.AssertProjectOwnership(ctx, rec.ProjectID)
}

// compile-time guard: Handler удовлетворяет server-интерфейсу.
var _ vpcv1.AnycastAddressPoolServiceServer = (*Handler)(nil)
