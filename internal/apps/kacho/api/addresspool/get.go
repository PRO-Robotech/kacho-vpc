package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetAddressPoolUseCase — sync read AddressPool по id.
type GetAddressPoolUseCase struct {
	pools AddressPoolRepo
}

// NewGetAddressPoolUseCase собирает use-case.
func NewGetAddressPoolUseCase(pools AddressPoolRepo) *GetAddressPoolUseCase {
	return &GetAddressPoolUseCase{pools: pools}
}

// Execute возвращает AddressPool по id. ErrNotFound если не существует.
func (u *GetAddressPoolUseCase) Execute(ctx context.Context, id string) (*domain.AddressPool, error) {
	return u.pools.Get(ctx, id)
}
