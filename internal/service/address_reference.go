package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

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
func (s *AddressService) SetAddressReference(ctx context.Context, req SetAddressReferenceReq) (*domain.AddressReference, error) {
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

// ClearAddressReference удаляет referrer-row адреса (no-op если нет) и
// выставляет Address.used=false. Sync RPC.
//
// Errors: InvalidArgument (пустой/malformed address_id), NotFound (address не существует).
func (s *AddressService) ClearAddressReference(ctx context.Context, addressID string) error {
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
func (s *AddressService) GetAddressReference(ctx context.Context, addressID string) (*domain.AddressReference, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return nil, err
	}
	ref, err := s.repo.GetReference(ctx, addressID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return ref, nil
}
