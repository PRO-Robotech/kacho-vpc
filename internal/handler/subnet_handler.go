package handler

import (
	"context"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SubnetHandler реализует pb.SubnetServiceServer.
type SubnetHandler struct {
	pb.UnimplementedSubnetServiceServer
	svc *service.SubnetService
}

// NewSubnetHandler создаёт SubnetHandler.
func NewSubnetHandler(svc *service.SubnetService) *SubnetHandler {
	return &SubnetHandler{svc: svc}
}

func (h *SubnetHandler) Get(ctx context.Context, req *pb.GetSubnetRequest) (*pb.Subnet, error) {
	s, err := h.svc.Get(ctx, req.GetSubnetId())
	if err != nil {
		return nil, err
	}
	return subnetDomainToProto(s), nil
}

func (h *SubnetHandler) List(ctx context.Context, req *pb.ListSubnetsRequest) (*pb.ListSubnetsResponse, error) {
	filter := service.ListFilter{
		FolderID:  req.GetFolderId(),
		PageSize:  req.GetPageSize(),
		PageToken: req.GetPageToken(),
		Filter:    req.GetFilter(),
		OrderBy:   req.GetOrderBy(),
	}
	subnets, nextToken, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListSubnetsResponse{NextPageToken: nextToken}
	for i := range subnets {
		resp.Subnets = append(resp.Subnets, subnetDomainToProto(&subnets[i]))
	}
	return resp, nil
}

func (h *SubnetHandler) Create(ctx context.Context, req *pb.CreateSubnetRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Create(ctx,
		req.GetFolderId(), req.GetNetworkId(), req.GetZoneId(), req.GetCidrBlock(),
		req.GetName(), req.GetDescription(), req.GetLabels(),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) Update(ctx context.Context, req *pb.UpdateSubnetRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Update(ctx,
		req.GetSubnetId(), req.GetResourceVersion(),
		req.GetName(), req.GetDescription(), req.GetLabels(),
		maskFields(req.GetUpdateMask()),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) Delete(ctx context.Context, req *pb.DeleteSubnetRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Delete(ctx, req.GetSubnetId())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func subnetDomainToProto(s *domain.Subnet) *pb.Subnet {
	proto := &pb.Subnet{
		Id:              s.ID,
		FolderId:        s.FolderID,
		NetworkId:       s.NetworkID,
		ZoneId:          s.ZoneID,
		CidrBlock:       s.CIDRBlock,
		Name:            s.Name,
		Description:     s.Description,
		Labels:          s.Labels,
		Status:          pb.SubnetStatus(s.Status),
		Generation:      s.Generation,
		ResourceVersion: s.ResourceVersion,
	}
	if !s.CreatedAt.IsZero() {
		proto.CreatedAt = timestamppb.New(s.CreatedAt)
	}
	if !s.StatusLastTransitionAt.IsZero() {
		proto.StatusLastTransitionAt = timestamppb.New(s.StatusLastTransitionAt)
	}
	return proto
}
