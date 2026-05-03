package handler

import (
	"context"

	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// VpcInternalHandler реализует pb.VpcInternalServiceServer.
type VpcInternalHandler struct {
	pb.UnimplementedVpcInternalServiceServer
	networkSvc  *service.NetworkService
	subnetSvc   *service.SubnetService
	sgSvc       *service.SecurityGroupService
	rtSvc       *service.RouteTableService
	addressSvc  *service.AddressService
}

// NewVpcInternalHandler создаёт VpcInternalHandler.
func NewVpcInternalHandler(
	networkSvc *service.NetworkService,
	subnetSvc *service.SubnetService,
	sgSvc *service.SecurityGroupService,
	rtSvc *service.RouteTableService,
	addressSvc *service.AddressService,
) *VpcInternalHandler {
	return &VpcInternalHandler{
		networkSvc: networkSvc,
		subnetSvc:  subnetSvc,
		sgSvc:      sgSvc,
		rtSvc:      rtSvc,
		addressSvc: addressSvc,
	}
}

// NetworkExists проверяет существование сети по UID.
func (h *VpcInternalHandler) NetworkExists(ctx context.Context, req *pb.NetworkExistsRequest) (*pb.ExistsResponse, error) {
	net, err := h.networkSvc.GetByUID(ctx, req.GetUid())
	if err != nil {
		return &pb.ExistsResponse{Exists: false}, nil //nolint:nilerr
	}
	if net == nil || net.DeletionTimestamp != nil {
		return &pb.ExistsResponse{Exists: false}, nil
	}
	return &pb.ExistsResponse{Exists: true}, nil
}

// SubnetExists проверяет существование подсети по UID.
func (h *VpcInternalHandler) SubnetExists(ctx context.Context, req *pb.SubnetExistsRequest) (*pb.ExistsResponse, error) {
	subnet, err := h.subnetSvc.GetByUID(ctx, req.GetUid())
	if err != nil {
		return &pb.ExistsResponse{Exists: false}, nil //nolint:nilerr
	}
	if subnet == nil || subnet.DeletionTimestamp != nil {
		return &pb.ExistsResponse{Exists: false}, nil
	}
	return &pb.ExistsResponse{Exists: true}, nil
}

// SecurityGroupExists проверяет существование группы безопасности по UID.
func (h *VpcInternalHandler) SecurityGroupExists(ctx context.Context, req *pb.SecurityGroupExistsRequest) (*pb.ExistsResponse, error) {
	sg, err := h.sgSvc.GetByUID(ctx, req.GetUid())
	if err != nil {
		return &pb.ExistsResponse{Exists: false}, nil //nolint:nilerr
	}
	if sg == nil || sg.DeletionTimestamp != nil {
		return &pb.ExistsResponse{Exists: false}, nil
	}
	return &pb.ExistsResponse{Exists: true}, nil
}

// RouteTableExists проверяет существование таблицы маршрутизации по UID.
func (h *VpcInternalHandler) RouteTableExists(ctx context.Context, req *pb.RouteTableExistsRequest) (*pb.ExistsResponse, error) {
	rt, err := h.rtSvc.GetByUID(ctx, req.GetUid())
	if err != nil {
		return &pb.ExistsResponse{Exists: false}, nil //nolint:nilerr
	}
	if rt == nil || rt.DeletionTimestamp != nil {
		return &pb.ExistsResponse{Exists: false}, nil
	}
	return &pb.ExistsResponse{Exists: true}, nil
}

// AddressExists проверяет существование адреса по UID.
func (h *VpcInternalHandler) AddressExists(ctx context.Context, req *pb.AddressExistsRequest) (*pb.ExistsResponse, error) {
	addr, err := h.addressSvc.GetByUID(ctx, req.GetUid())
	if err != nil {
		return &pb.ExistsResponse{Exists: false}, nil //nolint:nilerr
	}
	if addr == nil || addr.DeletionTimestamp != nil {
		return &pb.ExistsResponse{Exists: false}, nil
	}
	return &pb.ExistsResponse{Exists: true}, nil
}

// NetworkHasDependents проверяет, есть ли у сети дочерние ресурсы.
func (h *VpcInternalHandler) NetworkHasDependents(ctx context.Context, req *pb.HasDependentsRequest) (*pb.HasDependentsResponse, error) {
	has, kinds, err := h.networkSvc.HasDependents(ctx, req.GetUid())
	if err != nil {
		return nil, err
	}
	return &pb.HasDependentsResponse{HasDependents: has, Kinds: kinds}, nil
}

// SubnetHasDependents проверяет, есть ли у подсети дочерние ресурсы.
func (h *VpcInternalHandler) SubnetHasDependents(ctx context.Context, req *pb.HasDependentsRequest) (*pb.HasDependentsResponse, error) {
	has, kinds, err := h.subnetSvc.HasDependents(ctx, req.GetUid())
	if err != nil {
		return nil, err
	}
	return &pb.HasDependentsResponse{HasDependents: has, Kinds: kinds}, nil
}

// SecurityGroupHasDependents проверяет, есть ли у SG дочерние ресурсы.
func (h *VpcInternalHandler) SecurityGroupHasDependents(ctx context.Context, req *pb.HasDependentsRequest) (*pb.HasDependentsResponse, error) {
	has, kinds, err := h.sgSvc.HasDependents(ctx, req.GetUid())
	if err != nil {
		return nil, err
	}
	return &pb.HasDependentsResponse{HasDependents: has, Kinds: kinds}, nil
}

// RouteTableHasDependents проверяет, есть ли у RT дочерние ресурсы.
func (h *VpcInternalHandler) RouteTableHasDependents(ctx context.Context, req *pb.HasDependentsRequest) (*pb.HasDependentsResponse, error) {
	has, kinds, err := h.rtSvc.HasDependents(ctx, req.GetUid())
	if err != nil {
		return nil, err
	}
	return &pb.HasDependentsResponse{HasDependents: has, Kinds: kinds}, nil
}

// AddressHasDependents проверяет, есть ли у адреса дочерние ресурсы.
func (h *VpcInternalHandler) AddressHasDependents(ctx context.Context, req *pb.HasDependentsRequest) (*pb.HasDependentsResponse, error) {
	has, kinds, err := h.addressSvc.HasDependents(ctx, req.GetUid())
	if err != nil {
		return nil, err
	}
	return &pb.HasDependentsResponse{HasDependents: has, Kinds: kinds}, nil
}

// UpdateAddressStatus обновляет статус адреса (вызывается из compute-сервиса).
func (h *VpcInternalHandler) UpdateAddressStatus(ctx context.Context, req *pb.AddressUpdateStatusRequest) (*pb.AddressUpdateStatusResponse, error) {
	if err := h.addressSvc.UpdateStatus(ctx, req.GetUid(), req.GetState()); err != nil {
		return nil, err
	}
	return &pb.AddressUpdateStatusResponse{}, nil
}
