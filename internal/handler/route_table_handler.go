package handler

import (
	"context"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RouteTableHandler реализует pb.RouteTableServiceServer.
type RouteTableHandler struct {
	pb.UnimplementedRouteTableServiceServer
	svc *service.RouteTableService
}

// NewRouteTableHandler создаёт RouteTableHandler.
func NewRouteTableHandler(svc *service.RouteTableService) *RouteTableHandler {
	return &RouteTableHandler{svc: svc}
}

func (h *RouteTableHandler) Get(ctx context.Context, req *pb.GetRouteTableRequest) (*pb.RouteTable, error) {
	rt, err := h.svc.Get(ctx, req.GetRouteTableId())
	if err != nil {
		return nil, err
	}
	return rtDomainToProto(rt), nil
}

func (h *RouteTableHandler) List(ctx context.Context, req *pb.ListRouteTablesRequest) (*pb.ListRouteTablesResponse, error) {
	filter := service.ListFilter{
		FolderID:  req.GetFolderId(),
		PageSize:  req.GetPageSize(),
		PageToken: req.GetPageToken(),
		Filter:    req.GetFilter(),
		OrderBy:   req.GetOrderBy(),
	}
	tables, nextToken, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListRouteTablesResponse{NextPageToken: nextToken}
	for i := range tables {
		resp.RouteTables = append(resp.RouteTables, rtDomainToProto(&tables[i]))
	}
	return resp, nil
}

func (h *RouteTableHandler) Create(ctx context.Context, req *pb.CreateRouteTableRequest) (*operationv1.Operation, error) {
	routes := protoRoutesToDomain(req.GetStaticRoutes())
	op, err := h.svc.Create(ctx,
		req.GetFolderId(), req.GetNetworkId(), req.GetName(), req.GetDescription(), req.GetLabels(), routes,
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *RouteTableHandler) Update(ctx context.Context, req *pb.UpdateRouteTableRequest) (*operationv1.Operation, error) {
	routes := protoRoutesToDomain(req.GetStaticRoutes())
	op, err := h.svc.Update(ctx,
		req.GetRouteTableId(), req.GetResourceVersion(),
		req.GetName(), req.GetDescription(), req.GetLabels(), routes,
		maskFields(req.GetUpdateMask()),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *RouteTableHandler) Delete(ctx context.Context, req *pb.DeleteRouteTableRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Delete(ctx, req.GetRouteTableId())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func rtDomainToProto(rt *domain.RouteTable) *pb.RouteTable {
	routes := make([]*pb.StaticRoute, len(rt.StaticRoutes))
	for i, r := range rt.StaticRoutes {
		routes[i] = &pb.StaticRoute{
			Id:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	proto := &pb.RouteTable{
		Id:              rt.ID,
		FolderId:        rt.FolderID,
		NetworkId:       rt.NetworkID,
		Name:            rt.Name,
		Description:     rt.Description,
		Labels:          rt.Labels,
		Status:          pb.RouteTableStatus(rt.Status),
		Generation:      rt.Generation,
		ResourceVersion: rt.ResourceVersion,
		StaticRoutes:    routes,
	}
	if !rt.CreatedAt.IsZero() {
		proto.CreatedAt = timestamppb.New(rt.CreatedAt)
	}
	return proto
}

func protoRoutesToDomain(protoRoutes []*pb.StaticRoute) []domain.StaticRoute {
	routes := make([]domain.StaticRoute, len(protoRoutes))
	for i, r := range protoRoutes {
		routes[i] = domain.StaticRoute{
			DestinationPrefix: r.GetDestinationPrefix(),
			NextHopAddress:    r.GetNextHopAddress(),
			Description:       r.GetDescription(),
		}
	}
	return routes
}
