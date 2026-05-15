package gateway

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
	// Blank-import регистрирует Gateway/time DTO трансферы (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
)

// Handler — реализация vpcv1.GatewayServiceServer на основе use-case'ов
// (skill evgeniy §2). Тонкий transport-слой: proto-request → domain → use-case
// → proto-response. Никакой бизнес-логики.
type Handler struct {
	vpcv1.UnimplementedGatewayServiceServer

	create         *CreateGatewayUseCase
	update         *UpdateGatewayUseCase
	delete         *DeleteGatewayUseCase
	move           *MoveGatewayUseCase
	get            *GetGatewayUseCase
	list           *ListGatewaysUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateGatewayUseCase,
	update *UpdateGatewayUseCase,
	deleteUC *DeleteGatewayUseCase,
	move *MoveGatewayUseCase,
	get *GetGatewayUseCase,
	list *ListGatewaysUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		delete:         deleteUC,
		move:           move,
		get:            get,
		list:           list,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetGatewayRequest) (*vpcv1.Gateway, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.get.Execute(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	return gatewayToPb(g)
}

// List — folder_id required + AuthZ.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListGatewaysRequest) (*vpcv1.ListGatewaysResponse, error) {
	if err := handler.AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	gws, nextToken, err := h.list.Execute(ctx, GatewayFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListGatewaysResponse{NextPageToken: nextToken}
	for _, g := range gws {
		pb, err := gatewayToPb(g)
		if err != nil {
			return nil, err
		}
		resp.Gateways = append(resp.Gateways, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateGatewayRequest) (*operationpb.Operation, error) {
	if err := handler.AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	gtype := ""
	if _, ok := req.Gateway.(*vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec); ok {
		gtype = "shared_egress"
	}
	in := CreateInput{
		Gateway: domain.Gateway{
			FolderID:    req.FolderId,
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
			GatewayType: domain.GatewayType(gtype),
		},
	}
	op, err := h.create.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.get.Execute(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	gtype := ""
	if _, ok := req.Gateway.(*vpcv1.UpdateGatewayRequest_SharedEgressGatewaySpec); ok {
		gtype = "shared_egress"
	}
	in := UpdateInput{
		GatewayID: req.GatewayId,
		Gateway: domain.Gateway{
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
			GatewayType: domain.GatewayType(gtype),
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
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.get.Execute(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Move — sync repo.Get для AuthZ источника, AssertFolderOwnership на dest, затем
// use-case.
func (h *Handler) Move(ctx context.Context, req *vpcv1.MoveGatewayRequest) (*operationpb.Operation, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	g, err := h.get.Execute(ctx, req.GatewayId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, g.FolderID); err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.move.Execute(ctx, req.GatewayId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// ListOperations — best-effort AuthZ: ресурс жив → folder-ownership проверяем;
// удалён (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListGatewayOperationsRequest) (*vpcv1.ListGatewayOperationsResponse, error) {
	if req.GatewayId == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if g, gerr := h.get.Execute(ctx, req.GatewayId); gerr == nil {
		if err := handler.AssertFolderOwnership(ctx, g.FolderID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.GatewayId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListGatewayOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// gatewayToPb — repo-entity Gateway → proto Gateway через DTO-реестр (skill
// evgeniy §3 C.3).
func gatewayToPb(rec *domain.GatewayRecord) (*vpcv1.Gateway, error) {
	var dst *vpcv1.Gateway
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Gateway failed")
	}
	return dst, nil
}

// operationToProto — локальная копия `handler.operationToProto` (та lowercase).
// При полном переезде use-case'ов из `internal/service` вынесем общий helper в
// shared-leaf.
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
