package handler

import (
	"context"
	"strings"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AddressHandler реализует pb.AddressServiceServer.
type AddressHandler struct {
	pb.UnimplementedAddressServiceServer
	svc *service.AddressService
}

// NewAddressHandler создаёт AddressHandler.
func NewAddressHandler(svc *service.AddressService) *AddressHandler {
	return &AddressHandler{svc: svc}
}

func (h *AddressHandler) Get(ctx context.Context, req *pb.GetAddressRequest) (*pb.Address, error) {
	a, err := h.svc.Get(ctx, req.GetAddressId())
	if err != nil {
		return nil, err
	}
	return addressDomainToProto(a), nil
}

func (h *AddressHandler) List(ctx context.Context, req *pb.ListAddressesRequest) (*pb.ListAddressesResponse, error) {
	filter := service.ListFilter{
		FolderID:  req.GetFolderId(),
		PageSize:  req.GetPageSize(),
		PageToken: req.GetPageToken(),
		Filter:    req.GetFilter(),
		OrderBy:   req.GetOrderBy(),
	}
	addresses, nextToken, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListAddressesResponse{NextPageToken: nextToken}
	for i := range addresses {
		resp.Addresses = append(resp.Addresses, addressDomainToProto(&addresses[i]))
	}
	return resp, nil
}

func (h *AddressHandler) Create(ctx context.Context, req *pb.CreateAddressRequest) (*operationv1.Operation, error) {
	addrType := addrTypeString(req.GetAddressType())
	op, err := h.svc.Create(ctx,
		req.GetFolderId(), req.GetName(), req.GetDescription(), req.GetLabels(),
		addrType, req.GetZoneId(),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *AddressHandler) Update(ctx context.Context, req *pb.UpdateAddressRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Update(ctx,
		req.GetAddressId(),
		req.GetName(), req.GetDescription(), req.GetLabels(),
		maskFields(req.GetUpdateMask()),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *AddressHandler) Delete(ctx context.Context, req *pb.DeleteAddressRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Delete(ctx, req.GetAddressId())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func addressDomainToProto(a *domain.Address) *pb.Address {
	proto := &pb.Address{
		Id:            a.ID,
		FolderId:      a.FolderID,
		Name:          a.Name,
		Description:   a.Description,
		Labels:        a.Labels,
		AddressType:   addrTypeProto(a.AddressType),
		ZoneId:        a.ZoneID,
		AllocatedIpv4: a.AllocatedIPv4,
		Status:        pb.AddressStatus(a.Status),
	}
	if !a.CreatedAt.IsZero() {
		proto.CreatedAt = timestamppb.New(a.CreatedAt)
	}
	return proto
}

func addrTypeProto(t string) pb.AddressType {
	if strings.Contains(strings.ToUpper(t), "EXTERNAL") {
		return pb.AddressType_ADDRESS_TYPE_EXTERNAL
	}
	return pb.AddressType_ADDRESS_TYPE_UNSPECIFIED
}

func addrTypeString(t pb.AddressType) string {
	if t == pb.AddressType_ADDRESS_TYPE_EXTERNAL {
		return "ADDRESS_TYPE_EXTERNAL"
	}
	return ""
}
