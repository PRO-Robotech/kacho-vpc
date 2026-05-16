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
//
// Wave 5 A.7 sub-PR 1/6: Get + CountAddressesByPool + Delete + outbox-emit идут
// в одной writer-TX kacho.Repository.Writer(ctx).
type DeleteAddressPoolUseCase struct {
	repo Repo
}

// NewDeleteAddressPoolUseCase собирает use-case.
func NewDeleteAddressPoolUseCase(r Repo) *DeleteAddressPoolUseCase {
	return &DeleteAddressPoolUseCase{repo: r}
}

// Execute удаляет AddressPool. Verbatim семантика legacy-сервиса.
func (u *DeleteAddressPoolUseCase) Execute(ctx context.Context, id string) error {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if _, err := w.AddressPools().Get(ctx, id); err != nil {
		return err
	}
	n, err := w.AddressPools().CountAddressesByPool(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"AddressPool %s is not empty (%d allocated addresses); release IPs first", id, n)
	}
	if err := w.AddressPools().Delete(ctx, id); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPool", id, "DELETED", map[string]any{"id": id}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}
