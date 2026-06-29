// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"

	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetAddressPoolUseCase — sync read AddressPool по id. Открывает Reader-TX
// kacho.Repository (чтение уходит на slave-pool, если настроен; иначе master
// fallback).
type GetAddressPoolUseCase struct {
	repo Repo
}

// NewGetAddressPoolUseCase собирает use-case.
func NewGetAddressPoolUseCase(r Repo) *GetAddressPoolUseCase {
	return &GetAddressPoolUseCase{repo: r}
}

// Execute возвращает AddressPoolRecord по id (с DB-managed CreatedAt для
// proto-проекции). ErrNotFound если не существует.
func (u *GetAddressPoolUseCase) Execute(ctx context.Context, id string) (*kachorepo.AddressPoolRecord, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	return rd.AddressPools().Get(ctx, id)
}
