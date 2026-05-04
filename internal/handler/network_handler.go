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

// NetworkHandler реализует vpcv1.NetworkServiceServer.
type NetworkHandler struct {
	vpcv1.UnimplementedNetworkServiceServer
	svc *svc.NetworkService
}

// NewNetworkHandler создаёт NetworkHandler.
func NewNetworkHandler(s *svc.NetworkService) *NetworkHandler {
	return &NetworkHandler{svc: s}
}

func (h *NetworkHandler) Get(ctx context.Context, req *vpcv1.GetNetworkRequest) (*vpcv1.Network, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	return networkToProto(n), nil
}

func (h *NetworkHandler) List(ctx context.Context, req *vpcv1.ListNetworksRequest) (*vpcv1.ListNetworksResponse, error) {
	nets, nextToken, err := h.svc.List(ctx, svc.NetworkFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworksResponse{NextPageToken: nextToken}
	for _, n := range nets {
		resp.Networks = append(resp.Networks, networkToProto(n))
	}
	return resp, nil
}

func (h *NetworkHandler) Create(ctx context.Context, req *vpcv1.CreateNetworkRequest) (*operationpb.Operation, error) {
	op, err := h.svc.Create(ctx, svc.CreateNetworkReq{
		FolderID:    req.FolderId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) Update(ctx context.Context, req *vpcv1.UpdateNetworkRequest) (*operationpb.Operation, error) {
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.svc.Update(ctx, svc.UpdateNetworkReq{
		NetworkID:   req.NetworkId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		UpdateMask:  mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) ListSubnets(ctx context.Context, req *vpcv1.ListNetworkSubnetsRequest) (*vpcv1.ListNetworkSubnetsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	subs, nextToken, err := h.svc.ListSubnets(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkSubnetsResponse{NextPageToken: nextToken}
	for _, s := range subs {
		resp.Subnets = append(resp.Subnets, subnetToProto(s))
	}
	return resp, nil
}

func (h *NetworkHandler) ListSecurityGroups(ctx context.Context, req *vpcv1.ListNetworkSecurityGroupsRequest) (*vpcv1.ListNetworkSecurityGroupsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// Network must exist; no SG support yet — return empty list (verbatim к YC поведению "пусто").
	if _, err := h.svc.Get(ctx, req.NetworkId); err != nil {
		return nil, err
	}
	return &vpcv1.ListNetworkSecurityGroupsResponse{}, nil
}

func (h *NetworkHandler) ListRouteTables(ctx context.Context, req *vpcv1.ListNetworkRouteTablesRequest) (*vpcv1.ListNetworkRouteTablesResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	rts, nextToken, err := h.svc.ListRouteTables(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		resp.RouteTables = append(resp.RouteTables, routeTableToProto(rt))
	}
	return resp, nil
}

func (h *NetworkHandler) ListOperations(ctx context.Context, req *vpcv1.ListNetworkOperationsRequest) (*vpcv1.ListNetworkOperationsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

func (h *NetworkHandler) Move(ctx context.Context, req *vpcv1.MoveNetworkRequest) (*operationpb.Operation, error) {
	op, err := h.svc.Move(ctx, req.NetworkId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) Delete(ctx context.Context, req *vpcv1.DeleteNetworkRequest) (*operationpb.Operation, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	op, err := h.svc.Delete(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// networkToProto конвертирует domain Network → proto Network.
//
// CreatedAt — truncate до seconds для verbatim YC parity (resource.createdAt
// в YC всегда seconds-precision). См. YC-DIFF-TIMESTAMP-PRECISION.md.
func networkToProto(n *domain.Network) *vpcv1.Network {
	return &vpcv1.Network{
		Id:                     n.ID,
		FolderId:               n.FolderID,
		CreatedAt:              timestamppb.New(n.CreatedAt.Truncate(time.Second)),
		Name:                   n.Name,
		Description:            n.Description,
		Labels:                 n.Labels,
		DefaultSecurityGroupId: n.DefaultSecurityGroupID,
	}
}
