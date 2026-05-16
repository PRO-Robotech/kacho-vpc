package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ListAddressPoolsUseCase — admin-only list. AddressPool — глобальный
// infrastructure-ресурс (нет folder_id), фильтрация по (zone_id, kind).
type ListAddressPoolsUseCase struct {
	pools AddressPoolRepo
}

// NewListAddressPoolsUseCase собирает use-case.
func NewListAddressPoolsUseCase(pools AddressPoolRepo) *ListAddressPoolsUseCase {
	return &ListAddressPoolsUseCase{pools: pools}
}

// Execute возвращает страницу пулов + next-page token.
func (u *ListAddressPoolsUseCase) Execute(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*domain.AddressPool, string, error) {
	return u.pools.List(ctx, f, p)
}
