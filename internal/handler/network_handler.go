package handler

import (
	"context"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NetworkHandler реализует pb.NetworkServiceServer.
type NetworkHandler struct {
	pb.UnimplementedNetworkServiceServer
	svc *service.NetworkService
}

// NewNetworkHandler создаёт NetworkHandler.
func NewNetworkHandler(svc *service.NetworkService) *NetworkHandler {
	return &NetworkHandler{svc: svc}
}

func (h *NetworkHandler) Get(ctx context.Context, req *pb.GetNetworkRequest) (*pb.Network, error) {
	n, err := h.svc.Get(ctx, req.GetNetworkId())
	if err != nil {
		return nil, err
	}
	return networkDomainToProto(n), nil
}

func (h *NetworkHandler) List(ctx context.Context, req *pb.ListNetworksRequest) (*pb.ListNetworksResponse, error) {
	filter := service.ListFilter{
		FolderID:  req.GetFolderId(),
		PageSize:  req.GetPageSize(),
		PageToken: req.GetPageToken(),
		Filter:    req.GetFilter(),
		OrderBy:   req.GetOrderBy(),
	}
	networks, nextToken, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListNetworksResponse{
		NextPageToken: nextToken,
	}
	for i := range networks {
		resp.Networks = append(resp.Networks, networkDomainToProto(&networks[i]))
	}
	return resp, nil
}

func (h *NetworkHandler) Create(ctx context.Context, req *pb.CreateNetworkRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Create(ctx, req.GetFolderId(), req.GetName(), req.GetDescription(), req.GetLabels())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) Update(ctx context.Context, req *pb.UpdateNetworkRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Update(ctx,
		req.GetNetworkId(),
		req.GetResourceVersion(),
		req.GetName(),
		req.GetDescription(),
		req.GetLabels(),
		maskFields(req.GetUpdateMask()),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *NetworkHandler) Delete(ctx context.Context, req *pb.DeleteNetworkRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Delete(ctx, req.GetNetworkId())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// networkDomainToProto конвертирует domain.Network в pb.Network.
func networkDomainToProto(n *domain.Network) *pb.Network {
	proto := &pb.Network{
		Id:              n.ID,
		FolderId:        n.FolderID,
		Name:            n.Name,
		Description:     n.Description,
		Labels:          n.Labels,
		Status:          pb.NetworkStatus(n.Status),
		Generation:      n.Generation,
		ResourceVersion: n.ResourceVersion,
	}
	if !n.CreatedAt.IsZero() {
		proto.CreatedAt = timestamppb.New(n.CreatedAt)
	}
	if !n.StatusLastTransitionAt.IsZero() {
		proto.StatusLastTransitionAt = timestamppb.New(n.StatusLastTransitionAt)
	}
	return proto
}
