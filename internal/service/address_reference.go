package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressReferenceService — Sync (не Operation) referrer-tracking над Address.
// Wave 3 (KAC-94): эти методы раньше висели на `*AddressService` (extension
// methods на fat-сервисе). После переноса CRUD-логики Address в use-case-пакет
// `internal/apps/kacho/api/address/` AddressService удалён; референс-методы
// собраны в собственный сервис, который инжектируется в `Internal*`-handler'ы
// напрямую через свой port (см. task §4 — «инжектируем как dependency»).
//
// Использует только узкий контракт AddressRepo (SetReference / MarkEphemeralInUse /
// ClearReference / GetReference) — нам не нужен общий AddressRepo с List/Insert/…
// для этой работы, но мы переиспользуем ports.AddressRepo чтобы не плодить
// параллельный интерфейс. Реализация — тот же `repo.AddressRepo`, который
// инжектируется и в Address use-case-пакет, и в pool service.
type AddressReferenceService struct {
	repo AddressRepo
}

// NewAddressReferenceService создаёт AddressReferenceService.
func NewAddressReferenceService(repo AddressRepo) *AddressReferenceService {
	return &AddressReferenceService{repo: repo}
}

// SetAddressReferenceReq — параметры привязки referrer'а к адресу.
type SetAddressReferenceReq struct {
	AddressID    string
	ReferrerType string
	ReferrerID   string
	ReferrerName string
}

// SetAddressReference upsert'ит referrer-row адреса (кто его использует) и
// выставляет Address.used=true. Идемпотентно. Sync RPC (не Operation).
//
// Errors: InvalidArgument (пустой/malformed address_id, пустой referrer_type/id),
// NotFound (address не существует).
func (s *AddressReferenceService) SetAddressReference(ctx context.Context, req SetAddressReferenceReq) (*domain.AddressReference, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, req.AddressID); err != nil {
		return nil, err
	}
	if req.ReferrerType == "" {
		return nil, status.Error(codes.InvalidArgument, "referrer_type required")
	}
	if req.ReferrerID == "" {
		return nil, status.Error(codes.InvalidArgument, "referrer_id required")
	}
	ref, err := s.repo.SetReference(ctx, &domain.AddressReference{
		AddressID:    req.AddressID,
		ReferrerType: req.ReferrerType,
		ReferrerID:   req.ReferrerID,
		ReferrerName: req.ReferrerName,
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return ref, nil
}

// MarkAddressEphemeralInUse атомарно (одна tx): выставляет Address.reserved=false,
// Address.used=true и upsert'ит referrer-row (= SetAddressReference + сброс
// reserved). Используется kacho-compute для эфемерных NIC/NAT Address-ресурсов,
// которые он сам создал через публичный AddressService.Create (там reserved=true
// verbatim YC, но для авто-аллоцированного NIC-адреса это неверно — в YC такой
// адрес не reserved). Идемпотентно. Sync RPC (не Operation).
//
// Errors: InvalidArgument (пустой/malformed address_id, пустой referrer_type/id),
// NotFound (address не существует).
func (s *AddressReferenceService) MarkAddressEphemeralInUse(ctx context.Context, req SetAddressReferenceReq) (*domain.AddressReference, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, req.AddressID); err != nil {
		return nil, err
	}
	if req.ReferrerType == "" {
		return nil, status.Error(codes.InvalidArgument, "referrer_type required")
	}
	if req.ReferrerID == "" {
		return nil, status.Error(codes.InvalidArgument, "referrer_id required")
	}
	ref, err := s.repo.MarkEphemeralInUse(ctx, &domain.AddressReference{
		AddressID:    req.AddressID,
		ReferrerType: req.ReferrerType,
		ReferrerID:   req.ReferrerID,
		ReferrerName: req.ReferrerName,
	})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return ref, nil
}

// ClearAddressReference удаляет referrer-row адреса (no-op если нет) и
// выставляет Address.used=false. Sync RPC.
//
// Errors: InvalidArgument (пустой/malformed address_id), NotFound (address не существует).
func (s *AddressReferenceService) ClearAddressReference(ctx context.Context, addressID string) error {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return err
	}
	if err := s.repo.ClearReference(ctx, addressID); err != nil {
		return mapRepoErr(err)
	}
	return nil
}

// GetAddressReference возвращает referrer-row адреса. Sync RPC.
//
// Errors: InvalidArgument (пустой/malformed address_id), NotFound (address не
// существует ИЛИ у него нет referrer'а).
func (s *AddressReferenceService) GetAddressReference(ctx context.Context, addressID string) (*domain.AddressReference, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return nil, err
	}
	ref, err := s.repo.GetReference(ctx, addressID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return ref, nil
}
