package service

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
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
	// VPC RouteTable принимает empty name (verbatim YC permissive policy).
	// folder_id / network_id больше НЕ валидируются sync — async через
	// folderClient.Exists / networkRepo.Get → NotFound (verbatim YC). См.
	// YC-DIFF-INVALID-PARENT-CODE.md, YC-DIFF-NAME-VALIDATION.md.
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}
	// RT-CIDR-VALIDATION: каждая static route должна иметь валидный CIDR
	// destinationPrefix (без host-bits) и валидный nextHopAddress (IPv4/IPv6).
	// См. RT-STATIC-ROUTES-VALIDATION.md.
	if err := validateStaticRoutes(req.StaticRoutes); err != nil {
		return nil, err
	}

	rtID := ids.NewID(ids.PrefixRouteTable)
	op, err := operations.New(
		ids.PrefixOperationVPC,
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
		// verbatim YC text: "Folder with id <X> not found".
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
	}

	if _, err := s.networkRepo.Get(ctx, req.NetworkID); err != nil {
		// verbatim YC text: "Network <X> not found".
		return nil, status.Errorf(codes.NotFound, "Network %s not found", req.NetworkID)
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
//
// Sync-валидация: см. validateRouteTableUpdate.
func (s *RouteTableService) Update(ctx context.Context, req UpdateRouteTableReq) (*operations.Operation, error) {
	if req.RouteTableID == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	if err := validateRouteTableUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
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

// validateRouteTableUpdate проверяет name/description/labels/static_routes в Update.
func validateRouteTableUpdate(req UpdateRouteTableReq) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {}, "static_routes": {},
	}
	if err := corevalidate.UpdateMask("update_mask", req.UpdateMask, known); err != nil {
		return err
	}
	updates := req.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			// VPC RouteTable: empty name allowed (YC permissive policy).
			if err := corevalidate.NameVPC("name", req.Name); err != nil {
				return err
			}
		case "description":
			if err := corevalidate.Description("description", req.Description); err != nil {
				return err
			}
		case "labels":
			if err := corevalidate.Labels("labels", req.Labels); err != nil {
				return err
			}
		case "static_routes":
			if err := validateStaticRoutes(req.StaticRoutes); err != nil {
				return err
			}
		}
	}
	// Полный апдейт без mask тоже валидирует static_routes, если они есть.
	if len(req.UpdateMask) == 0 && len(req.StaticRoutes) > 0 {
		if err := validateStaticRoutes(req.StaticRoutes); err != nil {
			return err
		}
	}
	return nil
}

// validateStaticRoutes проверяет каждую запись routes:
//   - destinationPrefix: валидный CIDR (IPv4 или IPv6) без host-bits;
//   - nextHopAddress: валидный IP-адрес (IPv4 или IPv6).
//
// Пустой массив — допустим (route table без статических маршрутов).
// При нарушении — InvalidArgument с FieldViolation `static_routes[<i>].<field>`.
func validateStaticRoutes(routes []domain.StaticRoute) error {
	for i, r := range routes {
		dpField := fmt.Sprintf("static_routes[%d].destination_prefix", i)
		if r.DestinationPrefix == "" {
			return invalidArg(dpField, dpField+" is required")
		}
		prefix, err := netip.ParsePrefix(r.DestinationPrefix)
		if err != nil {
			return invalidArg(dpField, dpField+" must be a valid CIDR (e.g. 10.0.0.0/24)")
		}
		if prefix.Masked() != prefix {
			return invalidArg(dpField,
				dpField+" must have zero host-bits (use the network address, e.g. 10.0.0.0/24, not 10.0.0.5/24)")
		}
		nhField := fmt.Sprintf("static_routes[%d].next_hop_address", i)
		if r.NextHopAddress == "" {
			return invalidArg(nhField, nhField+" is required")
		}
		if _, err := netip.ParseAddr(r.NextHopAddress); err != nil {
			return invalidArg(nhField, nhField+" must be a valid IP address (IPv4 or IPv6)")
		}
	}
	return nil
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

// ListOperations возвращает операции для конкретного RouteTable.
func (s *RouteTableService) ListOperations(ctx context.Context, rtID string, p Pagination) ([]operations.Operation, string, error) {
	if _, err := s.repo.Get(ctx, rtID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: rtID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}

// Move инициирует перенос RouteTable в другой folder.
func (s *RouteTableService) Move(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Move route table %s", id),
		&vpcv1.MoveRouteTableMetadata{RouteTableId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, err := s.folderClient.Exists(ctx, destFolderID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destFolderID)
		}
		updated, err := s.repo.SetFolderID(ctx, id, destFolderID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(domainRouteTableToProto(updated))
	})
	return &op, nil
}

// Delete удаляет RouteTable.
func (s *RouteTableService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
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
