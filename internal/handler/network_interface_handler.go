package handler

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// type2pb регистрирует DTO-трансферы в init() — нужны для dto.Transfer.
	// Skill evgeniy §3 C.4.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// networkInterfaceToPb конвертирует repo-entity NIC → proto NIC через DTO-реестр.
// Wave 2 batch C (KAC-94): protoconv.NetworkInterface удалён (skill evgeniy §3 C.6 / AP-11).
func networkInterfaceToPb(rec *domain.NetworkInterfaceRecord) (*vpcv1.NetworkInterface, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer NetworkInterface: %w", err)
	}
	return dst, nil
}

// NetworkInterfaceHandler реализует vpcv1.NetworkInterfaceServiceServer.
type NetworkInterfaceHandler struct {
	vpcv1.UnimplementedNetworkInterfaceServiceServer
	svc *svc.NetworkInterfaceService
}

// NewNetworkInterfaceHandler создаёт NetworkInterfaceHandler.
func NewNetworkInterfaceHandler(s *svc.NetworkInterfaceService) *NetworkInterfaceHandler {
	return &NetworkInterfaceHandler{svc: s}
}

func (h *NetworkInterfaceHandler) Get(ctx context.Context, req *vpcv1.GetNetworkInterfaceRequest) (*vpcv1.NetworkInterface, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	n, err := h.svc.Get(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	return networkInterfaceToPb(n)
}

func (h *NetworkInterfaceHandler) List(ctx context.Context, req *vpcv1.ListNetworkInterfacesRequest) (*vpcv1.ListNetworkInterfacesResponse, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	out, next, err := h.svc.List(ctx, svc.NetworkInterfaceFilter{
		FolderID: req.FolderId, InstanceID: req.InstanceId, SubnetID: req.SubnetId, NetworkID: req.NetworkId,
	}, svc.Pagination{PageSize: req.PageSize, PageToken: req.PageToken})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkInterfacesResponse{NextPageToken: next}
	for _, n := range out {
		pb, err := networkInterfaceToPb(n)
		if err != nil {
			return nil, err
		}
		resp.NetworkInterfaces = append(resp.NetworkInterfaces, pb)
	}
	return resp, nil
}

func (h *NetworkInterfaceHandler) Create(ctx context.Context, req *vpcv1.CreateNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Create(ctx, svc.CreateNICReq{
		FolderID:         req.FolderId,
		Name:             req.Name,
		Description:      req.Description,
		Labels:           req.Labels,
		SubnetID:         req.SubnetId,
		V4AddressIDs:     req.V4AddressIds,
		V6AddressIDs:     req.V6AddressIds,
		SecurityGroupIDs: req.SecurityGroupIds,
		InstanceID:       req.InstanceId,
		Index:            req.Index,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkInterfaceHandler) Update(ctx context.Context, req *vpcv1.UpdateNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.svc.Get(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.svc.Update(ctx, svc.UpdateNICReq{
		ID: req.NetworkInterfaceId, Name: req.Name, Description: req.Description, Labels: req.Labels,
		SecurityGroupIDs: req.SecurityGroupIds, V4AddressIDs: req.V4AddressIds, V6AddressIDs: req.V6AddressIds, UpdateMask: mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkInterfaceHandler) Delete(ctx context.Context, req *vpcv1.DeleteNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.svc.Get(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkInterfaceHandler) AttachToInstance(ctx context.Context, req *vpcv1.AttachNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.svc.Get(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.AttachToInstance(ctx, req.NetworkInterfaceId, req.InstanceId, req.Index)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkInterfaceHandler) DetachFromInstance(ctx context.Context, req *vpcv1.DetachNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.svc.Get(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.DetachFromInstance(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkInterfaceHandler) ListOperations(ctx context.Context, req *vpcv1.ListNetworkInterfaceOperationsRequest) (*vpcv1.ListNetworkInterfaceOperationsResponse, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	// ListOperations должен работать и после удаления ресурса (история операций).
	// Get — best-effort: ресурс жив → проверяем folder-ownership; удалён (NotFound)
	// → пропускаем проверку и отдаём накопленные операции.
	if cur, gerr := h.svc.Get(ctx, req.NetworkInterfaceId); gerr == nil {
		if err := AssertFolderOwnership(ctx, cur.FolderID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, next, err := h.svc.ListOperations(ctx, req.NetworkInterfaceId, svc.Pagination{PageSize: req.PageSize, PageToken: req.PageToken})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkInterfaceOperationsResponse{NextPageToken: next}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}
