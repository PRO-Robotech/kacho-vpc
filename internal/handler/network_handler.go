package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// Blank-import регистрирует Network/time DTO трансферы; см. service/network.go.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// networkToPb формирует *vpcv1.Network из repo-entity через DTO-реестр.
// Wave 2 pilot (KAC-99/KAC-94): handler больше не зовёт protoconv.Network(...)
// для Network — это место единственного fan-out через DTO. Остальные ресурсы
// в этом handler-е (Subnet/SecurityGroup/RouteTable) по-прежнему через
// protoconv.X до их Wave 2 итераций.
func networkToPb(rec *domain.NetworkRecord) (*vpcv1.Network, error) {
	var dst *vpcv1.Network
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Network failed")
	}
	return dst, nil
}

// NetworkHandler реализует vpcv1.NetworkServiceServer.
type NetworkHandler struct {
	vpcv1.UnimplementedNetworkServiceServer
	svc *svc.NetworkService
}

// NewNetworkHandler создаёт NetworkHandler.
func NewNetworkHandler(s *svc.NetworkService) *NetworkHandler {
	return &NetworkHandler{svc: s}
}

func (h *NetworkHandler) Get(ctx context.Context, req *vpcv1.GetNetworkRequest) (*vpcv1.Network, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	// AuthZ: caller обязан иметь access к folder, владеющему ресурсом.
	// При AuthN-noop (anonymous) HasFolderAccess возвращает true (empty
	// FolderIDs = full access) — backward compat. С IAM token caller
	// получит PermissionDenied для cross-folder.
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	return networkToPb(n)
}

func (h *NetworkHandler) List(ctx context.Context, req *vpcv1.ListNetworksRequest) (*vpcv1.ListNetworksResponse, error) {
	// AuthZ: caller обязан иметь access к запрошенному folder. Anonymous
	// (FolderIDs={}) пропускается; AuthN tenant без access → PermissionDenied.
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	nets, nextToken, err := h.svc.List(ctx, svc.NetworkFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworksResponse{NextPageToken: nextToken}
	for _, n := range nets {
		pb, err := networkToPb(n)
		if err != nil {
			return nil, err
		}
		resp.Networks = append(resp.Networks, pb)
	}
	return resp, nil
}

func (h *NetworkHandler) Create(ctx context.Context, req *vpcv1.CreateNetworkRequest) (*operationpb.Operation, error) {
	// AuthZ: caller обязан иметь access к destination folder.
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Create(ctx, svc.CreateNetworkReq{
		FolderID:    req.FolderId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) Update(ctx context.Context, req *vpcv1.UpdateNetworkRequest) (*operationpb.Operation, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// AuthZ: sync repo.Get + folder check до старта Operation.
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.svc.Update(ctx, svc.UpdateNetworkReq{
		NetworkID:   req.NetworkId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		UpdateMask:  mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) ListSubnets(ctx context.Context, req *vpcv1.ListNetworkSubnetsRequest) (*vpcv1.ListNetworkSubnetsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// AuthZ: child list — caller обязан владеть parent network'ом.
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	subs, nextToken, err := h.svc.ListSubnets(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkSubnetsResponse{NextPageToken: nextToken}
	for _, s := range subs {
		resp.Subnets = append(resp.Subnets, protoconv.Subnet(s))
	}
	return resp, nil
}

func (h *NetworkHandler) ListSecurityGroups(ctx context.Context, req *vpcv1.ListNetworkSecurityGroupsRequest) (*vpcv1.ListNetworkSecurityGroupsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	sgs, nextToken, err := h.svc.ListSecurityGroups(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkSecurityGroupsResponse{NextPageToken: nextToken}
	for _, sg := range sgs {
		resp.SecurityGroups = append(resp.SecurityGroups, protoconv.SecurityGroup(sg))
	}
	return resp, nil
}

func (h *NetworkHandler) ListRouteTables(ctx context.Context, req *vpcv1.ListNetworkRouteTablesRequest) (*vpcv1.ListNetworkRouteTablesResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	rts, nextToken, err := h.svc.ListRouteTables(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		resp.RouteTables = append(resp.RouteTables, protoconv.RouteTable(rt))
	}
	return resp, nil
}

func (h *NetworkHandler) ListOperations(ctx context.Context, req *vpcv1.ListNetworkOperationsRequest) (*vpcv1.ListNetworkOperationsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// ListOperations должен работать и после удаления ресурса (история операций).
	// Get — best-effort: ресурс жив → проверяем folder-ownership; удалён (NotFound)
	// → пропускаем проверку и отдаём накопленные операции.
	if n, gerr := h.svc.Get(ctx, req.NetworkId); gerr == nil {
		if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.NetworkId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

func (h *NetworkHandler) Move(ctx context.Context, req *vpcv1.MoveNetworkRequest) (*operationpb.Operation, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// AuthZ: caller должен владеть и source-folder'ом (текущим), и destination'ом.
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Move(ctx, req.NetworkId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) Delete(ctx context.Context, req *vpcv1.DeleteNetworkRequest) (*operationpb.Operation, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.svc.Get(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, n.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// networkToProto конвертирует domain Network → proto Network.
//
// CreatedAt — truncate до seconds для verbatim YC parity (resource.createdAt
// в YC всегда seconds-precision). См. YC-DIFF-TIMESTAMP-PRECISION.md.
