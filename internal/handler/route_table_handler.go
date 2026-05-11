package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// RouteTableHandler реализует vpcv1.RouteTableServiceServer.
type RouteTableHandler struct {
	vpcv1.UnimplementedRouteTableServiceServer
	svc *svc.RouteTableService
}

// NewRouteTableHandler создаёт RouteTableHandler.
func NewRouteTableHandler(s *svc.RouteTableService) *RouteTableHandler {
	return &RouteTableHandler{svc: s}
}

func (h *RouteTableHandler) Get(ctx context.Context, req *vpcv1.GetRouteTableRequest) (*vpcv1.RouteTable, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.svc.Get(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	return protoconv.RouteTable(rt), nil
}

func (h *RouteTableHandler) List(ctx context.Context, req *vpcv1.ListRouteTablesRequest) (*vpcv1.ListRouteTablesResponse, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	rts, nextToken, err := h.svc.List(ctx, svc.RouteTableFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		resp.RouteTables = append(resp.RouteTables, protoconv.RouteTable(rt))
	}
	return resp, nil
}

func (h *RouteTableHandler) Create(ctx context.Context, req *vpcv1.CreateRouteTableRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	createReq := svc.CreateRouteTableReq{
		FolderID:    req.FolderId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		NetworkID:   req.NetworkId,
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
		createReq.StaticRoutes = append(createReq.StaticRoutes, route)
	}
	op, err := h.svc.Create(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *RouteTableHandler) Update(ctx context.Context, req *vpcv1.UpdateRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.svc.Get(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	updReq := svc.UpdateRouteTableReq{
		RouteTableID: req.RouteTableId,
		Name:         req.Name,
		Description:  req.Description,
		Labels:       req.Labels,
		UpdateMask:   mask,
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
		updReq.StaticRoutes = append(updReq.StaticRoutes, route)
	}
	op, err := h.svc.Update(ctx, updReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *RouteTableHandler) ListOperations(ctx context.Context, req *vpcv1.ListRouteTableOperationsRequest) (*vpcv1.ListRouteTableOperationsResponse, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.svc.Get(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.RouteTableId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTableOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

func (h *RouteTableHandler) Move(ctx context.Context, req *vpcv1.MoveRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.svc.Get(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Move(ctx, req.RouteTableId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *RouteTableHandler) Delete(ctx context.Context, req *vpcv1.DeleteRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.svc.Get(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// routeTableToProto конвертирует domain RouteTable → proto RouteTable.
//
// CreatedAt — truncate до seconds для verbatim YC parity. См.
// YC-DIFF-TIMESTAMP-PRECISION.md.
