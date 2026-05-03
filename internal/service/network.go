package service

import (
	"context"
	"reflect"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkService реализует use-cases для Network.
type NetworkService struct {
	repo         NetworkRepo
	folderClient FolderClient
}

// NewNetworkService создаёт NetworkService.
func NewNetworkService(r NetworkRepo, fc FolderClient) *NetworkService {
	return &NetworkService{repo: r, folderClient: fc}
}

// Upsert создаёт или обновляет сеть.
func (s *NetworkService) Upsert(ctx context.Context, net *domain.Network) (*domain.Network, error) {
	if net.Name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if net.FolderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}

	// Проверяем существование папки через cross-service
	exists, err := s.folderClient.Exists(ctx, net.FolderID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, coreerrors.NotFound("Folder", net.FolderID).Err()
	}

	existing, err := s.repo.GetByFolderAndName(ctx, net.FolderID, net.Name)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		net.UID = ids.NewUID()
		net.State = "ACTIVE"
		return s.repo.Insert(ctx, net)
	}

	if !networkDiff(existing, net) {
		return existing, nil
	}

	existing.Labels = net.Labels
	existing.Annotations = net.Annotations
	existing.DisplayName = net.DisplayName
	existing.Description = net.Description
	return s.repo.Update(ctx, existing)
}

// GetByUID возвращает сеть по UID.
func (s *NetworkService) GetByUID(ctx context.Context, uid string) (*domain.Network, error) {
	if uid == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}
	return s.repo.GetByUID(ctx, uid)
}

// List возвращает список сетей.
func (s *NetworkService) List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.Network, string, int64, error) {
	return s.repo.List(ctx, selectors, page)
}

// SnapshotResourceVersion возвращает текущее значение глобального sequence.
func (s *NetworkService) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	return s.repo.SnapshotResourceVersion(ctx)
}

// Delete удаляет сеть по UID.
func (s *NetworkService) Delete(ctx context.Context, uid string) error {
	if uid == "" {
		return coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}

	existing, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return err
	}
	if existing == nil {
		return coreerrors.NotFound("Network", uid).Err()
	}

	hasDeps, err := s.repo.HasDependents(ctx, uid)
	if err != nil {
		return err
	}
	if hasDeps {
		return coreerrors.FailedPrecondition("network has dependent resources: Subnet, SecurityGroup, RouteTable").Err()
	}

	return s.repo.HardDelete(ctx, uid)
}

// HasDependents проверяет, есть ли у сети дочерние ресурсы.
func (s *NetworkService) HasDependents(ctx context.Context, uid string) (bool, []string, error) {
	has, err := s.repo.HasDependents(ctx, uid)
	if err != nil {
		return false, nil, err
	}
	if has {
		return true, []string{"Subnet", "SecurityGroup", "RouteTable"}, nil
	}
	return false, nil, nil
}

func networkDiff(existing, incoming *domain.Network) bool {
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
