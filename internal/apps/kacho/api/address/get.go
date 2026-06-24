// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// enforceGetVisible применяет per-object no-leak: если filter != nil, subject
// не пуст и address id вне accessible-set (того же FGA grant-set, что и List —
// read==enforce) → NotFound с тем же текстом, что и для несуществующего address
// (no existence leak). FGA-ошибка → fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id, resourceName string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeAddress, authzfilter.ActionAddressList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: %s %s not found", serviceerr.ErrNotFound, resourceName, id))
	}
	return nil
}

// GetAddressUseCase — простой read: id-валидация + перевод repo-sentinel в gRPC
// status + обогащение UsedBy (referrer-tracking) + per-object no-leak enforce.
// Use-case можно было бы и опустить, но handler-у удобнее единый шов.
//
// Открывает Reader-TX явно через `repo.Reader(ctx)` — routing на slave-реплику
// станет automatic, когда та появится; пока на той же мастер-pool.
//
// Per-object no-leak: если filter != nil и subject не пуст — после repo.Get
// проверяем, что id входит в accessible-set того же FGA grant-set, что и List
// (read==enforce). filter == nil / subject == "" → enforce делает per-RPC
// interceptor (dev / system-principal).
type GetAddressUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetAddressUseCase создает GetAddressUseCase. filter может быть nil
// (list-filter disabled / dev) → no-leak enforce пропускается.
func NewGetAddressUseCase(r Repo, filter ListFilter) *GetAddressUseCase {
	return &GetAddressUseCase{repo: r, filter: filter}
}

// Execute возвращает repo-entity Address. NotFound → mapRepoErr → gRPC NotFound.
// UsedBy обогащается best-effort (failure → лог + адрес без UsedBy).
// Per-object no-leak: subject без гранта на address → NotFound.
func (u *GetAddressUseCase) Execute(ctx context.Context, subjectID, id string) (*kachorepo.AddressRecord, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	a, err := r.Addresses().Get(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id, "Address"); err != nil {
		return nil, err
	}
	loadUsedBy(ctx, r.Addresses(), []*kachorepo.AddressRecord{a})
	return a, nil
}

// GetByValueUseCase возвращает Address по его IP-значению (external или
// internal). oneof external_ipv4_address / internal_ipv4_address; optional
// subnet_id scope.
type GetByValueUseCase struct {
	repo Repo
}

// NewGetByValueUseCase создает GetByValueUseCase.
func NewGetByValueUseCase(r Repo) *GetByValueUseCase {
	return &GetByValueUseCase{repo: r}
}

// Execute — sync-валидация + lookup по IP + загрузка UsedBy.
func (u *GetByValueUseCase) Execute(ctx context.Context, externalIP, internalIP, subnetID string) (*kachorepo.AddressRecord, error) {
	if externalIP == "" && internalIP == "" {
		return nil, serviceerr.InvalidArg("address", "address (external_ipv4_address or internal_ipv4_address) is required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	a, err := r.Addresses().GetByValue(ctx, externalIP, internalIP, subnetID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	loadUsedBy(ctx, r.Addresses(), []*kachorepo.AddressRecord{a})
	return a, nil
}

// loadUsedBy обогащает каждый адрес из набора полем UsedBy (referrer-tracking,
// output-only) — кто использует адрес. Best-effort: ошибка чтения
// address_references → лог + адреса без UsedBy (graceful degradation, не валит
// чтение). Пустой/nil вход — no-op.
//
// Принимает `AddressReaderIface` (writer-iface тоже его embed'ит) — caller
// передает reader/writer из своей открытой TX.
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
