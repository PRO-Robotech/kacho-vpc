package addresspool

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// BindAsNetworkDefaultUseCase — назначить pool как default для Network.
// Family-agnostic (B13): family-фильтр применяется на resolve-этапе, не на bind.
type BindAsNetworkDefaultUseCase struct {
	pools    AddressPoolRepo
	bindings AddressPoolBindingRepo
	netRepo  NetworkRepo
}

// NewBindAsNetworkDefaultUseCase собирает use-case.
func NewBindAsNetworkDefaultUseCase(pools AddressPoolRepo, bindings AddressPoolBindingRepo, netRepo NetworkRepo) *BindAsNetworkDefaultUseCase {
	return &BindAsNetworkDefaultUseCase{pools: pools, bindings: bindings, netRepo: netRepo}
}

// Execute проверяет Network и AddressPool существуют, затем upsert'ит binding.
func (u *BindAsNetworkDefaultUseCase) Execute(ctx context.Context, networkID, poolID string) error {
	if _, err := u.netRepo.Get(ctx, networkID); err != nil {
		return err
	}
	if _, err := u.pools.Get(ctx, poolID); err != nil {
		return err
	}
	return u.bindings.SetNetworkDefault(ctx, networkID, poolID)
}

// UnbindNetworkDefaultUseCase — снятие per-network binding'а (идемпотентно).
type UnbindNetworkDefaultUseCase struct {
	bindings AddressPoolBindingRepo
}

// NewUnbindNetworkDefaultUseCase собирает use-case.
func NewUnbindNetworkDefaultUseCase(bindings AddressPoolBindingRepo) *UnbindNetworkDefaultUseCase {
	return &UnbindNetworkDefaultUseCase{bindings: bindings}
}

// Execute удаляет binding. Idempotent — no error если binding не задан.
func (u *UnbindNetworkDefaultUseCase) Execute(ctx context.Context, networkID string) error {
	return u.bindings.UnsetNetworkDefault(ctx, networkID)
}

// BindAsAddressOverrideUseCase — назначить override-pool для Address.
// Возвращает FailedPrecondition если у Address уже выделен external IP
// (override был бы no-op, поэтому отвергаем — caller должен это знать).
// Family-agnostic (B13): family-фильтр на resolve.
type BindAsAddressOverrideUseCase struct {
	pools    AddressPoolRepo
	bindings AddressPoolBindingRepo
	addrRepo AddressRepo
}

// NewBindAsAddressOverrideUseCase собирает use-case.
func NewBindAsAddressOverrideUseCase(pools AddressPoolRepo, bindings AddressPoolBindingRepo, addrRepo AddressRepo) *BindAsAddressOverrideUseCase {
	return &BindAsAddressOverrideUseCase{pools: pools, bindings: bindings, addrRepo: addrRepo}
}

// Execute проверяет Address/Pool существуют + у Address нет allocated IP,
// затем upsert'ит binding.
func (u *BindAsAddressOverrideUseCase) Execute(ctx context.Context, addressID, poolID string) error {
	a, err := u.addrRepo.Get(ctx, addressID)
	if err != nil {
		return err
	}
	if _, err := u.pools.Get(ctx, poolID); err != nil {
		return err
	}
	if a.ExternalIpv4 != nil && a.ExternalIpv4.Address != "" {
		return status.Errorf(codes.FailedPrecondition,
			"address %s already has allocated external IP %q; override would be a no-op",
			addressID, a.ExternalIpv4.Address)
	}
	return u.bindings.SetAddressOverride(ctx, addressID, poolID)
}

// UnbindAddressOverrideUseCase — снятие per-address override'а (идемпотентно).
type UnbindAddressOverrideUseCase struct {
	bindings AddressPoolBindingRepo
}

// NewUnbindAddressOverrideUseCase собирает use-case.
func NewUnbindAddressOverrideUseCase(bindings AddressPoolBindingRepo) *UnbindAddressOverrideUseCase {
	return &UnbindAddressOverrideUseCase{bindings: bindings}
}

// Execute удаляет binding. Idempotent — no error если binding не задан.
func (u *UnbindAddressOverrideUseCase) Execute(ctx context.Context, addressID string) error {
	return u.bindings.UnsetAddressOverride(ctx, addressID)
}

// SetCloudPoolSelectorUseCase — admin устанавливает selector на Cloud.
// Cloud должен существовать в kacho-resource-manager (peer-check не делаем —
// существование cloud_id ловится на CloudPoolSelectorRepo.Set FK; legacy
// behaviour сохранён).
type SetCloudPoolSelectorUseCase struct {
	cloudSel CloudPoolSelectorRepo
}

// NewSetCloudPoolSelectorUseCase собирает use-case.
func NewSetCloudPoolSelectorUseCase(cloudSel CloudPoolSelectorRepo) *SetCloudPoolSelectorUseCase {
	return &SetCloudPoolSelectorUseCase{cloudSel: cloudSel}
}

// Execute upsert'ит selector. Пустой selector ≡ отсутствие binding'а
// (в cascade-resolve skip'аем label-step).
func (u *SetCloudPoolSelectorUseCase) Execute(ctx context.Context, cloudID string, selector map[string]string, setBy string) error {
	if cloudID == "" {
		return status.Error(codes.InvalidArgument, "cloud_id required")
	}
	return u.cloudSel.Set(ctx, cloudID, selector, setBy)
}

// UnsetCloudPoolSelectorUseCase — снятие admin-controlled selector'а.
type UnsetCloudPoolSelectorUseCase struct {
	cloudSel CloudPoolSelectorRepo
}

// NewUnsetCloudPoolSelectorUseCase собирает use-case.
func NewUnsetCloudPoolSelectorUseCase(cloudSel CloudPoolSelectorRepo) *UnsetCloudPoolSelectorUseCase {
	return &UnsetCloudPoolSelectorUseCase{cloudSel: cloudSel}
}

// Execute удаляет selector. Idempotent.
func (u *UnsetCloudPoolSelectorUseCase) Execute(ctx context.Context, cloudID string) error {
	if cloudID == "" {
		return status.Error(codes.InvalidArgument, "cloud_id required")
	}
	return u.cloudSel.Unset(ctx, cloudID)
}

// GetCloudPoolSelectorUseCase — admin-read selector'а.
type GetCloudPoolSelectorUseCase struct {
	cloudSel CloudPoolSelectorRepo
}

// NewGetCloudPoolSelectorUseCase собирает use-case.
func NewGetCloudPoolSelectorUseCase(cloudSel CloudPoolSelectorRepo) *GetCloudPoolSelectorUseCase {
	return &GetCloudPoolSelectorUseCase{cloudSel: cloudSel}
}

// Execute возвращает CloudPoolSelector. ErrNotFound если binding не задан.
func (u *GetCloudPoolSelectorUseCase) Execute(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	return u.cloudSel.Get(ctx, cloudID)
}
