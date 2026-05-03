package service

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AddressService реализует use-cases для Address.
type AddressService struct {
	repo         AddressRepo
	folderClient FolderClient
}

// NewAddressService создаёт AddressService.
func NewAddressService(r AddressRepo, fc FolderClient) *AddressService {
	return &AddressService{repo: r, folderClient: fc}
}

// Upsert создаёт или обновляет адрес.
// При создании выделяет псевдо-случайный IP из 203.0.113.0/24 (TEST-NET-3, RFC 5737).
func (s *AddressService) Upsert(ctx context.Context, addr *domain.Address) (*domain.Address, error) {
	if addr.Name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if addr.FolderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}

	// F2: только EXTERNAL поддерживается в 0.3
	if addr.AddressType != "" && addr.AddressType != "EXTERNAL" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("spec.address_type", "only EXTERNAL address type is supported").Err()
	}
	addr.AddressType = "EXTERNAL"

	// Проверяем существование Folder через cross-service
	folderExists, err := s.folderClient.Exists(ctx, addr.FolderID)
	if err != nil {
		return nil, err
	}
	if !folderExists {
		return nil, coreerrors.NotFound("Folder", addr.FolderID).Err()
	}

	existing, err := s.repo.GetByFolderAndName(ctx, addr.FolderID, addr.Name)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		addr.UID = ids.NewUID()
		addr.State = "RESERVED"

		// Выделяем IP из 203.0.113.0/24 и сохраняем в БД; retry при конфликте по allocated_ipv4.
		return s.allocateIP(ctx, addr)
	}

	if !addressDiff(existing, addr) {
		return existing, nil
	}

	existing.Labels = addr.Labels
	existing.Annotations = addr.Annotations
	existing.DisplayName = addr.DisplayName
	existing.Description = addr.Description
	existing.ZoneID = addr.ZoneID
	return s.repo.Update(ctx, existing)
}

// allocateIP выбирает псевдо-случайный IP из 203.0.113.0/24 с retry при UNIQUE violation
// на колонке allocated_ipv4. Вставляет запись в БД и возвращает сохранённый Address.
func (s *AddressService) allocateIP(ctx context.Context, addr *domain.Address) (*domain.Address, error) {
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		// 203.0.113.1 — 203.0.113.254 (исключаем .0 и .255)
		octet := rand.Intn(254) + 1 //nolint:gosec
		addr.AllocatedIPv4 = fmt.Sprintf("203.0.113.%d", octet)
		result, err := s.repo.Insert(ctx, addr)
		if err == nil {
			return result, nil
		}
		// Проверяем на UNIQUE violation по allocated_ipv4; при любой другой ошибке — возвращаем сразу
		if isUniqueViolation(err) && containsStr(err.Error(), "allocated_ipv4") {
			continue
		}
		return nil, err
	}
	return nil, coreerrors.Aborted("failed to allocate IP address after retries").Err()
}

// GetByUID возвращает адрес по UID.
func (s *AddressService) GetByUID(ctx context.Context, uid string) (*domain.Address, error) {
	if uid == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}
	return s.repo.GetByUID(ctx, uid)
}

// List возвращает список адресов.
func (s *AddressService) List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.Address, string, int64, error) {
	return s.repo.List(ctx, selectors, page)
}

// SnapshotResourceVersion возвращает текущее значение глобального sequence.
func (s *AddressService) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	return s.repo.SnapshotResourceVersion(ctx)
}

// Delete удаляет адрес по UID.
func (s *AddressService) Delete(ctx context.Context, uid string) error {
	if uid == "" {
		return coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}

	existing, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return err
	}
	if existing == nil {
		return coreerrors.NotFound("Address", uid).Err()
	}

	return s.repo.HardDelete(ctx, uid)
}

// UpdateStatus обновляет статус адреса (Internal RPC).
// Допустимые переходы: RESERVED→IN_USE, IN_USE→RELEASED, RELEASED→RESERVED.
func (s *AddressService) UpdateStatus(ctx context.Context, uid, newState string) error {
	if uid == "" {
		return coreerrors.InvalidArgument().AddFieldViolation("uid", "uid is required").Err()
	}

	existing, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return err
	}
	if existing == nil {
		return coreerrors.NotFound("Address", uid).Err()
	}

	// Проверяем допустимость перехода
	if !isValidStateTransition(existing.State, newState) {
		return coreerrors.FailedPrecondition(
			fmt.Sprintf("invalid state transition: %s → %s", existing.State, newState),
		).Err()
	}

	return s.repo.UpdateStatus(ctx, uid, newState)
}

// HasDependents у Address нет дочерних ресурсов.
func (s *AddressService) HasDependents(ctx context.Context, uid string) (bool, []string, error) {
	has, err := s.repo.HasDependents(ctx, uid)
	if err != nil {
		return false, nil, err
	}
	if has {
		return true, []string{}, nil
	}
	return false, nil, nil
}

func isValidStateTransition(from, to string) bool {
	switch from {
	case "RESERVED":
		return to == "IN_USE"
	case "IN_USE":
		return to == "RELEASED"
	case "RELEASED":
		return to == "RESERVED"
	}
	return false
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx error code 23505 = unique_violation
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.AlreadyExists
	}
	// Проверяем строку ошибки как запасной вариант
	msg := err.Error()
	return contains(msg, "23505") || contains(msg, "unique_violation") || contains(msg, "duplicate key")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func addressDiff(existing, incoming *domain.Address) bool {
	if existing.DisplayName != incoming.DisplayName {
		return true
	}
	if existing.Description != incoming.Description {
		return true
	}
	if existing.ZoneID != incoming.ZoneID {
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
