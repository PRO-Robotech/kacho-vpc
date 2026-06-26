// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package routetable

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует RouteTable/time DTO трансферы.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// Handler — реализация vpcv1.RouteTableServiceServer на основе use-case'ов.
type Handler struct {
	vpcv1.UnimplementedRouteTableServiceServer

	create         *CreateRouteTableUseCase
	update         *UpdateRouteTableUseCase
	delete         *DeleteRouteTableUseCase
	get            *GetRouteTableUseCase
	list           *ListRouteTablesUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateRouteTableUseCase,
	update *UpdateRouteTableUseCase,
	deleteUC *DeleteRouteTableUseCase,
	get *GetRouteTableUseCase,
	list *ListRouteTablesUseCase,
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
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetRouteTableRequest) (*vpcv1.RouteTable, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	subject := pbconv.SubjectFromContext(ctx)
	rt, err := h.get.Execute(ctx, subject, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, rt.ProjectID); err != nil {
		return nil, err
	}
	return routeTableToPb(rt)
}

// List — project_id required + AuthZ + FGA list-filter.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListRouteTablesRequest) (*vpcv1.ListRouteTablesResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
	rts, nextToken, err := h.list.Execute(ctx, subject, RouteTableFilter{
		ProjectID: req.ProjectId,
		Filter:    req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		pb, err := routeTableToPb(rt)
		if err != nil {
			return nil, err
		}
		resp.RouteTables = append(resp.RouteTables, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateRouteTableRequest) (*operationpb.Operation, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	rt := domain.RouteTable{
		ProjectID:   req.ProjectId,
		NetworkID:   req.NetworkId,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	for _, sr := range req.StaticRoutes {
		route := domain.StaticRoute{
			Labels: sr.Labels,
		}
		if sr.GetDestinationPrefix() != "" {
			route.DestinationPrefix = sr.GetDestinationPrefix()
		}
		if sr.GetNextHopAddress() != "" {
			route.NextHopAddress = sr.GetNextHopAddress()
		}
		rt.StaticRoutes = append(rt.StaticRoutes, route)
	}
	op, err := h.create.Execute(ctx, rt)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.get.Execute(ctx, "", req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, rt.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	in := UpdateInput{
		RouteTableID: req.RouteTableId,
		RouteTable: domain.RouteTable{
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
		},
		UpdateMask: mask,
	}
	for _, sr := range req.StaticRoutes {
		route := domain.StaticRoute{
			Labels: sr.Labels,
		}
		if sr.GetDestinationPrefix() != "" {
			route.DestinationPrefix = sr.GetDestinationPrefix()
		}
		if sr.GetNextHopAddress() != "" {
			route.NextHopAddress = sr.GetNextHopAddress()
		}
		in.RouteTable.StaticRoutes = append(in.RouteTable.StaticRoutes, route)
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.get.Execute(ctx, "", req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, rt.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// ListOperations — best-effort AuthZ.
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListRouteTableOperationsRequest) (*vpcv1.ListRouteTableOperationsResponse, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	if rt, gerr := h.get.Execute(ctx, "", req.RouteTableId); gerr == nil {
		if err := tenant.AssertProjectOwnership(ctx, rt.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.RouteTableId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTableOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, pbconv.OperationToProto(&ops[i]))
	}
	return resp, nil
}

// routeTableToPb — repo-entity RouteTable → proto RouteTable через DTO-реестр.
func routeTableToPb(rec *kachorepo.RouteTableRecord) (*vpcv1.RouteTable, error) {
	var dst *vpcv1.RouteTable
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer RouteTable failed")
	}
	return dst, nil
}
