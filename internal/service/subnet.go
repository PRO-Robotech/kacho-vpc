package service

import (
	"context"
	"net"
	"reflect"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SubnetService реализует use-cases для Subnet.
type SubnetService struct {
	repo         SubnetRepo
	networkRepo  NetworkRepo
	folderClient FolderClient
}

// NewSubnetService создаёт SubnetService.
func NewSubnetService(r SubnetRepo, networkRepo NetworkRepo, fc FolderClient) *SubnetService {
	return &SubnetService{repo: r, networkRepo: networkRepo, folderClient: fc}
}

// Upsert создаёт или обновляет подсеть.
func (s *SubnetService) Upsert(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error) {
	if subnet.Name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if subnet.FolderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if subnet.NetworkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.network_id", "network_id is required").Err()
	}
	if subnet.CIDRBlock == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.cidr_block", "cidr_block is required").Err()
	}

	// Валидация CIDR: нельзя передавать host-bits-set адрес
	ip, ipNet, err := net.ParseCIDR(subnet.CIDRBlock)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.cidr_block", "invalid CIDR format").Err()
	}
	// I19: проверяем что host bits не установлены
	if ip.Equal(ipNet.IP) == false {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.cidr_block", "host bits are set in CIDR address").Err()
	}

	// Проверяем существование Network (same-DB валидация)
	network, err := s.networkRepo.GetByUID(ctx, subnet.NetworkID)
	if err != nil {
		return nil, err
	}
	if network == nil {
		return nil, coreerrors.NotFound("Network", subnet.NetworkID).Err()
	}

	// Денормализуем cloud_id и organization_id из Network
	subnet.CloudID = network.CloudID
	subnet.OrganizationID = network.OrganizationID

	// Проверяем существование Folder через cross-service
	folderExists, err := s.folderClient.Exists(ctx, subnet.FolderID)
	if err != nil {
		return nil, err
	}
	if !folderExists {
		return nil, coreerrors.NotFound("Folder", subnet.FolderID).Err()
	}

	existing, err := s.repo.GetByFolderAndName(ctx, subnet.FolderID, subnet.Name)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		subnet.UID = ids.NewUID()
		subnet.State = "ACTIVE"
		return s.repo.Insert(ctx, subnet)
	}

	if !subnetDiff(existing, subnet) {
		return existing, nil
	}

	existing.Labels = subnet.Labels
	existing.Annotations = subnet.Annotations
	existing.CIDRBlock = subnet.CIDRBlock
	existing.ZoneID = subnet.ZoneID
	existing.DisplayName = subnet.DisplayName
	existing.Description = subnet.Description
	return s.repo.Update(ctx, existing)
}

// GetByUID возвращает подсеть по UID.
func (s *SubnetService) GetByUID(ctx context.Context, uid string) (*domain.Subnet, error) {
	if uid == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}
	return s.repo.GetByUID(ctx, uid)
}

// List возвращает список подсетей.
func (s *SubnetService) List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.Subnet, string, int64, error) {
	return s.repo.List(ctx, selectors, page)
}

// SnapshotResourceVersion возвращает текущее значение глобального sequence.
func (s *SubnetService) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	return s.repo.SnapshotResourceVersion(ctx)
}

// Delete удаляет подсеть по UID.
func (s *SubnetService) Delete(ctx context.Context, uid string) error {
	if uid == "" {
		return coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}

	existing, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return err
	}
	if existing == nil {
		return coreerrors.NotFound("Subnet", uid).Err()
	}

	return s.repo.HardDelete(ctx, uid)
}

// HasDependents у Subnet нет собственных дочерних ресурсов в VPC.
func (s *SubnetService) HasDependents(_ context.Context, _ string) (bool, []string, error) {
	return false, nil, nil
}

func subnetDiff(existing, incoming *domain.Subnet) bool {
	if existing.CIDRBlock != incoming.CIDRBlock {
		return true
	}
	if existing.ZoneID != incoming.ZoneID {
		return true
	}
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
