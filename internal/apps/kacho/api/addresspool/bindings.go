package addresspool

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// BindAsNetworkDefaultUseCase — назначить pool как default для Network.
// Family-agnostic (B13): family-фильтр применяется на resolve-этапе, не на bind.
//
// Wave 5 A.7 sub-PR 1/6: Network/Pool существование + Set binding + outbox-
// emit идут в одной writer-TX kacho.Repository.Writer(ctx).
type BindAsNetworkDefaultUseCase struct {
	repo    Repo
	netRepo NetworkRepo
}

// NewBindAsNetworkDefaultUseCase собирает use-case.
func NewBindAsNetworkDefaultUseCase(r Repo, netRepo NetworkRepo) *BindAsNetworkDefaultUseCase {
	return &BindAsNetworkDefaultUseCase{repo: r, netRepo: netRepo}
}

// Execute проверяет Network и AddressPool существуют, затем upsert'ит binding.
func (u *BindAsNetworkDefaultUseCase) Execute(ctx context.Context, networkID, poolID string) error {
	if _, err := u.netRepo.Get(ctx, networkID); err != nil {
		return err
	}
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if _, err := w.AddressPools().Get(ctx, poolID); err != nil {
		return err
	}
	if err := w.AddressPoolBindings().SetNetworkDefault(ctx, networkID, poolID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPoolNetworkDefault", networkID, "UPDATED",
		map[string]any{"network_id": networkID, "pool_id": poolID}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}

// UnbindNetworkDefaultUseCase — снятие per-network binding'а (идемпотентно).
type UnbindNetworkDefaultUseCase struct {
	repo Repo
}

// NewUnbindNetworkDefaultUseCase собирает use-case.
func NewUnbindNetworkDefaultUseCase(r Repo) *UnbindNetworkDefaultUseCase {
	return &UnbindNetworkDefaultUseCase{repo: r}
}

// Execute удаляет binding. Idempotent — no error если binding не задан.
func (u *UnbindNetworkDefaultUseCase) Execute(ctx context.Context, networkID string) error {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if err := w.AddressPoolBindings().UnsetNetworkDefault(ctx, networkID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPoolNetworkDefault", networkID, "DELETED",
		map[string]any{"network_id": networkID}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}

// BindAsAddressOverrideUseCase — назначить override-pool для Address.
// Возвращает FailedPrecondition если у Address уже выделен external IP
// (override был бы no-op, поэтому отвергаем — caller должен это знать).
// Family-agnostic (B13): family-фильтр на resolve.
//
// Wave 5 A.7 sub-PR 1/6: Address/Pool exists + Set binding + outbox в одной
// writer-TX.
type BindAsAddressOverrideUseCase struct {
	repo     Repo
	addrRepo AddressRepo
}

// NewBindAsAddressOverrideUseCase собирает use-case.
func NewBindAsAddressOverrideUseCase(r Repo, addrRepo AddressRepo) *BindAsAddressOverrideUseCase {
	return &BindAsAddressOverrideUseCase{repo: r, addrRepo: addrRepo}
}

// Execute проверяет Address/Pool существуют + у Address нет allocated IP,
// затем upsert'ит binding.
func (u *BindAsAddressOverrideUseCase) Execute(ctx context.Context, addressID, poolID string) error {
	a, err := u.addrRepo.Get(ctx, addressID)
	if err != nil {
		return err
	}
	if a.ExternalIpv4 != nil && a.ExternalIpv4.Address != "" {
		return status.Errorf(codes.FailedPrecondition,
			"address %s already has allocated external IP %q; override would be a no-op",
			addressID, a.ExternalIpv4.Address)
	}

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if _, err := w.AddressPools().Get(ctx, poolID); err != nil {
		return err
	}
	if err := w.AddressPoolBindings().SetAddressOverride(ctx, addressID, poolID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPoolAddressOverride", addressID, "UPDATED",
		map[string]any{"address_id": addressID, "pool_id": poolID}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}

// UnbindAddressOverrideUseCase — снятие per-address override'а (идемпотентно).
type UnbindAddressOverrideUseCase struct {
	repo Repo
}

// NewUnbindAddressOverrideUseCase собирает use-case.
func NewUnbindAddressOverrideUseCase(r Repo) *UnbindAddressOverrideUseCase {
	return &UnbindAddressOverrideUseCase{repo: r}
}

// Execute удаляет binding. Idempotent — no error если binding не задан.
func (u *UnbindAddressOverrideUseCase) Execute(ctx context.Context, addressID string) error {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if err := w.AddressPoolBindings().UnsetAddressOverride(ctx, addressID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPoolAddressOverride", addressID, "DELETED",
		map[string]any{"address_id": addressID}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}

// SetCloudPoolSelectorUseCase — admin устанавливает selector на Cloud.
// Cloud должен существовать в kacho-resource-manager (peer-check не делаем —
// существование cloud_id ловится на DB-уровне; legacy behaviour сохранён).
//
// Wave 5 A.7 sub-PR 1/6: Set + outbox в одной writer-TX.
type SetCloudPoolSelectorUseCase struct {
	repo Repo
}

// NewSetCloudPoolSelectorUseCase собирает use-case.
func NewSetCloudPoolSelectorUseCase(r Repo) *SetCloudPoolSelectorUseCase {
	return &SetCloudPoolSelectorUseCase{repo: r}
}

// Execute upsert'ит selector. Пустой selector ≡ отсутствие binding'а
// (в cascade-resolve skip'аем label-step).
func (u *SetCloudPoolSelectorUseCase) Execute(ctx context.Context, cloudID string, selector map[string]string, setBy string) error {
	if cloudID == "" {
		return status.Error(codes.InvalidArgument, "cloud_id required")
	}
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if err := w.CloudPoolSelectors().Set(ctx, cloudID, selector, setBy); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "CloudPoolSelector", cloudID, "UPDATED", map[string]any{
		"cloud_id": cloudID, "selector": normalizeMapForPayload(selector), "set_by": setBy,
	}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}

// UnsetCloudPoolSelectorUseCase — снятие admin-controlled selector'а.
type UnsetCloudPoolSelectorUseCase struct {
	repo Repo
}

// NewUnsetCloudPoolSelectorUseCase собирает use-case.
func NewUnsetCloudPoolSelectorUseCase(r Repo) *UnsetCloudPoolSelectorUseCase {
	return &UnsetCloudPoolSelectorUseCase{repo: r}
}

// Execute удаляет selector. Idempotent.
func (u *UnsetCloudPoolSelectorUseCase) Execute(ctx context.Context, cloudID string) error {
	if cloudID == "" {
		return status.Error(codes.InvalidArgument, "cloud_id required")
	}
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if err := w.CloudPoolSelectors().Unset(ctx, cloudID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "CloudPoolSelector", cloudID, "DELETED",
		map[string]any{"cloud_id": cloudID}); err != nil {
		return status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	return w.Commit()
}

// GetCloudPoolSelectorUseCase — admin-read selector'а.
type GetCloudPoolSelectorUseCase struct {
	repo Repo
}

// NewGetCloudPoolSelectorUseCase собирает use-case.
func NewGetCloudPoolSelectorUseCase(r Repo) *GetCloudPoolSelectorUseCase {
	return &GetCloudPoolSelectorUseCase{repo: r}
}

// Execute возвращает CloudPoolSelector. ErrNotFound если binding не задан.
func (u *GetCloudPoolSelectorUseCase) Execute(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	return rd.CloudPoolSelectors().Get(ctx, cloudID)
}

// normalizeMapForPayload — nil → empty map (deterministic outbox payload).
func normalizeMapForPayload(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
