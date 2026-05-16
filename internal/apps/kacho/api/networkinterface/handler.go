package networkinterface

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует NetworkInterface/time DTO трансферы (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Handler — реализация vpcv1.NetworkInterfaceServiceServer на основе use-case'ов
// (skill evgeniy §2). Тонкий transport-слой: proto-request → domain → use-case
// → proto-response. Никакой бизнес-логики.
//
// NB: у NIC **нет Move RPC** (NIC привязан к Subnet — verbatim YC API не
// поддерживает перемещение между folder'ами). Есть специфические `AttachToInstance` /
// `DetachFromInstance` (атомарный CAS из миграции 0016, KAC-52).
type Handler struct {
	vpcv1.UnimplementedNetworkInterfaceServiceServer

	create         *CreateNetworkInterfaceUseCase
	update         *UpdateNetworkInterfaceUseCase
	delete         *DeleteNetworkInterfaceUseCase
	get            *GetNetworkInterfaceUseCase
	list           *ListNetworkInterfacesUseCase
	attach         *AttachToInstanceUseCase
	detach         *DetachFromInstanceUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateNetworkInterfaceUseCase,
	update *UpdateNetworkInterfaceUseCase,
	deleteUC *DeleteNetworkInterfaceUseCase,
	get *GetNetworkInterfaceUseCase,
	list *ListNetworkInterfacesUseCase,
	attach *AttachToInstanceUseCase,
	detach *DetachFromInstanceUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		delete:         deleteUC,
		get:            get,
		list:           list,
		attach:         attach,
		detach:         detach,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetNetworkInterfaceRequest) (*vpcv1.NetworkInterface, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	n, err := h.get.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	return networkInterfaceToPb(n)
}

// List — folder_id required + AuthZ.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListNetworkInterfacesRequest) (*vpcv1.ListNetworkInterfacesResponse, error) {
	if err := handler.AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	out, next, err := h.list.Execute(ctx, NetworkInterfaceFilter{
		FolderID:   req.FolderId,
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
	if err := handler.AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	in := CreateInput{
		NetworkInterface: domain.NetworkInterface{
			FolderID:         req.FolderId,
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
	return operationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.get.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, cur.FolderID); err != nil {
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
	return operationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.get.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// AttachToInstance — sync repo.Get для AuthZ, затем use-case с atomic CAS.
func (h *Handler) AttachToInstance(ctx context.Context, req *vpcv1.AttachNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.get.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	op, err := h.attach.Execute(ctx, req.NetworkInterfaceId, req.InstanceId, req.Index)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// DetachFromInstance — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) DetachFromInstance(ctx context.Context, req *vpcv1.DetachNetworkInterfaceRequest) (*operationpb.Operation, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	cur, err := h.get.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, cur.FolderID); err != nil {
		return nil, err
	}
	op, err := h.detach.Execute(ctx, req.NetworkInterfaceId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// ListOperations — best-effort AuthZ: ресурс жив → folder-ownership проверяем;
// удалён (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListNetworkInterfaceOperationsRequest) (*vpcv1.ListNetworkInterfaceOperationsResponse, error) {
	if req.NetworkInterfaceId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	if cur, gerr := h.get.Execute(ctx, req.NetworkInterfaceId); gerr == nil {
		if err := handler.AssertFolderOwnership(ctx, cur.FolderID); err != nil {
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
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// networkInterfaceToPb — repo-entity NIC → proto NIC через DTO-реестр.
// Wave 5 replicate (KAC-94, NIC batch): принимает `*kacho.NetworkInterfaceRecord`
// (repo-entity переехала из domain в repo-leaf).
func networkInterfaceToPb(rec *kachorepo.NetworkInterfaceRecord) (*vpcv1.NetworkInterface, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer NetworkInterface failed")
	}
	return dst, nil
}

// operationToProto — локальная копия `handler.operationToProto`. При полном
// переезде use-case'ов в `internal/apps/kacho` вынесем общий helper в shared-leaf.
func operationToProto(op *operations.Operation) *operationpb.Operation {
	p := &operationpb.Operation{
		Id:          op.ID,
		Description: op.Description,
		CreatedAt:   timestamppb.New(op.CreatedAt),
		CreatedBy:   op.CreatedBy,
		ModifiedAt:  timestamppb.New(op.ModifiedAt),
		Done:        op.Done,
		Metadata:    op.Metadata,
	}
	if op.Error != nil {
		p.Result = &operationpb.Operation_Error{Error: op.Error}
	} else if op.Response != nil {
		p.Result = &operationpb.Operation_Response{Response: op.Response}
	}
	return p
}
