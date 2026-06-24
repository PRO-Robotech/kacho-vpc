// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
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

// Execute возвращает страницу пулов + next-page token.
func (u *ListAddressPoolsUseCase) Execute(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*domain.AddressPool, string, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()

	recs, next, err := rd.AddressPools().List(ctx, f, p)
	if err != nil {
		return nil, "", err
	}
	out := make([]*domain.AddressPool, 0, len(recs))
	for _, r := range recs {
		ap := r.AddressPool
		out = append(out, &ap)
	}
	return out, next, nil
}
