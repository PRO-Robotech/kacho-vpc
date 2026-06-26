// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует Network/time DTO трансферы через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// Handler — реализация vpcv1.NetworkServiceServer на основе use-case'ов. Тонкий
// transport-слой: proto-request → domain → use-case → proto-response. Никакой
// бизнес-логики.
type Handler struct {
	vpcv1.UnimplementedNetworkServiceServer

	create            *CreateNetworkUseCase
	update            *UpdateNetworkUseCase
	delete            *DeleteNetworkUseCase
	get               *GetNetworkUseCase
	list              *ListNetworksUseCase
	listSubnets       *ListSubnetsUseCase
	listSecurityGroup *ListSecurityGroupsUseCase
	listRouteTables   *ListRouteTablesUseCase
	listOperations    *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов. Конструктор намеренно
// принимает все use-case'ы — composition-root (cmd/vpc/main.go) собирает их
// с одинаковыми зависимостями (repo / projectClient / opsRepo).
func NewHandler(
	create *CreateNetworkUseCase,
	update *UpdateNetworkUseCase,
	deleteUC *DeleteNetworkUseCase,
	get *GetNetworkUseCase,
	list *ListNetworksUseCase,
	listSubnets *ListSubnetsUseCase,
	listSG *ListSecurityGroupsUseCase,
	listRT *ListRouteTablesUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:            create,
		update:            update,
		delete:            deleteUC,
		get:               get,
		list:              list,
		listSubnets:       listSubnets,
		listSecurityGroup: listSG,
		listRouteTables:   listRT,
		listOperations:    listOps,
	}
}

// Get — sync read + AuthZ + per-object no-leak.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetNetworkRequest) (*vpcv1.Network, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	subject := pbconv.SubjectFromContext(ctx)
	n, err := h.get.Execute(ctx, subject, req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	return networkToPb(n)
}

// List — project_id required + AuthZ + FGA list-filter.
//
// Subject из ctx (principal-extractor) → ListAllowedIDs → repo.ListByIDs.
// AssertProjectOwnership остается как defense-in-depth pre-check (FGA listing
// scoped to project; пользователь не должен слать запрос на чужой project).
func (h *Handler) List(ctx context.Context, req *vpcv1.ListNetworksRequest) (*vpcv1.ListNetworksResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
	nets, nextToken, err := h.list.Execute(ctx, subject, NetworkFilter{
		ProjectID: req.ProjectId,
		Filter:    req.Filter,
	}, Pagination{
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

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateNetworkRequest) (*operationpb.Operation, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	n := domain.Network{
		ProjectID:   req.ProjectId,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	op, err := h.create.Execute(ctx, n)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateNetworkRequest) (*operationpb.Operation, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.get.Execute(ctx, "", req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	in := UpdateInput{
		NetworkID: req.NetworkId,
		Network: domain.Network{
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
		},
		UpdateMask: mask,
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// ListSubnets — child list; caller обязан владеть parent network'ом.
func (h *Handler) ListSubnets(ctx context.Context, req *vpcv1.ListNetworkSubnetsRequest) (*vpcv1.ListNetworkSubnetsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.get.Execute(ctx, "", req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	subs, nextToken, err := h.listSubnets.Execute(ctx, req.NetworkId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkSubnetsResponse{NextPageToken: nextToken}
	for _, s := range subs {
		pb, err := subnetToPb(s)
		if err != nil {
			return nil, err
		}
		resp.Subnets = append(resp.Subnets, pb)
	}
	return resp, nil
}

// ListSecurityGroups — child list; caller обязан владеть parent network'ом.
func (h *Handler) ListSecurityGroups(ctx context.Context, req *vpcv1.ListNetworkSecurityGroupsRequest) (*vpcv1.ListNetworkSecurityGroupsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.get.Execute(ctx, "", req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	sgs, nextToken, err := h.listSecurityGroup.Execute(ctx, req.NetworkId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkSecurityGroupsResponse{NextPageToken: nextToken}
	for _, sg := range sgs {
		pb, err := securityGroupToPb(sg)
		if err != nil {
			return nil, err
		}
		resp.SecurityGroups = append(resp.SecurityGroups, pb)
	}
	return resp, nil
}

// ListRouteTables — child list; caller обязан владеть parent network'ом.
func (h *Handler) ListRouteTables(ctx context.Context, req *vpcv1.ListNetworkRouteTablesRequest) (*vpcv1.ListNetworkRouteTablesResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.get.Execute(ctx, "", req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	rts, nextToken, err := h.listRouteTables.Execute(ctx, req.NetworkId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkRouteTablesResponse{NextPageToken: nextToken}
	for _, rt := range rts {
		pb, err := routeTableToPb(rt)
		if err != nil {
			return nil, err
		}
		resp.RouteTables = append(resp.RouteTables, pb)
	}
	return resp, nil
}

// ListOperations — best-effort AuthZ: ресурс жив → project-ownership проверяем;
// удален (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListNetworkOperationsRequest) (*vpcv1.ListNetworkOperationsResponse, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if n, gerr := h.get.Execute(ctx, "", req.NetworkId); gerr == nil {
		if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.NetworkId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListNetworkOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, pbconv.OperationToProto(&ops[i]))
	}
	return resp, nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteNetworkRequest) (*operationpb.Operation, error) {
	if req.NetworkId == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.get.Execute(ctx, "", req.NetworkId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, n.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.NetworkId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// networkToPb — repo-entity Network → proto Network через DTO-реестр.
func networkToPb(rec *kachorepo.NetworkRecord) (*vpcv1.Network, error) {
	var dst *vpcv1.Network
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Network failed")
	}
	return dst, nil
}

// subnetToPb / routeTableToPb / securityGroupToPb — repo-entity child-resource
// → proto. Reuse уже зарегистрированных DTO-трансферов из `internal/dto/toproto`
// (blank-import выше).
func subnetToPb(rec *kachorepo.SubnetRecord) (*vpcv1.Subnet, error) {
	var dst *vpcv1.Subnet
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Subnet failed")
	}
	return dst, nil
}

func routeTableToPb(rec *kachorepo.RouteTableRecord) (*vpcv1.RouteTable, error) {
	var dst *vpcv1.RouteTable
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer RouteTable failed")
	}
	return dst, nil
}

func securityGroupToPb(rec *kachorepo.SecurityGroupRecord) (*vpcv1.SecurityGroup, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer SecurityGroup failed")
	}
	return dst, nil
}
