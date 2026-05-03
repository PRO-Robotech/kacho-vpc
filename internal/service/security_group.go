package service

import (
	"context"
	"reflect"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SecurityGroupService реализует use-cases для SecurityGroup.
type SecurityGroupService struct {
	repo         SecurityGroupRepo
	networkRepo  NetworkRepo
	folderClient FolderClient
}

// NewSecurityGroupService создаёт SecurityGroupService.
func NewSecurityGroupService(r SecurityGroupRepo, networkRepo NetworkRepo, fc FolderClient) *SecurityGroupService {
	return &SecurityGroupService{repo: r, networkRepo: networkRepo, folderClient: fc}
}

// Upsert создаёт или обновляет группу безопасности с полной заменой правил.
func (s *SecurityGroupService) Upsert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	if sg.Name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if sg.FolderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if sg.NetworkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.network_id", "network_id is required").Err()
	}

	// Проверяем существование Network
	network, err := s.networkRepo.GetByUID(ctx, sg.NetworkID)
	if err != nil {
		return nil, err
	}
	if network == nil {
		return nil, coreerrors.NotFound("Network", sg.NetworkID).Err()
	}

	sg.CloudID = network.CloudID
	sg.OrganizationID = network.OrganizationID

	// Проверяем Folder через cross-service
	folderExists, err := s.folderClient.Exists(ctx, sg.FolderID)
	if err != nil {
		return nil, err
	}
	if !folderExists {
		return nil, coreerrors.NotFound("Folder", sg.FolderID).Err()
	}

	// D3: сервер всегда переназначает id правил
	assignRuleIDs(sg.Rules)

	existing, err := s.repo.GetByFolderAndName(ctx, sg.FolderID, sg.Name)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		sg.UID = ids.NewUID()
		sg.State = "ACTIVE"
		return s.repo.Insert(ctx, sg)
	}

	// Full-replace: всегда обновляем (правила заменяются полностью)
	existing.Labels = sg.Labels
	existing.Annotations = sg.Annotations
	existing.DisplayName = sg.DisplayName
	existing.Description = sg.Description
	existing.Rules = sg.Rules
	return s.repo.Update(ctx, existing)
}

// GetByUID возвращает группу безопасности по UID.
func (s *SecurityGroupService) GetByUID(ctx context.Context, uid string) (*domain.SecurityGroup, error) {
	if uid == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}
	return s.repo.GetByUID(ctx, uid)
}

// List возвращает список групп безопасности.
func (s *SecurityGroupService) List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.SecurityGroup, string, int64, error) {
	return s.repo.List(ctx, selectors, page)
}

// SnapshotResourceVersion возвращает текущее значение глобального sequence.
func (s *SecurityGroupService) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	return s.repo.SnapshotResourceVersion(ctx)
}

// Delete удаляет группу безопасности по UID.
func (s *SecurityGroupService) Delete(ctx context.Context, uid string) error {
	if uid == "" {
		return coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}

	existing, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return err
	}
	if existing == nil {
		return coreerrors.NotFound("SecurityGroup", uid).Err()
	}

	return s.repo.HardDelete(ctx, uid)
}

// HasDependents у SecurityGroup нет дочерних ресурсов в VPC.
func (s *SecurityGroupService) HasDependents(ctx context.Context, uid string) (bool, []string, error) {
	has, err := s.repo.HasDependents(ctx, uid)
	if err != nil {
		return false, nil, err
	}
	if has {
		return true, []string{}, nil
	}
	return false, nil, nil
}

// assignRuleIDs назначает серверные UUIDs всем правилам (full-replace семантика).
func assignRuleIDs(rules []domain.SecurityGroupRule) {
	for i := range rules {
		rules[i].ID = ids.NewUID()
	}
}

func sgDiff(existing, incoming *domain.SecurityGroup) bool {
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
