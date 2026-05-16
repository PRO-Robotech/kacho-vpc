// Package addressref — sync (не Operation) referrer-tracking над Address.
//
// Wave 3 cleanup (KAC-94): перенесено из `internal/service/address_reference.go`
// в `internal/apps/kacho/services/addressref/` согласно skill evgeniy §1 A.3 —
// это не-resource service (не относится ни к одному use-case'у в `api/<resource>/`),
// но и не horizontal helper.
//
// Конструктор `NewService` + тип `Service`. Раньше был `AddressReferenceService`
// — переименован к каноничному `Service` (skill evgeniy §3 C.3: имя типа
// дублирует имя пакета в полном referrer'е — `addressref.Service`).
package addressref

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Repo — узкий port-интерфейс над `repo.AddressRepo`: только методы, нужные
// для referrer-tracking. `repo.AddressRepo` ⊇ этого интерфейса.
type Repo interface {
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	ClearReference(ctx context.Context, addressID string) error
	GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
}

// Service — sync (не Operation) референс-операции над Address.
//
// Wave 3 (KAC-94): эти методы раньше висели на `*AddressService` (extension
// methods на fat-сервисе). После переноса CRUD-логики Address в use-case-пакет
// `internal/apps/kacho/api/address/` AddressService удалён; референс-методы
// собраны в собственный сервис, который инжектируется в `Internal*`-handler'ы
// напрямую через свой port.
type Service struct {
	repo Repo
}

// NewService создаёт Service.
func NewService(repo Repo) *Service {
	return &Service{repo: repo}
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
func (s *Service) SetAddressReference(ctx context.Context, req SetAddressReferenceReq) (*domain.AddressReference, error) {
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
		return nil, serviceerr.MapRepoErr(err)
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
func (s *Service) MarkAddressEphemeralInUse(ctx context.Context, req SetAddressReferenceReq) (*domain.AddressReference, error) {
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
		return nil, serviceerr.MapRepoErr(err)
	}
	return ref, nil
}

// ClearAddressReference удаляет referrer-row адреса (no-op если нет) и
// выставляет Address.used=false. Sync RPC.
//
// Errors: InvalidArgument (пустой/malformed address_id), NotFound (address не существует).
func (s *Service) ClearAddressReference(ctx context.Context, addressID string) error {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return err
	}
	if err := s.repo.ClearReference(ctx, addressID); err != nil {
		return serviceerr.MapRepoErr(err)
	}
	return nil
}

// GetAddressReference возвращает referrer-row адреса. Sync RPC.
//
// Errors: InvalidArgument (пустой/malformed address_id), NotFound (address не
// существует ИЛИ у него нет referrer'а).
func (s *Service) GetAddressReference(ctx context.Context, addressID string) (*domain.AddressReference, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return nil, err
	}
	ref, err := s.repo.GetReference(ctx, addressID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return ref, nil
}
