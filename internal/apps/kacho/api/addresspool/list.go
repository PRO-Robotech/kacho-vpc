// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"

	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListAddressPoolsUseCase — admin-only list. AddressPool — глобальный
// infrastructure-ресурс (нет project_id), фильтрация по (zone_id, kind).
// Чтение идет в Reader-TX kacho.Repository.
type ListAddressPoolsUseCase struct {
	repo Repo
}

// NewListAddressPoolsUseCase собирает use-case.
func NewListAddressPoolsUseCase(r Repo) *ListAddressPoolsUseCase {
	return &ListAddressPoolsUseCase{repo: r}
}

// Execute возвращает страницу пулов (AddressPoolRecord, с CreatedAt) + next-page token.
func (u *ListAddressPoolsUseCase) Execute(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*kachorepo.AddressPoolRecord, string, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()

	return rd.AddressPools().List(ctx, f, p)
}
