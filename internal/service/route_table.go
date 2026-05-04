package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// CreateRouteTableReq — запрос на создание таблицы маршрутизации.
type CreateRouteTableReq struct {
	FolderID     string
	Name         string
	Description  string
	Labels       map[string]string
	NetworkID    string
	StaticRoutes []domain.StaticRoute
}

// UpdateRouteTableReq — запрос на обновление таблицы маршрутизации.
type UpdateRouteTableReq struct {
	RouteTableID string
	Name         string
	Description  string
	Labels       map[string]string
	StaticRoutes []domain.StaticRoute
	UpdateMask   []string
}

// RouteTableService — бизнес-логика управления таблицами маршрутизации.
type RouteTableService struct {
	repo         RouteTableRepo
	networkRepo  NetworkRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewRouteTableService создаёт RouteTableService.
func NewRouteTableService(repo RouteTableRepo, networkRepo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo) *RouteTableService {
	return &RouteTableService{repo: repo, networkRepo: networkRepo, folderClient: folderClient, opsRepo: opsRepo}
}

// Get возвращает RouteTable по ID.
func (s *RouteTableService) Get(ctx context.Context, id string) (*domain.RouteTable, error) {
	rt, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return rt, nil
}

// List возвращает список таблиц маршрутизации.
func (s *RouteTableService) List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*domain.RouteTable, string, error) {
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание RouteTable.
func (s *RouteTableService) Create(ctx context.Context, req CreateRouteTableReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name required")
	}
	if err := validateUUID("folder_id", req.FolderID); err != nil {
		return nil, err
	}
	if err := validateUUID("network_id", req.NetworkID); err != nil {
		return nil, err
	}

	rtID := ids.NewUID()
	op, err := operations.New(
		"vpc",
		fmt.Sprintf("Create route table %s", req.Name),
		&vpcv1.CreateRouteTableMetadata{RouteTableId: rtID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, rtID, req)
	})

	return &op, nil
}

func (s *RouteTableService) doCreate(ctx context.Context, rtID string, req CreateRouteTableReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "folder %s not found", req.FolderID)
	}

	if _, err := s.networkRepo.Get(ctx, req.NetworkID); err != nil {
		return nil, status.Errorf(codes.NotFound, "network %s not found", req.NetworkID)
	}

	rt := &domain.RouteTable{
		ID:           rtID,
		FolderID:     req.FolderID,
		CreatedAt:    time.Now().UTC(),
		Name:         req.Name,
		Description:  req.Description,
		Labels:       req.Labels,
		NetworkID:    req.NetworkID,
		StaticRoutes: req.StaticRoutes,
	}
	created, err := s.repo.Insert(ctx, rt)
	if err != nil {
		return nil, err
	}
	return anypb.New(domainRouteTableToProto(created))
}

// Update обновляет RouteTable.
func (s *RouteTableService) Update(ctx context.Context, req UpdateRouteTableReq) (*operations.Operation, error) {
	if req.RouteTableID == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}

	op, err := operations.New(
		"vpc",
		fmt.Sprintf("Update route table %s", req.RouteTableID),
		&vpcv1.UpdateRouteTableMetadata{RouteTableId: req.RouteTableID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doUpdate(ctx, req)
	})

	return &op, nil
}

func (s *RouteTableService) doUpdate(ctx context.Context, req UpdateRouteTableReq) (*anypb.Any, error) {
	rt, err := s.repo.Get(ctx, req.RouteTableID)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	applyRouteTableMask(rt, req)

	updated, err := s.repo.Update(ctx, rt)
	if err != nil {
		return nil, err
	}
	return anypb.New(domainRouteTableToProto(updated))
}

func applyRouteTableMask(rt *domain.RouteTable, req UpdateRouteTableReq) {
	if len(req.UpdateMask) == 0 {
		rt.Name = req.Name
		rt.Description = req.Description
		rt.Labels = req.Labels
		rt.StaticRoutes = req.StaticRoutes
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			rt.Name = req.Name
		case "description":
			rt.Description = req.Description
		case "labels":
			rt.Labels = req.Labels
		case "static_routes":
			rt.StaticRoutes = req.StaticRoutes
		}
	}
}

// Delete удаляет RouteTable.
func (s *RouteTableService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}

	op, err := operations.New(
		"vpc",
		fmt.Sprintf("Delete route table %s", id),
		&vpcv1.DeleteRouteTableMetadata{RouteTableId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if err := s.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&vpcv1.DeleteRouteTableMetadata{RouteTableId: id})
	})

	return &op, nil
}

// domainRouteTableToProto конвертирует domain RouteTable в proto RouteTable.
func domainRouteTableToProto(rt *domain.RouteTable) *vpcv1.RouteTable {
	p := &vpcv1.RouteTable{
		Id:          rt.ID,
		FolderId:    rt.FolderID,
		Name:        rt.Name,
		Description: rt.Description,
		Labels:      rt.Labels,
		NetworkId:   rt.NetworkID,
	}
	for _, sr := range rt.StaticRoutes {
		protoSR := &vpcv1.StaticRoute{
			Labels: sr.Labels,
		}
		if sr.DestinationPrefix != "" {
			protoSR.Destination = &vpcv1.StaticRoute_DestinationPrefix{
				DestinationPrefix: sr.DestinationPrefix,
			}
		}
		if sr.NextHopAddress != "" {
			protoSR.NextHop = &vpcv1.StaticRoute_NextHopAddress{
				NextHopAddress: sr.NextHopAddress,
			}
		}
		p.StaticRoutes = append(p.StaticRoutes, protoSR)
	}
	return p
}
