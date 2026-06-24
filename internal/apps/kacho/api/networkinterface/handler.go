// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует NetworkInterface/time DTO-трансферы.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// Handler — реализация vpcv1.NetworkInterfaceServiceServer на основе use-case'ов.
// Тонкий transport-слой: proto-request → domain → use-case → proto-response.
// Никакой бизнес-логики.
//
// NB: у NIC нет Move RPC (NIC привязан к Subnet, перемещение между project'ами
// не поддерживается). AttachToInstance / DetachFromInstance RPC тоже отсутствуют —
// NIC-ресурс остается, но used_by через RPC не выставляется.
type Handler struct {
	vpcv1.UnimplementedNetworkInterfaceServiceServer

	create         *CreateNetworkInterfaceUseCase
	update         *UpdateNetworkInterfaceUseCase
	delete         *DeleteNetworkInterfaceUseCase
	get            *GetNetworkInterfaceUseCase
	list           *ListNetworkInterfacesUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateNetworkInterfaceUseCase,
	update *UpdateNetworkInterfaceUseCase,
	deleteUC *DeleteNetworkInterfaceUseCase,
	get *GetNetworkInterfaceUseCase,
	list *ListNetworkInterfacesUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		delete:         deleteUC,
		get:            get,
		list:           list,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ + per-object no-leak.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetNetworkInterfaceRequest) (*vpcv1.NetworkInterface, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	subject := pbconv.SubjectFromContext(ctx)
	n, err := h.get.Execute(ctx, subject, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	return networkInterfaceToPb(n)
}

// List — project_id required + AuthZ + FGA list-filter.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListNetworkInterfacesRequest) (*vpcv1.ListNetworkInterfacesResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
	out, next, err := h.list.Execute(ctx, subject, NetworkInterfaceFilter{
		ProjectID:  req.ProjectId,
		InstanceID: req.InstanceId,
		SubnetID:   req.SubnetId,
		NetworkID:  req.NetworkId,
	}, Pagination{
		PageSize:  req.PageSize,
		PageToken: req.PageToken,
	})
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

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	in := CreateInput{
		NetworkInterface: domain.NetworkInterface{
			ProjectID:        req.ProjectId,
			Name:             domain.RcNameVPC(req.Name),
			Description:      domain.RcDescription(req.Description),
			Labels:           domain.LabelsFromMap(req.Labels),
			SubnetID:         req.SubnetId,
			V4AddressIDs:     req.V4AddressIds,
			V6AddressIDs:     req.V6AddressIds,
			SecurityGroupIDs: req.SecurityGroupIds,
		},
		InstanceID: req.InstanceId,
		Index:      req.Index,
	}
	op, err := h.create.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.get.Execute(ctx, "", req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, cur.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	in := UpdateInput{
		NetworkInterfaceID: req.NetworkInterfaceId,
		NetworkInterface: domain.NetworkInterface{
			Name:             domain.RcNameVPC(req.Name),
			Description:      domain.RcDescription(req.Description),
			Labels:           domain.LabelsFromMap(req.Labels),
			SecurityGroupIDs: req.SecurityGroupIds,
			V4AddressIDs:     req.V4AddressIds,
			V6AddressIDs:     req.V6AddressIds,
		},
		UpdateMask: mask,
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.get.Execute(ctx, "", req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, cur.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// ListOperations — best-effort AuthZ: ресурс жив → project-ownership проверяем;
// удален (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListNetworkInterfaceOperationsRequest) (*vpcv1.ListNetworkInterfaceOperationsResponse, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	if cur, gerr := h.get.Execute(ctx, "", req.NetworkInterfaceId); gerr == nil {
		if err := tenant.AssertProjectOwnership(ctx, cur.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, next, err := h.listOperations.Execute(ctx, req.NetworkInterfaceId, Pagination{
		PageSize:  req.PageSize,
		PageToken: req.PageToken,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkInterfaceOperationsResponse{NextPageToken: next}
	for i := range ops {
		resp.Operations = append(resp.Operations, pbconv.OperationToProto(&ops[i]))
	}
	return resp, nil
}

// networkInterfaceToPb — repo-entity NIC → proto NIC через DTO-реестр.
func networkInterfaceToPb(rec *kachorepo.NetworkInterfaceRecord) (*vpcv1.NetworkInterface, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer NetworkInterface failed")
	}
	return dst, nil
}
