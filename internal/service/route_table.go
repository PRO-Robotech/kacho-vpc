package service

import (
	"context"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// RouteTableService реализует use-cases для RouteTable.
type RouteTableService struct {
	repo         RouteTableRepo
	networkRepo  NetworkRepo
	opsRepo      operations.Repo
	folderClient FolderClient
}

// NewRouteTableService создаёт RouteTableService.
func NewRouteTableService(r RouteTableRepo, nr NetworkRepo, ops operations.Repo, fc FolderClient) *RouteTableService {
	return &RouteTableService{repo: r, networkRepo: nr, opsRepo: ops, folderClient: fc}
}

// Get возвращает RouteTable по ID (синхронный).
func (s *RouteTableService) Get(ctx context.Context, id string) (*domain.RouteTable, error) {
	if id == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("route_table_id", "route_table_id is required").Err()
	}
	rt, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rt == nil {
		return nil, coreerrors.NotFound("RouteTable", id).Err()
	}
	return rt, nil
}

// List возвращает список RouteTable (синхронный).
func (s *RouteTableService) List(ctx context.Context, filter ListFilter) ([]domain.RouteTable, string, error) {
	return s.repo.List(ctx, filter)
}

// Create создаёт RouteTable асинхронно.
func (s *RouteTableService) Create(ctx context.Context, folderID, networkID, name, description string, labels map[string]string, routes []domain.StaticRoute) (*operations.Operation, error) {
	if folderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if networkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("network_id", "network_id is required").Err()
	}
	if name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}

	resourceID := ids.NewUID()
	op, err := operations.New("Create RouteTable "+name, &pb.CreateRouteTableMetadata{RouteTableId: resourceID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, ferr := s.folderClient.Exists(ctx, folderID)
		if ferr != nil {
			return nil, ferr
		}
		if !exists {
			return nil, status.Errorf(codes.FailedPrecondition, "Folder %s not found", folderID)
		}

		net, nerr := s.networkRepo.Get(ctx, networkID)
		if nerr != nil {
			return nil, nerr
		}
		if net == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "Network %s not found", networkID)
		}

		assigned := assignRouteIDs(routes)

		rt := &domain.RouteTable{
			ID:           resourceID,
			FolderID:     folderID,
			NetworkID:    networkID,
			Name:         name,
			Description:  description,
			Labels:       labels,
			Status:       domain.RouteTableStatusProvisioning,
			Generation:   1,
			StaticRoutes: assigned,
		}
		if cerr := s.repo.Create(ctx, rt); cerr != nil {
			return nil, cerr
		}

		created, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		created.Status = domain.RouteTableStatusActive
		if uerr := s.repo.Update(ctx, created); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainRTToProto(final))
	})

	return &op, nil
}

// Update обновляет RouteTable асинхронно (static_routes — full-replace).
func (s *RouteTableService) Update(ctx context.Context, rtID, resourceVersion, name, description string, labels map[string]string, routes []domain.StaticRoute, updateMask []string) (*operations.Operation, error) {
	if rtID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("route_table_id", "route_table_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, rtID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("RouteTable", rtID).Err()
	}

	op, err := operations.New("Update RouteTable "+rtID, &pb.UpdateRouteTableMetadata{RouteTableId: rtID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, gerr := s.repo.Get(ctx, rtID)
		if gerr != nil {
			return nil, gerr
		}
		if cur == nil {
			return nil, status.Errorf(codes.NotFound, "RouteTable %s not found", rtID)
		}
		if resourceVersion != "" && cur.ResourceVersion != resourceVersion {
			return nil, status.Errorf(codes.Aborted, "resource_version mismatch: expected %s, got %s", resourceVersion, cur.ResourceVersion)
		}

		applyRTUpdateMask(cur, name, description, labels, routes, updateMask)
		cur.Generation++
		if uerr := s.repo.Update(ctx, cur); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, rtID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainRTToProto(final))
	})

	return &op, nil
}

// Delete удаляет RouteTable асинхронно.
func (s *RouteTableService) Delete(ctx context.Context, rtID string) (*operations.Operation, error) {
	if rtID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("route_table_id", "route_table_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, rtID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("RouteTable", rtID).Err()
	}

	op, err := operations.New("Delete RouteTable "+rtID, &pb.DeleteRouteTableMetadata{RouteTableId: rtID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if derr := s.repo.SoftDelete(ctx, rtID); derr != nil {
			return nil, derr
		}
		return anypb.New(&pb.DeleteRouteTableMetadata{RouteTableId: rtID})
	})

	return &op, nil
}

// assignRouteIDs назначает server-side UUID каждому маршруту.
func assignRouteIDs(routes []domain.StaticRoute) []domain.StaticRoute {
	result := make([]domain.StaticRoute, len(routes))
	for i, r := range routes {
		r.ID = ids.NewUID()
		result[i] = r
	}
	return result
}

func applyRTUpdateMask(rt *domain.RouteTable, name, description string, labels map[string]string, routes []domain.StaticRoute, mask []string) {
	if len(mask) == 0 {
		rt.Name = name
		rt.Description = description
		rt.Labels = labels
		rt.StaticRoutes = assignRouteIDs(routes)
		return
	}
	for _, f := range mask {
		switch f {
		case "name":
			rt.Name = name
		case "description":
			rt.Description = description
		case "labels":
			rt.Labels = labels
		case "static_routes":
			rt.StaticRoutes = assignRouteIDs(routes)
		}
	}
}

func domainRTToProto(rt *domain.RouteTable) *pb.RouteTable {
	routes := make([]*pb.StaticRoute, len(rt.StaticRoutes))
	for i, r := range rt.StaticRoutes {
		routes[i] = &pb.StaticRoute{
			Id:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	proto := &pb.RouteTable{
		Id:              rt.ID,
		FolderId:        rt.FolderID,
		NetworkId:       rt.NetworkID,
		Name:            rt.Name,
		Description:     rt.Description,
		Labels:          rt.Labels,
		Status:          pb.RouteTableStatus(rt.Status),
		Generation:      rt.Generation,
		ResourceVersion: rt.ResourceVersion,
		StaticRoutes:    routes,
	}
	if !rt.CreatedAt.IsZero() {
		proto.CreatedAt = timestampProto(rt.CreatedAt)
	}
	return proto
}
