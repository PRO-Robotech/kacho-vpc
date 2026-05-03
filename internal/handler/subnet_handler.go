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

// SubnetHandler реализует pb.SubnetServiceServer.
type SubnetHandler struct {
	pb.UnimplementedSubnetServiceServer
	svc *service.SubnetService
	hub *watch.Hub
}

// NewSubnetHandler создаёт SubnetHandler.
func NewSubnetHandler(svc *service.SubnetService, hub *watch.Hub) *SubnetHandler {
	return &SubnetHandler{svc: svc, hub: hub}
}

func (h *SubnetHandler) Upsert(ctx context.Context, req *pb.SubnetUpsertRequest) (*pb.SubnetUpsertResponse, error) {
	resp := &pb.SubnetUpsertResponse{}
	for _, in := range req.GetSubnets() {
		subnet := protoSubnetToDomain(in)
		result, err := h.svc.Upsert(ctx, subnet)
		if err != nil {
			return nil, err
		}
		resp.Subnets = append(resp.Subnets, domainSubnetToProto(result))
	}
	return resp, nil
}

func (h *SubnetHandler) Delete(ctx context.Context, req *pb.SubnetDeleteRequest) (*pb.SubnetDeleteResponse, error) {
	for _, item := range req.GetItems() {
		uid := item.GetUid()
		if uid == "" {
			return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required for delete").Err()
		}
		if err := h.svc.Delete(ctx, uid); err != nil {
			return nil, err
		}
	}
	return &pb.SubnetDeleteResponse{}, nil
}

func (h *SubnetHandler) List(ctx context.Context, req *pb.SubnetListRequest) (*pb.SubnetListResponse, error) {
	// C6: передаём refs для фильтрации по network_id
	selectors := protoSelectorsToServiceWithNetwork(req.GetSelectors())
	page := service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	}

	subnets, nextToken, snapshotRV, err := h.svc.List(ctx, selectors, page)
	if err != nil {
		return nil, err
	}

	resp := &pb.SubnetListResponse{
		ResourceVersion: strconv.FormatInt(snapshotRV, 10),
		NextPageToken:   nextToken,
	}
	for _, s := range subnets {
		resp.Subnets = append(resp.Subnets, domainSubnetToProto(s))
	}
	return resp, nil
}

func (h *SubnetHandler) Watch(req *pb.SubnetWatchRequest, stream pb.SubnetService_WatchServer) error {
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
		func(evt watch.Event) bool { return evt.ResourceKind == "Subnet" },
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
