package handler

import (
	"context"
	"errors"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/common/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/watch"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// NetworkHandler реализует pb.NetworkServiceServer.
type NetworkHandler struct {
	pb.UnimplementedNetworkServiceServer
	svc *service.NetworkService
	hub *watch.Hub
}

// NewNetworkHandler создаёт NetworkHandler.
func NewNetworkHandler(svc *service.NetworkService, hub *watch.Hub) *NetworkHandler {
	return &NetworkHandler{svc: svc, hub: hub}
}

func (h *NetworkHandler) Upsert(ctx context.Context, req *pb.NetworkUpsertRequest) (*pb.NetworkUpsertResponse, error) {
	resp := &pb.NetworkUpsertResponse{}
	for _, in := range req.GetNetworks() {
		net := protoNetworkToDomain(in)
		result, err := h.svc.Upsert(ctx, net)
		if err != nil {
			return nil, err
		}
		resp.Networks = append(resp.Networks, domainNetworkToProto(result))
	}
	return resp, nil
}

func (h *NetworkHandler) Delete(ctx context.Context, req *pb.NetworkDeleteRequest) (*pb.NetworkDeleteResponse, error) {
	for _, item := range req.GetItems() {
		uid := item.GetUid()
		if uid == "" {
			return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required for delete").Err()
		}
		if err := h.svc.Delete(ctx, uid); err != nil {
			return nil, err
		}
	}
	return &pb.NetworkDeleteResponse{}, nil
}

func (h *NetworkHandler) List(ctx context.Context, req *pb.NetworkListRequest) (*pb.NetworkListResponse, error) {
	selectors := protoSelectorsToService(req.GetSelectors())
	page := service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	}

	networks, nextToken, snapshotRV, err := h.svc.List(ctx, selectors, page)
	if err != nil {
		return nil, err
	}

	resp := &pb.NetworkListResponse{
		ResourceVersion: strconv.FormatInt(snapshotRV, 10),
		NextPageToken:   nextToken,
	}
	for _, n := range networks {
		resp.Networks = append(resp.Networks, domainNetworkToProto(n))
	}
	return resp, nil
}

func (h *NetworkHandler) Watch(req *pb.NetworkWatchRequest, stream pb.NetworkService_WatchServer) error {
	ctx := stream.Context()

	var fromRV int64
	if rvStr := req.GetResourceVersion(); rvStr != "" {
		var err error
		fromRV, err = strconv.ParseInt(rvStr, 10, 64)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid resource_version: %v", err)
		}
	}

	matchers := []watch.SelectorMatcher{
		func(evt watch.Event) bool {
			return evt.ResourceKind == "Network"
		},
	}
	sub, err := h.hub.Subscribe(ctx, fromRV, matchers...)
	if err != nil {
		if errors.Is(err, watch.ErrGone) {
			return status.Error(codes.OutOfRange, "resourceVersion too old, please relist")
		}
		return err
	}
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-sub.C:
			if !ok {
				return nil
			}
			if err := stream.Send(watchEventToProto(evt)); err != nil {
				return err
			}
		}
	}
}

func watchEventToProto(evt watch.Event) *commonv1.WatchEvent {
	var evtType commonv1.WatchEvent_EventType
	switch evt.EventType {
	case "ADDED":
		evtType = commonv1.WatchEvent_EVENT_TYPE_ADDED
	case "MODIFIED":
		evtType = commonv1.WatchEvent_EVENT_TYPE_MODIFIED
	case "DELETED":
		evtType = commonv1.WatchEvent_EVENT_TYPE_DELETED
	}
	return &commonv1.WatchEvent{
		EventType:       evtType,
		ResourceVersion: strconv.FormatInt(evt.ResourceVersion, 10),
		ResourceKind:    evt.ResourceKind,
		Data:            evt.Data,
	}
}
