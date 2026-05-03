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

// SecurityGroupHandler реализует pb.SecurityGroupServiceServer.
type SecurityGroupHandler struct {
	pb.UnimplementedSecurityGroupServiceServer
	svc *service.SecurityGroupService
	hub *watch.Hub
}

// NewSecurityGroupHandler создаёт SecurityGroupHandler.
func NewSecurityGroupHandler(svc *service.SecurityGroupService, hub *watch.Hub) *SecurityGroupHandler {
	return &SecurityGroupHandler{svc: svc, hub: hub}
}

func (h *SecurityGroupHandler) Upsert(ctx context.Context, req *pb.SecurityGroupUpsertRequest) (*pb.SecurityGroupUpsertResponse, error) {
	resp := &pb.SecurityGroupUpsertResponse{}
	for _, in := range req.GetSecurityGroups() {
		sg := protoSGToDomain(in)
		result, err := h.svc.Upsert(ctx, sg)
		if err != nil {
			return nil, err
		}
		resp.SecurityGroups = append(resp.SecurityGroups, domainSGToProto(result))
	}
	return resp, nil
}

func (h *SecurityGroupHandler) Delete(ctx context.Context, req *pb.SecurityGroupDeleteRequest) (*pb.SecurityGroupDeleteResponse, error) {
	for _, item := range req.GetItems() {
		uid := item.GetUid()
		if uid == "" {
			return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required for delete").Err()
		}
		if err := h.svc.Delete(ctx, uid); err != nil {
			return nil, err
		}
	}
	return &pb.SecurityGroupDeleteResponse{}, nil
}

func (h *SecurityGroupHandler) List(ctx context.Context, req *pb.SecurityGroupListRequest) (*pb.SecurityGroupListResponse, error) {
	selectors := protoSelectorsToService(req.GetSelectors())
	page := service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	}

	sgs, nextToken, snapshotRV, err := h.svc.List(ctx, selectors, page)
	if err != nil {
		return nil, err
	}

	resp := &pb.SecurityGroupListResponse{
		ResourceVersion: strconv.FormatInt(snapshotRV, 10),
		NextPageToken:   nextToken,
	}
	for _, sg := range sgs {
		resp.SecurityGroups = append(resp.SecurityGroups, domainSGToProto(sg))
	}
	return resp, nil
}

func (h *SecurityGroupHandler) Watch(req *pb.SecurityGroupWatchRequest, stream pb.SecurityGroupService_WatchServer) error {
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
		func(evt watch.Event) bool { return evt.ResourceKind == "SecurityGroup" },
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
