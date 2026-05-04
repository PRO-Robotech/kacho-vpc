package handler

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
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
	return routeTableToProto(rt), nil
}

func (h *RouteTableHandler) List(ctx context.Context, req *vpcv1.ListRouteTablesRequest) (*vpcv1.ListRouteTablesResponse, error) {
	rts, nextToken, err := h.svc.List(ctx, svc.RouteTableFilter{
		FolderID: req.FolderId,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		resp.RouteTables = append(resp.RouteTables, routeTableToProto(rt))
	}
	return resp, nil
}

func (h *RouteTableHandler) Create(ctx context.Context, req *vpcv1.CreateRouteTableRequest) (*operationpb.Operation, error) {
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

func (h *RouteTableHandler) Delete(ctx context.Context, req *vpcv1.DeleteRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
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
func routeTableToProto(rt *domain.RouteTable) *vpcv1.RouteTable {
	p := &vpcv1.RouteTable{
		Id:          rt.ID,
		FolderId:    rt.FolderID,
		CreatedAt:   timestamppb.New(rt.CreatedAt.Truncate(time.Second)),
		Name:        rt.Name,
		Description: rt.Description,
		Labels:      rt.Labels,
		NetworkId:   rt.NetworkID,
	}
	for _, sr := range rt.StaticRoutes {
		protoSR := &vpcv1.StaticRoute{Labels: sr.Labels}
		if sr.DestinationPrefix != "" {
			protoSR.Destination = &vpcv1.StaticRoute_DestinationPrefix{
				DestinationPrefix: sr.DestinationPrefix,
			}
		}
		if sr.NextHopAddress != "" {
			protoSR.NextHop = &vpcv1.StaticRoute_NextHopAddress{
				NextHopAddress: sr.NextHopAddress,
			}
		}
		p.StaticRoutes = append(p.StaticRoutes, protoSR)
	}
	return p
}
