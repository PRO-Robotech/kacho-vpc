package routetable

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

	// Blank-import регистрирует RouteTable/time DTO трансферы (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Handler — реализация vpcv1.RouteTableServiceServer на основе use-case'ов.
type Handler struct {
	vpcv1.UnimplementedRouteTableServiceServer

	create         *CreateRouteTableUseCase
	update         *UpdateRouteTableUseCase
	delete         *DeleteRouteTableUseCase
	move           *MoveRouteTableUseCase
	get            *GetRouteTableUseCase
	list           *ListRouteTablesUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreateRouteTableUseCase,
	update *UpdateRouteTableUseCase,
	deleteUC *DeleteRouteTableUseCase,
	move *MoveRouteTableUseCase,
	get *GetRouteTableUseCase,
	list *ListRouteTablesUseCase,
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
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetRouteTableRequest) (*vpcv1.RouteTable, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.get.Execute(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	return routeTableToPb(rt)
}

// List — folder_id required + AuthZ.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListRouteTablesRequest) (*vpcv1.ListRouteTablesResponse, error) {
	if err := handler.AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	rts, nextToken, err := h.list.Execute(ctx, RouteTableFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		pb, err := routeTableToPb(rt)
		if err != nil {
			return nil, err
		}
		resp.RouteTables = append(resp.RouteTables, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateRouteTableRequest) (*operationpb.Operation, error) {
	if err := handler.AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	rt := domain.RouteTable{
		FolderID:    req.FolderId,
		NetworkID:   req.NetworkId,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	for _, sr := range req.StaticRoutes {
		route := domain.StaticRoute{
			Labels: sr.Labels,
		}
		if sr.GetDestinationPrefix() != "" {
			route.DestinationPrefix = sr.GetDestinationPrefix()
		}
		if sr.GetNextHopAddress() != "" {
			route.NextHopAddress = sr.GetNextHopAddress()
		}
		rt.StaticRoutes = append(rt.StaticRoutes, route)
	}
	op, err := h.create.Execute(ctx, CreateInput{RouteTable: rt})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.get.Execute(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	in := UpdateInput{
		RouteTableID: req.RouteTableId,
		RouteTable: domain.RouteTable{
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
		},
		UpdateMask: mask,
	}
	for _, sr := range req.StaticRoutes {
		route := domain.StaticRoute{
			Labels: sr.Labels,
		}
		if sr.GetDestinationPrefix() != "" {
			route.DestinationPrefix = sr.GetDestinationPrefix()
		}
		if sr.GetNextHopAddress() != "" {
			route.NextHopAddress = sr.GetNextHopAddress()
		}
		in.RouteTable.StaticRoutes = append(in.RouteTable.StaticRoutes, route)
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.get.Execute(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Move — sync repo.Get для AuthZ источника, AssertFolderOwnership на dest, затем
// use-case.
func (h *Handler) Move(ctx context.Context, req *vpcv1.MoveRouteTableRequest) (*operationpb.Operation, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	rt, err := h.get.Execute(ctx, req.RouteTableId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, rt.FolderID); err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.move.Execute(ctx, req.RouteTableId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// ListOperations — best-effort AuthZ.
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListRouteTableOperationsRequest) (*vpcv1.ListRouteTableOperationsResponse, error) {
	if req.RouteTableId == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	if rt, gerr := h.get.Execute(ctx, req.RouteTableId); gerr == nil {
		if err := handler.AssertFolderOwnership(ctx, rt.FolderID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.RouteTableId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListRouteTableOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// routeTableToPb — repo-entity RouteTable → proto RouteTable через DTO-реестр.
// Wave 5 replicate (KAC-94): принимает `*kachorepo.RouteTableRecord` вместо
// `*domain.RouteTableRecord` (запись переехала в repo-leaf, §4 D.1).
func routeTableToPb(rec *kachorepo.RouteTableRecord) (*vpcv1.RouteTable, error) {
	var dst *vpcv1.RouteTable
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer RouteTable failed")
	}
	return dst, nil
}

// operationToProto — локальная копия `handler.operationToProto`.
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
