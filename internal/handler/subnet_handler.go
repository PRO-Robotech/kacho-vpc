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

// SubnetHandler реализует vpcv1.SubnetServiceServer.
type SubnetHandler struct {
	vpcv1.UnimplementedSubnetServiceServer
	svc *svc.SubnetService
}

// NewSubnetHandler создаёт SubnetHandler.
func NewSubnetHandler(s *svc.SubnetService) *SubnetHandler {
	return &SubnetHandler{svc: s}
}

func (h *SubnetHandler) Get(ctx context.Context, req *vpcv1.GetSubnetRequest) (*vpcv1.Subnet, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	return subnetToProto(sub), nil
}

func (h *SubnetHandler) List(ctx context.Context, req *vpcv1.ListSubnetsRequest) (*vpcv1.ListSubnetsResponse, error) {
	subs, nextToken, err := h.svc.List(ctx, svc.SubnetFilter{
		FolderID: req.FolderId,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSubnetsResponse{NextPageToken: nextToken}
	for _, s := range subs {
		resp.Subnets = append(resp.Subnets, subnetToProto(s))
	}
	return resp, nil
}

func (h *SubnetHandler) Create(ctx context.Context, req *vpcv1.CreateSubnetRequest) (*operationpb.Operation, error) {
	createReq := svc.CreateSubnetReq{
		FolderID:     req.FolderId,
		Name:         req.Name,
		Description:  req.Description,
		Labels:       req.Labels,
		NetworkID:    req.NetworkId,
		ZoneID:       req.ZoneId,
		V4CidrBlocks: req.V4CidrBlocks,
		RouteTableID: req.RouteTableId,
	}
	if req.DhcpOptions != nil {
		createReq.DhcpOptions = &domain.DhcpOptions{
			DomainNameServers: req.DhcpOptions.DomainNameServers,
			DomainName:        req.DhcpOptions.DomainName,
			NtpServers:        req.DhcpOptions.NtpServers,
		}
	}
	op, err := h.svc.Create(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) Update(ctx context.Context, req *vpcv1.UpdateSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	updReq := svc.UpdateSubnetReq{
		SubnetID:     req.SubnetId,
		Name:         req.Name,
		Description:  req.Description,
		Labels:       req.Labels,
		RouteTableID: req.RouteTableId,
		V4CidrBlocks: req.V4CidrBlocks,
		UpdateMask:   mask,
	}
	if req.DhcpOptions != nil {
		updReq.DhcpOptions = &domain.DhcpOptions{
			DomainNameServers: req.DhcpOptions.DomainNameServers,
			DomainName:        req.DhcpOptions.DomainName,
			NtpServers:        req.DhcpOptions.NtpServers,
		}
	}
	op, err := h.svc.Update(ctx, updReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) Delete(ctx context.Context, req *vpcv1.DeleteSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	op, err := h.svc.Delete(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// subnetToProto конвертирует domain Subnet → proto Subnet.
//
// CreatedAt — truncate до seconds для verbatim YC parity. См.
// YC-DIFF-TIMESTAMP-PRECISION.md.
func subnetToProto(s *domain.Subnet) *vpcv1.Subnet {
	p := &vpcv1.Subnet{
		Id:           s.ID,
		FolderId:     s.FolderID,
		CreatedAt:    timestamppb.New(s.CreatedAt.Truncate(time.Second)),
		Name:         s.Name,
		Description:  s.Description,
		Labels:       s.Labels,
		NetworkId:    s.NetworkID,
		ZoneId:       s.ZoneID,
		V4CidrBlocks: s.V4CidrBlocks,
		V6CidrBlocks: s.V6CidrBlocks,
		RouteTableId: s.RouteTableID,
	}
	if s.DhcpOptions != nil {
		p.DhcpOptions = &vpcv1.DhcpOptions{
			DomainNameServers: s.DhcpOptions.DomainNameServers,
			DomainName:        s.DhcpOptions.DomainName,
			NtpServers:        s.DhcpOptions.NtpServers,
		}
	}
	return p
}
