package address

import (
	"context"
	"log/slog"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetAddressUseCase — простой read; id-валидация + перевод repo-sentinel в gRPC
// status + обогащение UsedBy (referrer-tracking). Skill evgeniy §2 B.3: use-case
// можно было бы вообще опустить, но handler-у удобнее единый шов.
type GetAddressUseCase struct {
	repo AddressRepo
}

// NewGetAddressUseCase создаёт GetAddressUseCase.
func NewGetAddressUseCase(repo AddressRepo) *GetAddressUseCase {
	return &GetAddressUseCase{repo: repo}
}

// Execute возвращает repo-entity Address. NotFound → mapRepoErr → gRPC NotFound.
// UsedBy обогащается best-effort (failure → лог + адрес без UsedBy).
func (u *GetAddressUseCase) Execute(ctx context.Context, id string) (*kachorepo.AddressRecord, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	a, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	loadUsedBy(ctx, u.repo, []*kachorepo.AddressRecord{a})
	return a, nil
}

// GetByValueUseCase возвращает Address по его IP-значению (external или
// internal). Verbatim YC: oneof external_ipv4_address / internal_ipv4_address;
// optional subnet_id scope.
type GetByValueUseCase struct {
	repo AddressRepo
}

// NewGetByValueUseCase создаёт GetByValueUseCase.
func NewGetByValueUseCase(repo AddressRepo) *GetByValueUseCase {
	return &GetByValueUseCase{repo: repo}
}

// Execute — sync-валидация + lookup по IP + загрузка UsedBy.
func (u *GetByValueUseCase) Execute(ctx context.Context, externalIP, internalIP, subnetID string) (*kachorepo.AddressRecord, error) {
	if externalIP == "" && internalIP == "" {
		return nil, invalidArg("address", "address (external_ipv4_address or internal_ipv4_address) is required")
	}
	a, err := u.repo.GetByValue(ctx, externalIP, internalIP, subnetID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	loadUsedBy(ctx, u.repo, []*kachorepo.AddressRecord{a})
	return a, nil
}

// loadUsedBy обогащает каждый адрес из набора полем UsedBy (referrer-tracking,
// output-only) — кто использует адрес. Best-effort: ошибка чтения
// address_references → лог + адреса без UsedBy (graceful degradation, не валит
// чтение). Пустой/nil вход — no-op.
//
// Live-копия `service.AddressService.loadUsedBy` (Wave 3 migration; общий
// helper можно будет вынести после полного переезда use-case'ов).
func loadUsedBy(ctx context.Context, repo AddressRepo, addrs []*kachorepo.AddressRecord) {
	if len(addrs) == 0 {
		return
	}
	idsList := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a != nil {
			idsList = append(idsList, a.ID)
		}
	}
	if len(idsList) == 0 {
		return
	}
	refs, err := repo.ReferencesForAddresses(ctx, idsList)
	if err != nil {
		slog.WarnContext(ctx, "failed to load address referrers (used_by); returning addresses without it", "err", err)
		return
	}
	for _, a := range addrs {
		if a == nil {
			continue
		}
		if ref, ok := refs[a.ID]; ok && ref != nil {
			a.UsedBy = []*domain.AddressReference{ref}
		}
	}
}
