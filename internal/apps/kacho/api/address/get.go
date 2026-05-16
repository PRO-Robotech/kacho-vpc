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
//
// A.7 sub-PR 2 (KAC-94, skill evgeniy §6 G.5): открывает Reader-TX явно через
// `repo.Reader(ctx)` — routing на slave-реплику станет automatic, когда та
// появится; пока на той же мастер-pool.
type GetAddressUseCase struct {
	repo Repo
}

// NewGetAddressUseCase создаёт GetAddressUseCase.
func NewGetAddressUseCase(r Repo) *GetAddressUseCase {
	return &GetAddressUseCase{repo: r}
}

// Execute возвращает repo-entity Address. NotFound → mapRepoErr → gRPC NotFound.
// UsedBy обогащается best-effort (failure → лог + адрес без UsedBy).
func (u *GetAddressUseCase) Execute(ctx context.Context, id string) (*kachorepo.AddressRecord, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	a, err := r.Addresses().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	loadUsedBy(ctx, r.Addresses(), []*kachorepo.AddressRecord{a})
	return a, nil
}

// GetByValueUseCase возвращает Address по его IP-значению (external или
// internal). Verbatim YC: oneof external_ipv4_address / internal_ipv4_address;
// optional subnet_id scope.
type GetByValueUseCase struct {
	repo Repo
}

// NewGetByValueUseCase создаёт GetByValueUseCase.
func NewGetByValueUseCase(r Repo) *GetByValueUseCase {
	return &GetByValueUseCase{repo: r}
}

// Execute — sync-валидация + lookup по IP + загрузка UsedBy.
func (u *GetByValueUseCase) Execute(ctx context.Context, externalIP, internalIP, subnetID string) (*kachorepo.AddressRecord, error) {
	if externalIP == "" && internalIP == "" {
		return nil, invalidArg("address", "address (external_ipv4_address or internal_ipv4_address) is required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	a, err := r.Addresses().GetByValue(ctx, externalIP, internalIP, subnetID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	loadUsedBy(ctx, r.Addresses(), []*kachorepo.AddressRecord{a})
	return a, nil
}

// loadUsedBy обогащает каждый адрес из набора полем UsedBy (referrer-tracking,
// output-only) — кто использует адрес. Best-effort: ошибка чтения
// address_references → лог + адреса без UsedBy (graceful degradation, не валит
// чтение). Пустой/nil вход — no-op.
//
// A.7 sub-PR 2 (KAC-94): принимает `AddressReaderIface` (writer-iface тоже его
// embed'ит, G.2) — caller передаёт reader/writer из своей открытой TX.
func loadUsedBy(ctx context.Context, addrReader AddressReaderIface, addrs []*kachorepo.AddressRecord) {
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
	refs, err := addrReader.ReferencesForAddresses(ctx, idsList)
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
