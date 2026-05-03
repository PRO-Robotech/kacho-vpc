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

// AddressHandler реализует pb.AddressServiceServer.
type AddressHandler struct {
	pb.UnimplementedAddressServiceServer
	svc *service.AddressService
	hub *watch.Hub
}

// NewAddressHandler создаёт AddressHandler.
func NewAddressHandler(svc *service.AddressService, hub *watch.Hub) *AddressHandler {
	return &AddressHandler{svc: svc, hub: hub}
}

func (h *AddressHandler) Upsert(ctx context.Context, req *pb.AddressUpsertRequest) (*pb.AddressUpsertResponse, error) {
	resp := &pb.AddressUpsertResponse{}
	for _, in := range req.GetAddresses() {
		addr := protoAddrToDomain(in)
		result, err := h.svc.Upsert(ctx, addr)
		if err != nil {
			return nil, err
		}
		resp.Addresses = append(resp.Addresses, domainAddrToProto(result))
	}
	return resp, nil
}

func (h *AddressHandler) Delete(ctx context.Context, req *pb.AddressDeleteRequest) (*pb.AddressDeleteResponse, error) {
	for _, item := range req.GetItems() {
		uid := item.GetUid()
		if uid == "" {
			return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required for delete").Err()
		}
		if err := h.svc.Delete(ctx, uid); err != nil {
			return nil, err
		}
	}
	return &pb.AddressDeleteResponse{}, nil
}

func (h *AddressHandler) List(ctx context.Context, req *pb.AddressListRequest) (*pb.AddressListResponse, error) {
	selectors := protoSelectorsToService(req.GetSelectors())
	page := service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	}

	addrs, nextToken, snapshotRV, err := h.svc.List(ctx, selectors, page)
	if err != nil {
		return nil, err
	}

	resp := &pb.AddressListResponse{
		ResourceVersion: strconv.FormatInt(snapshotRV, 10),
		NextPageToken:   nextToken,
	}
	for _, a := range addrs {
		resp.Addresses = append(resp.Addresses, domainAddrToProto(a))
	}
	return resp, nil
}

func (h *AddressHandler) Watch(req *pb.AddressWatchRequest, stream pb.AddressService_WatchServer) error {
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
		func(evt watch.Event) bool { return evt.ResourceKind == "Address" },
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
