package service

import (
	"context"
	"reflect"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// RouteTableService реализует use-cases для RouteTable.
type RouteTableService struct {
	repo        RouteTableRepo
	networkRepo NetworkRepo
	folderClient FolderClient
}

// NewRouteTableService создаёт RouteTableService.
func NewRouteTableService(r RouteTableRepo, networkRepo NetworkRepo, fc FolderClient) *RouteTableService {
	return &RouteTableService{repo: r, networkRepo: networkRepo, folderClient: fc}
}

// Upsert создаёт или обновляет таблицу маршрутизации с полной заменой маршрутов.
func (s *RouteTableService) Upsert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	if rt.Name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if rt.FolderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if rt.NetworkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.network_id", "network_id is required").Err()
	}

	// Проверяем существование Network
	network, err := s.networkRepo.GetByUID(ctx, rt.NetworkID)
	if err != nil {
		return nil, err
	}
	if network == nil {
		return nil, coreerrors.NotFound("Network", rt.NetworkID).Err()
	}

	rt.CloudID = network.CloudID
	rt.OrganizationID = network.OrganizationID

	// Проверяем Folder через cross-service
	folderExists, err := s.folderClient.Exists(ctx, rt.FolderID)
	if err != nil {
		return nil, err
	}
	if !folderExists {
		return nil, coreerrors.NotFound("Folder", rt.FolderID).Err()
	}

	// E2: сервер всегда переназначает id маршрутов
	assignStaticRouteIDs(rt.StaticRoutes)

	existing, err := s.repo.GetByFolderAndName(ctx, rt.FolderID, rt.Name)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		rt.UID = ids.NewUID()
		rt.State = "ACTIVE"
		return s.repo.Insert(ctx, rt)
	}

	// Full-replace: маршруты заменяются полностью
	existing.Labels = rt.Labels
	existing.Annotations = rt.Annotations
	existing.DisplayName = rt.DisplayName
	existing.Description = rt.Description
	existing.StaticRoutes = rt.StaticRoutes
	return s.repo.Update(ctx, existing)
}

// GetByUID возвращает таблицу маршрутизации по UID.
func (s *RouteTableService) GetByUID(ctx context.Context, uid string) (*domain.RouteTable, error) {
	if uid == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}
	return s.repo.GetByUID(ctx, uid)
}

// List возвращает список таблиц маршрутизации.
func (s *RouteTableService) List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.RouteTable, string, int64, error) {
	return s.repo.List(ctx, selectors, page)
}

// SnapshotResourceVersion возвращает текущее значение глобального sequence.
func (s *RouteTableService) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	return s.repo.SnapshotResourceVersion(ctx)
}

// Delete удаляет таблицу маршрутизации по UID.
func (s *RouteTableService) Delete(ctx context.Context, uid string) error {
	if uid == "" {
		return coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}

	existing, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return err
	}
	if existing == nil {
		return coreerrors.NotFound("RouteTable", uid).Err()
	}

	return s.repo.HardDelete(ctx, uid)
}

// HasDependents у RouteTable нет дочерних ресурсов в VPC.
func (s *RouteTableService) HasDependents(ctx context.Context, uid string) (bool, []string, error) {
	has, err := s.repo.HasDependents(ctx, uid)
	if err != nil {
		return false, nil, err
	}
	if has {
		return true, []string{}, nil
	}
	return false, nil, nil
}

// assignStaticRouteIDs назначает серверные UUIDs всем маршрутам (full-replace семантика).
func assignStaticRouteIDs(routes []domain.StaticRoute) {
	for i := range routes {
		routes[i].ID = ids.NewUID()
	}
}

func rtDiff(existing, incoming *domain.RouteTable) bool {
	if existing.DisplayName != incoming.DisplayName {
		return true
	}
	if existing.Description != incoming.Description {
		return true
	}
	if !reflect.DeepEqual(normalizeMap(existing.Labels), normalizeMap(incoming.Labels)) {
		return true
	}
	if !reflect.DeepEqual(normalizeMap(existing.Annotations), normalizeMap(incoming.Annotations)) {
		return true
	}
	return false
}
