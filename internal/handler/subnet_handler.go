package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	reference "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/reference"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
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
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	return protoconv.Subnet(sub), nil
}

func (h *SubnetHandler) List(ctx context.Context, req *vpcv1.ListSubnetsRequest) (*vpcv1.ListSubnetsResponse, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	subs, nextToken, err := h.svc.List(ctx, svc.SubnetFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSubnetsResponse{NextPageToken: nextToken}
	for _, s := range subs {
		resp.Subnets = append(resp.Subnets, protoconv.Subnet(s))
	}
	return resp, nil
}

func (h *SubnetHandler) Create(ctx context.Context, req *vpcv1.CreateSubnetRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	createReq := svc.CreateSubnetReq{
		FolderID:     req.FolderId,
		Name:         req.Name,
		Description:  req.Description,
		Labels:       req.Labels,
		NetworkID:    req.NetworkId,
		ZoneID:       req.ZoneId,
		V4CidrBlocks: req.V4CidrBlocks,
		V6CidrBlocks: req.V6CidrBlocks,
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
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
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

func (h *SubnetHandler) ListOperations(ctx context.Context, req *vpcv1.ListSubnetOperationsRequest) (*vpcv1.ListSubnetOperationsResponse, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.SubnetId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSubnetOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

func (h *SubnetHandler) Move(ctx context.Context, req *vpcv1.MoveSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Move(ctx, req.SubnetId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) Delete(ctx context.Context, req *vpcv1.DeleteSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) AddCidrBlocks(ctx context.Context, req *vpcv1.AddSubnetCidrBlocksRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.AddCidrBlocks(ctx, req.SubnetId, req.V4CidrBlocks)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) RemoveCidrBlocks(ctx context.Context, req *vpcv1.RemoveSubnetCidrBlocksRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.RemoveCidrBlocks(ctx, req.SubnetId, req.V4CidrBlocks)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) Relocate(ctx context.Context, req *vpcv1.RelocateSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Relocate(ctx, req.SubnetId, req.DestinationZoneId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SubnetHandler) ListUsedAddresses(ctx context.Context, req *vpcv1.ListUsedAddressesRequest) (*vpcv1.ListUsedAddressesResponse, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	sub, err := h.svc.Get(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
		return nil, err
	}
	addrs, refs, nextToken, err := h.svc.ListUsedAddresses(ctx, req.SubnetId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListUsedAddressesResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		ua := &vpcv1.UsedAddress{
			IpVersion: vpcv1.IpVersion(a.IpVersion),
		}
		if a.InternalIpv4 != nil {
			ua.Address = a.InternalIpv4.Address
		} else if a.ExternalIpv4 != nil {
			ua.Address = a.ExternalIpv4.Address
		}
		// references[] — кто использует адрес (referrer-tracking; YC-like).
		if ref, ok := refs[a.ID]; ok && ref != nil {
			ua.References = []*reference.Reference{{
				Referrer: &reference.Referrer{Type: ref.ReferrerType, Id: ref.ReferrerID},
				Type:     reference.Reference_USED_BY,
			}}
		}
		resp.Addresses = append(resp.Addresses, ua)
	}
	return resp, nil
}

// subnetToProto конвертирует domain Subnet → proto Subnet.
//
// CreatedAt — truncate до seconds для verbatim YC parity. См.
// YC-DIFF-TIMESTAMP-PRECISION.md.
