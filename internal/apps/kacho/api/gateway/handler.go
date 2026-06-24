// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует Gateway/time DTO-трансферы через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// Handler — реализация vpcv1.GatewayServiceServer на основе use-case'ов. Тонкий
// transport-слой: proto-request → domain → use-case → proto-response. Никакой
// бизнес-логики.
type Handler struct {
	vpcv1.UnimplementedGatewayServiceServer

	create         *CreateGatewayUseCase
	update         *UpdateGatewayUseCase
	delete         *DeleteGatewayUseCase
	get            *GetGatewayUseCase
	list           *ListGatewaysUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateGatewayUseCase,
	update *UpdateGatewayUseCase,
	deleteUC *DeleteGatewayUseCase,
	get *GetGatewayUseCase,
	list *ListGatewaysUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		delete:         deleteUC,
		get:            get,
		list:           list,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ + per-object no-leak.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetGatewayRequest) (*vpcv1.Gateway, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	subject := pbconv.SubjectFromContext(ctx)
	g, err := h.get.Execute(ctx, subject, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, g.ProjectID); err != nil {
		return nil, err
	}
	return gatewayToPb(g)
}

// List — project_id required + AuthZ + FGA list-filter.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListGatewaysRequest) (*vpcv1.ListGatewaysResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
	gws, nextToken, err := h.list.Execute(ctx, subject, GatewayFilter{
		ProjectID: req.ProjectId,
		Filter:    req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListGatewaysResponse{NextPageToken: nextToken}
	for _, g := range gws {
		pb, err := gatewayToPb(g)
		if err != nil {
			return nil, err
		}
		resp.Gateways = append(resp.Gateways, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateGatewayRequest) (*operationpb.Operation, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	gtype := ""
	if _, ok := req.Gateway.(*vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec); ok {
		gtype = "shared_egress"
	}
	g := domain.Gateway{
		ProjectID:   req.ProjectId,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
		GatewayType: domain.GatewayType(gtype),
	}
	op, err := h.create.Execute(ctx, g)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.get.Execute(ctx, "", req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, g.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	gtype := ""
	if _, ok := req.Gateway.(*vpcv1.UpdateGatewayRequest_SharedEgressGatewaySpec); ok {
		gtype = "shared_egress"
	}
	in := UpdateInput{
		GatewayID: req.GatewayId,
		Gateway: domain.Gateway{
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
			GatewayType: domain.GatewayType(gtype),
		},
		UpdateMask: mask,
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.get.Execute(ctx, "", req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, g.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// ListOperations — best-effort AuthZ: ресурс жив → project-ownership проверяем;
// удален (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListGatewayOperationsRequest) (*vpcv1.ListGatewayOperationsResponse, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if g, gerr := h.get.Execute(ctx, "", req.GatewayId); gerr == nil {
		if err := tenant.AssertProjectOwnership(ctx, g.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.GatewayId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListGatewayOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, pbconv.OperationToProto(&ops[i]))
	}
	return resp, nil
}

// gatewayToPb — repo-entity Gateway → proto Gateway через DTO-реестр.
func gatewayToPb(rec *kacho.GatewayRecord) (*vpcv1.Gateway, error) {
	var dst *vpcv1.Gateway
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Gateway failed")
	}
	return dst, nil
}
