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

// GatewayHandler реализует vpcv1.GatewayServiceServer.
type GatewayHandler struct {
	vpcv1.UnimplementedGatewayServiceServer
	svc *svc.GatewayService
}

// NewGatewayHandler создаёт GatewayHandler.
func NewGatewayHandler(s *svc.GatewayService) *GatewayHandler {
	return &GatewayHandler{svc: s}
}

func (h *GatewayHandler) Get(ctx context.Context, req *vpcv1.GetGatewayRequest) (*vpcv1.Gateway, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.svc.Get(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	return gatewayToProto(g), nil
}

func (h *GatewayHandler) List(ctx context.Context, req *vpcv1.ListGatewaysRequest) (*vpcv1.ListGatewaysResponse, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	gws, nextToken, err := h.svc.List(ctx, svc.GatewayFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListGatewaysResponse{NextPageToken: nextToken}
	for _, g := range gws {
		resp.Gateways = append(resp.Gateways, gatewayToProto(g))
	}
	return resp, nil
}

func (h *GatewayHandler) Create(ctx context.Context, req *vpcv1.CreateGatewayRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	gtype := ""
	if _, ok := req.Gateway.(*vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec); ok {
		gtype = "shared_egress"
	}
	op, err := h.svc.Create(ctx, svc.CreateGatewayReq{
		FolderID:    req.FolderId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		GatewayType: gtype,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *GatewayHandler) Update(ctx context.Context, req *vpcv1.UpdateGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.svc.Get(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, g.FolderID); err != nil {
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
	op, err := h.svc.Update(ctx, svc.UpdateGatewayReq{
		GatewayID:   req.GatewayId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		GatewayType: gtype,
		UpdateMask:  mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *GatewayHandler) Delete(ctx context.Context, req *vpcv1.DeleteGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.svc.Get(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *GatewayHandler) Move(ctx context.Context, req *vpcv1.MoveGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.svc.Get(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Move(ctx, req.GatewayId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *GatewayHandler) ListOperations(ctx context.Context, req *vpcv1.ListGatewayOperationsRequest) (*vpcv1.ListGatewayOperationsResponse, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.svc.Get(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.GatewayId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListGatewayOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// gatewayToProto конвертирует domain Gateway в proto Gateway, заполняя oneof.
func gatewayToProto(g *domain.Gateway) *vpcv1.Gateway {
	p := &vpcv1.Gateway{
		Id:          g.ID,
		FolderId:    g.FolderID,
		CreatedAt:   timestamppb.New(g.CreatedAt.Truncate(time.Second)),
		Name:        g.Name,
		Description: g.Description,
		Labels:      g.Labels,
	}
	// shared_egress — единственный тип в YC.
	p.Gateway = &vpcv1.Gateway_SharedEgressGateway{
		SharedEgressGateway: &vpcv1.SharedEgressGateway{},
	}
	return p
}
