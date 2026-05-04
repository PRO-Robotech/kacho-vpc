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

// AddressHandler реализует vpcv1.AddressServiceServer.
type AddressHandler struct {
	vpcv1.UnimplementedAddressServiceServer
	svc *svc.AddressService
}

// NewAddressHandler создаёт AddressHandler.
func NewAddressHandler(s *svc.AddressService) *AddressHandler {
	return &AddressHandler{svc: s}
}

func (h *AddressHandler) Get(ctx context.Context, req *vpcv1.GetAddressRequest) (*vpcv1.Address, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.svc.Get(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	return addressToProto(a), nil
}

func (h *AddressHandler) List(ctx context.Context, req *vpcv1.ListAddressesRequest) (*vpcv1.ListAddressesResponse, error) {
	addrs, nextToken, err := h.svc.List(ctx, svc.AddressFilter{
		FolderID: req.FolderId,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressesResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		resp.Addresses = append(resp.Addresses, addressToProto(a))
	}
	return resp, nil
}

func (h *AddressHandler) Create(ctx context.Context, req *vpcv1.CreateAddressRequest) (*operationpb.Operation, error) {
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

func (h *AddressHandler) Delete(ctx context.Context, req *vpcv1.DeleteAddressRequest) (*operationpb.Operation, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
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
func addressToProto(a *domain.Address) *vpcv1.Address {
	p := &vpcv1.Address{
		Id:                 a.ID,
		FolderId:           a.FolderID,
		CreatedAt:          timestamppb.New(a.CreatedAt.Truncate(time.Second)),
		Name:               a.Name,
		Description:        a.Description,
		Labels:             a.Labels,
		Reserved:           a.Reserved,
		Used:               a.Used,
		Type:               vpcv1.Address_Type(a.Type),
		IpVersion:          vpcv1.Address_IpVersion(a.IpVersion),
		DeletionProtection: a.DeletionProtection,
	}
	if a.ExternalIpv4 != nil {
		p.Address = &vpcv1.Address_ExternalIpv4Address{
			ExternalIpv4Address: &vpcv1.ExternalIpv4Address{
				Address: a.ExternalIpv4.Address,
				ZoneId:  a.ExternalIpv4.ZoneID,
			},
		}
	} else if a.InternalIpv4 != nil {
		p.Address = &vpcv1.Address_InternalIpv4Address{
			InternalIpv4Address: &vpcv1.InternalIpv4Address{
				Address: a.InternalIpv4.Address,
				Scope: &vpcv1.InternalIpv4Address_SubnetId{
					SubnetId: a.InternalIpv4.SubnetID,
				},
			},
		}
	}
	return p
}
