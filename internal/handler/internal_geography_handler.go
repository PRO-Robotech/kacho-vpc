// Package handler — InternalRegionService + InternalZoneService реализация.
package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// -- Region --

type InternalRegionHandler struct {
	vpcv1.UnimplementedInternalRegionServiceServer
	svc *service.RegionService
}

func NewInternalRegionHandler(s *service.RegionService) *InternalRegionHandler {
	return &InternalRegionHandler{svc: s}
}

func (h *InternalRegionHandler) Create(ctx context.Context, req *vpcv1.CreateRegionRequest) (*vpcv1.Region, error) {
	v, err := h.svc.Create(ctx, req.GetId(), req.GetName())
	if err != nil {
		return nil, mapGeoErr(err)
	}
	return regionToProto(v), nil
}

func (h *InternalRegionHandler) Get(ctx context.Context, req *vpcv1.GetRegionRequest) (*vpcv1.Region, error) {
	v, err := h.svc.Get(ctx, req.GetRegionId())
	if err != nil {
		return nil, mapGeoErr(err)
	}
	return regionToProto(v), nil
}

func (h *InternalRegionHandler) List(ctx context.Context, req *vpcv1.ListRegionsRequest) (*vpcv1.ListRegionsResponse, error) {
	items, next, err := h.svc.List(ctx, service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	})
	if err != nil {
		return nil, mapGeoErr(err)
	}
	out := make([]*vpcv1.Region, 0, len(items))
	for _, v := range items {
		out = append(out, regionToProto(v))
	}
	return &vpcv1.ListRegionsResponse{Regions: out, NextPageToken: next}, nil
}

func (h *InternalRegionHandler) Update(ctx context.Context, req *vpcv1.UpdateRegionRequest) (*vpcv1.Region, error) {
	v, err := h.svc.Update(ctx, req.GetRegionId(), req.GetName())
	if err != nil {
		return nil, mapGeoErr(err)
	}
	return regionToProto(v), nil
}

func (h *InternalRegionHandler) Delete(ctx context.Context, req *vpcv1.DeleteRegionRequest) (*vpcv1.DeleteRegionResponse, error) {
	if err := h.svc.Delete(ctx, req.GetRegionId()); err != nil {
		return nil, mapGeoErr(err)
	}
	return &vpcv1.DeleteRegionResponse{}, nil
}

func regionToProto(v *domain.Region) *vpcv1.Region {
	if v == nil {
		return nil
	}
	return &vpcv1.Region{
		Id:        v.ID,
		Name:      v.Name,
		CreatedAt: timestamppb.New(v.CreatedAt),
	}
}

// -- Zone --

type InternalZoneHandler struct {
	vpcv1.UnimplementedInternalZoneServiceServer
	svc *service.ZoneService
}

func NewInternalZoneHandler(s *service.ZoneService) *InternalZoneHandler {
	return &InternalZoneHandler{svc: s}
}

func (h *InternalZoneHandler) Create(ctx context.Context, req *vpcv1.CreateZoneRequest) (*vpcv1.Zone, error) {
	v, err := h.svc.Create(ctx, req.GetId(), req.GetRegionId(), req.GetName())
	if err != nil {
		return nil, mapGeoErr(err)
	}
	return zoneToProto(v), nil
}

func (h *InternalZoneHandler) Get(ctx context.Context, req *vpcv1.GetZoneRequest) (*vpcv1.Zone, error) {
	v, err := h.svc.Get(ctx, req.GetZoneId())
	if err != nil {
		return nil, mapGeoErr(err)
	}
	return zoneToProto(v), nil
}

func (h *InternalZoneHandler) List(ctx context.Context, req *vpcv1.ListZonesRequest) (*vpcv1.ListZonesResponse, error) {
	items, next, err := h.svc.List(ctx, req.GetRegionId(), service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	})
	if err != nil {
		return nil, mapGeoErr(err)
	}
	out := make([]*vpcv1.Zone, 0, len(items))
	for _, v := range items {
		out = append(out, zoneToProto(v))
	}
	return &vpcv1.ListZonesResponse{Zones: out, NextPageToken: next}, nil
}

func (h *InternalZoneHandler) Update(ctx context.Context, req *vpcv1.UpdateZoneRequest) (*vpcv1.Zone, error) {
	v, err := h.svc.Update(ctx, req.GetZoneId(), req.GetName())
	if err != nil {
		return nil, mapGeoErr(err)
	}
	return zoneToProto(v), nil
}

func (h *InternalZoneHandler) Delete(ctx context.Context, req *vpcv1.DeleteZoneRequest) (*vpcv1.DeleteZoneResponse, error) {
	if err := h.svc.Delete(ctx, req.GetZoneId()); err != nil {
		return nil, mapGeoErr(err)
	}
	return &vpcv1.DeleteZoneResponse{}, nil
}

func zoneToProto(v *domain.Zone) *vpcv1.Zone {
	if v == nil {
		return nil
	}
	return &vpcv1.Zone{
		Id:        v.ID,
		RegionId:  v.RegionID,
		Name:      v.Name,
		CreatedAt: timestamppb.New(v.CreatedAt),
	}
}

// mapGeoErr — admin-handler error mapping для Region/Zone admin RPC.
// Делегирует internalMapErr (R8 M1 sibling closure).
func mapGeoErr(err error) error {
	return internalMapErr("geography admin error", err)
}
