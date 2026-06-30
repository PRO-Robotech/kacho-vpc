// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package addressref — sync (не Operation) referrer-tracking над Address.
//
// Это не-resource service: не относится ни к одному use-case в `api/<resource>/`,
// но и не horizontal helper. Тип называется `Service` (полный referrer —
// `addressref.Service`), конструктор — `NewService`.
package addressref

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Repo — узкий port-интерфейс над `repo.AddressRepo`: только методы, нужные
// для referrer-tracking. `repo.AddressRepo` ⊇ этого интерфейса.
type Repo interface {
	Get(ctx context.Context, id string) (*kachorepo.AddressRecord, error)
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	ClearReference(ctx context.Context, addressID string) error
	GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
}

// Service — sync (не Operation) референс-операции над Address. Референс-методы
// собраны в отдельный сервис и инжектируются в `Internal*`-handler'ы напрямую
// через свой port.
type Service struct {
	repo Repo
}

// NewService создает Service.
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

// SetAnycastReferenceReq — параметры BYO-привязки tenant'ского anycast-Address к
// referrer'у (напр. LoadBalancer). ExpectProjectID/ExpectIPVersion — guard
// ownership/family: адрес обязан принадлежать ожидаемому проекту и семейству.
type SetAnycastReferenceReq struct {
	AddressID       string
	ReferrerType    string
	ReferrerID      string
	ReferrerName    string
	ExpectProjectID string           // ownership guard (пусто → не проверяется)
	ExpectIPVersion domain.IpVersion // family guard (Unspecified → не проверяется)
}

// errIllegalAddressID — generic InvalidArgument для BYO-guard: не подтверждает
// существование/ownership чужого адреса (анти-oracle, ADR §8).
var errIllegalAddressID = status.Error(codes.InvalidArgument, "Illegal argument addressId")

// SetAnycastReference — BYO-привязка anycast-Address к referrer'у с guard'ом
// ownership/family. Адрес обязан быть anycast'ом, принадлежать ожидаемому проекту
// и семейству — иначе generic "Illegal argument addressId" (без подтверждения
// чужого ownership/существования). used_by-CAS: занятый другим referrer'ом адрес
// → FailedPrecondition "address is already in use". Sync RPC (не Operation).
func (s *Service) SetAnycastReference(ctx context.Context, req SetAnycastReferenceReq) (*domain.AddressReference, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, req.AddressID); err != nil {
		return nil, err
	}
	if req.ReferrerType == "" {
		return nil, status.Error(codes.InvalidArgument, "referrer_type required")
	}
	if req.ReferrerID == "" {
		return nil, status.Error(codes.InvalidArgument, "referrer_id required")
	}
	a, err := s.repo.Get(ctx, req.AddressID)
	if err != nil {
		// Не подтверждаем существование чужого/несуществующего адреса.
		if errors.Is(err, repo.ErrNotFound) {
			return nil, errIllegalAddressID
		}
		return nil, serviceerr.MapRepoErr(err)
	}
	// Guard: только anycast-Address ожидаемого проекта и семейства.
	if a.Anycast == nil {
		return nil, errIllegalAddressID
	}
	if req.ExpectProjectID != "" && a.ProjectID != req.ExpectProjectID {
		return nil, errIllegalAddressID
	}
	if req.ExpectIPVersion != domain.IpVersionUnspecified && a.IpVersion != req.ExpectIPVersion {
		return nil, errIllegalAddressID
	}
	ref, err := s.repo.SetReference(ctx, &domain.AddressReference{
		AddressID:    req.AddressID,
		ReferrerType: req.ReferrerType,
		ReferrerID:   req.ReferrerID,
		ReferrerName: req.ReferrerName,
	})
	if err != nil {
		// used_by-CAS: адрес уже привязан к другому referrer'у.
		if errors.Is(err, repo.ErrFailedPrecondition) {
			return nil, status.Error(codes.FailedPrecondition, "address is already in use")
		}
		return nil, serviceerr.MapRepoErr(err)
	}
	return ref, nil
}

// MarkAddressEphemeralInUse атомарно (одна tx): выставляет Address.reserved=false,
// Address.used=true и upsert'ит referrer-row (= SetAddressReference + сброс
// reserved). Используется kacho-compute для эфемерных NIC/NAT Address-ресурсов,
// которые он сам создал через публичный AddressService.Create (там по умолчанию
// reserved=true, но для авто-аллоцированного NIC-адреса это неверно — такой адрес
// reserved быть не должен). Идемпотентно. Sync RPC (не Operation).
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
