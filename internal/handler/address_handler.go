package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// AddressHandler реализует vpcv1.AddressServiceServer.
type AddressHandler struct {
	vpcv1.UnimplementedAddressServiceServer
	svc       *svc.AddressService
	subnetSvc *svc.SubnetService // для AuthZ pre-check на ListBySubnet
}

// NewAddressHandler создаёт AddressHandler. subnet может быть nil только
// в unit-тестах (handler nil-safe в ListBySubnet); production composition
// root в cmd/vpc/main.go обязан передать non-nil — иначе ListBySubnet
// AuthZ check будет skip'нут (R10 fail-fast hardening — см. M3 carry-over).
func NewAddressHandler(s *svc.AddressService, subnet *svc.SubnetService) *AddressHandler {
	return &AddressHandler{svc: s, subnetSvc: subnet}
}

func (h *AddressHandler) Get(ctx context.Context, req *vpcv1.GetAddressRequest) (*vpcv1.Address, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.svc.Get(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, a.FolderID); err != nil {
		return nil, err
	}
	return protoconv.Address(a), nil
}

func (h *AddressHandler) GetByValue(ctx context.Context, req *vpcv1.GetAddressByValueRequest) (*vpcv1.Address, error) {
	externalIP := req.GetExternalIpv4Address()
	internalIP := req.GetInternalIpv4Address()
	subnetID := req.GetSubnetId()
	a, err := h.svc.GetByValue(ctx, externalIP, internalIP, subnetID)
	if err != nil {
		return nil, err
	}
	// AuthZ: post-fetch check (нельзя проверить до lookup'а — RPC резолвит
	// IP→Address). Маскируем под NotFound вместо PermissionDenied чтобы
	// не leak'нуть существование IP в чужом folder'е (R10 m4 closure +
	// verbatim YC parity: not-owned-not-existing).
	if err := AssertFolderOwnership(ctx, a.FolderID); err != nil {
		return nil, status.Error(codes.NotFound, "Address not found")
	}
	return protoconv.Address(a), nil
}

func (h *AddressHandler) ListBySubnet(ctx context.Context, req *vpcv1.ListAddressesBySubnetRequest) (*vpcv1.ListAddressesBySubnetResponse, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// AuthZ: child list — caller обязан владеть parent subnet'ом.
	// subnetSvc может быть nil только в unit-тестах (см. NewAddressHandler).
	if h.subnetSvc != nil {
		sub, err := h.subnetSvc.Get(ctx, req.SubnetId)
		if err != nil {
			return nil, err
		}
		if err := AssertFolderOwnership(ctx, sub.FolderID); err != nil {
			return nil, err
		}
	}
	addrs, nextToken, err := h.svc.ListBySubnet(ctx, req.SubnetId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressesBySubnetResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		resp.Addresses = append(resp.Addresses, protoconv.Address(a))
	}
	return resp, nil
}

func (h *AddressHandler) List(ctx context.Context, req *vpcv1.ListAddressesRequest) (*vpcv1.ListAddressesResponse, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	addrs, nextToken, err := h.svc.List(ctx, svc.AddressFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressesResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		resp.Addresses = append(resp.Addresses, protoconv.Address(a))
	}
	return resp, nil
}

func (h *AddressHandler) Create(ctx context.Context, req *vpcv1.CreateAddressRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	createReq := svc.CreateAddressReq{
		FolderID:           req.FolderId,
		Name:               req.Name,
		Description:        req.Description,
		Labels:             req.Labels,
		DeletionProtection: req.DeletionProtection,
	}

	if ext := req.GetExternalIpv4AddressSpec(); ext != nil {
		createReq.ExternalSpec = &svc.ExternalAddrSpec{
			Address: ext.Address,
			ZoneID:  ext.ZoneId,
		}
		if r := ext.GetRequirements(); r != nil {
			createReq.ExternalSpec.Requirements = &svc.AddrRequirements{
				DdosProtectionProvider: r.DdosProtectionProvider,
				OutgoingSmtpCapability: r.OutgoingSmtpCapability,
			}
		}
	} else if intSpec := req.GetInternalIpv4AddressSpec(); intSpec != nil {
		createReq.InternalSpec = &svc.InternalAddrSpec{
			Address:  intSpec.Address,
			SubnetID: intSpec.GetSubnetId(),
		}
	}

	op, err := h.svc.Create(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *AddressHandler) Update(ctx context.Context, req *vpcv1.UpdateAddressRequest) (*operationpb.Operation, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.svc.Get(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, a.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.svc.Update(ctx, svc.UpdateAddressReq{
		AddressID:          req.AddressId,
		Name:               req.Name,
		Description:        req.Description,
		Labels:             req.Labels,
		DeletionProtection: req.DeletionProtection,
		Reserved:           req.Reserved,
		UpdateMask:         mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *AddressHandler) ListOperations(ctx context.Context, req *vpcv1.ListAddressOperationsRequest) (*vpcv1.ListAddressOperationsResponse, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.svc.Get(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, a.FolderID); err != nil {
		return nil, err
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.AddressId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

func (h *AddressHandler) Move(ctx context.Context, req *vpcv1.MoveAddressRequest) (*operationpb.Operation, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.svc.Get(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, a.FolderID); err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Move(ctx, req.AddressId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *AddressHandler) Delete(ctx context.Context, req *vpcv1.DeleteAddressRequest) (*operationpb.Operation, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.svc.Get(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, a.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// addressToProto конвертирует domain Address → proto Address.
//
// CreatedAt — truncate до seconds для verbatim YC parity. См.
// YC-DIFF-TIMESTAMP-PRECISION.md.
