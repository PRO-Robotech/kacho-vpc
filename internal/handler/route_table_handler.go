package handler

import (
	"context"
	"errors"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/watch"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// RouteTableHandler реализует pb.RouteTableServiceServer.
type RouteTableHandler struct {
	pb.UnimplementedRouteTableServiceServer
	svc *service.RouteTableService
	hub *watch.Hub
}

// NewRouteTableHandler создаёт RouteTableHandler.
func NewRouteTableHandler(svc *service.RouteTableService, hub *watch.Hub) *RouteTableHandler {
	return &RouteTableHandler{svc: svc, hub: hub}
}

func (h *RouteTableHandler) Upsert(ctx context.Context, req *pb.RouteTableUpsertRequest) (*pb.RouteTableUpsertResponse, error) {
	resp := &pb.RouteTableUpsertResponse{}
	for _, in := range req.GetRouteTables() {
		rt := protoRTToDomain(in)
		result, err := h.svc.Upsert(ctx, rt)
		if err != nil {
			return nil, err
		}
		resp.RouteTables = append(resp.RouteTables, domainRTToProto(result))
	}
	return resp, nil
}

func (h *RouteTableHandler) Delete(ctx context.Context, req *pb.RouteTableDeleteRequest) (*pb.RouteTableDeleteResponse, error) {
	for _, item := range req.GetItems() {
		uid := item.GetUid()
		if uid == "" {
			return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required for delete").Err()
		}
		if err := h.svc.Delete(ctx, uid); err != nil {
			return nil, err
		}
	}
	return &pb.RouteTableDeleteResponse{}, nil
}

func (h *RouteTableHandler) List(ctx context.Context, req *pb.RouteTableListRequest) (*pb.RouteTableListResponse, error) {
	selectors := protoSelectorsToService(req.GetSelectors())
	page := service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	}

	rts, nextToken, snapshotRV, err := h.svc.List(ctx, selectors, page)
	if err != nil {
		return nil, err
	}

	resp := &pb.RouteTableListResponse{
		ResourceVersion: strconv.FormatInt(snapshotRV, 10),
		NextPageToken:   nextToken,
	}
	for _, rt := range rts {
		resp.RouteTables = append(resp.RouteTables, domainRTToProto(rt))
	}
	return resp, nil
}

func (h *RouteTableHandler) Watch(req *pb.RouteTableWatchRequest, stream pb.RouteTableService_WatchServer) error {
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
		func(evt watch.Event) bool { return evt.ResourceKind == "RouteTable" },
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
