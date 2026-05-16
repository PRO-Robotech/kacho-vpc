package addresspool

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DeleteAddressPoolUseCase — admin-only Delete. Bindings (network_default /
// address_override) удаляются автоматически через ON DELETE RESTRICT FK — они
// блокируют delete: caller должен сначала Unbind.
//
// Delete pool'а запрещён если из него выделены IP (есть Address с
// external_ipv4.address_pool_id = id и непустым address). FK constraint
// невозможен (адрес ссылается через JSONB, не через колонку) —
// service-level guard обязателен.
type DeleteAddressPoolUseCase struct {
	pools AddressPoolRepo
}

// NewDeleteAddressPoolUseCase собирает use-case.
func NewDeleteAddressPoolUseCase(pools AddressPoolRepo) *DeleteAddressPoolUseCase {
	return &DeleteAddressPoolUseCase{pools: pools}
}

// Execute удаляет AddressPool. Verbatim семантика legacy-сервиса.
func (u *DeleteAddressPoolUseCase) Execute(ctx context.Context, id string) error {
	if _, err := u.pools.Get(ctx, id); err != nil {
		return err
	}
	n, err := u.pools.CountAddressesByPool(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"AddressPool %s is not empty (%d allocated addresses); release IPs first", id, n)
	}
	return u.pools.Delete(ctx, id)
}
