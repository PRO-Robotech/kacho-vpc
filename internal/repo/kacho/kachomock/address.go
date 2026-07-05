// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kachomock

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory mock Address-CQRS (parity с NetworkRecord/SecurityGroupRecord/
// RouteTableRecord). Address use-case'ы работают через узкий
// `internal/repo/repomock.AddressRepo`; этот mock нужен, чтобы интерфейс
// kacho.Repository был полностью реализуем (compile-time gate). Атомарность
// writer-TX как у Network/SG.

// ---- Address reader ----

type addressReader struct {
	snap map[string]*kacho.AddressRecord
	// refs — seeded referrer-строки по address_id (кто использует адрес + owned).
	// nil, если тест не seed'ил referrer'ов.
	refs map[string]*domain.AddressReference
}

func (r *addressReader) Get(_ context.Context, id string) (*kacho.AddressRecord, error) {
	a, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (r *addressReader) List(_ context.Context, f kacho.AddressFilter, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	var result []*kacho.AddressRecord
	for _, a := range r.snap {
		if (f.ProjectID == "" || a.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(a.Name) == f.Name) {
			cp := *a
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — фильтрация поверх множества разрешенных ids + те же in-memory
// предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (r *addressReader) ListByIDs(_ context.Context, f kacho.AddressFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.AddressRecord
	for _, a := range r.snap {
		if _, ok := allowed[a.ID]; !ok {
			continue
		}
		if (f.ProjectID == "" || a.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(a.Name) == f.Name) {
			cp := *a
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (r *addressReader) GetByValue(_ context.Context, ext, intl, _ string) (*kacho.AddressRecord, error) {
	for _, a := range r.snap {
		if ext != "" && a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ext {
			cp := *a
			return &cp, nil
		}
		if intl != "" && a.InternalIpv4 != nil && a.InternalIpv4.Address == intl {
			cp := *a
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (r *addressReader) ExistsIP(_ context.Context, ip string) (bool, error) {
	for _, a := range r.snap {
		if a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ip {
			return true, nil
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.Address == ip {
			return true, nil
		}
	}
	return false, nil
}

func (r *addressReader) GetReference(_ context.Context, id string) (*domain.AddressReference, error) {
	// Seeded referrer (SeedReference) → возвращаем его; иначе ErrNotFound. Более
	// богатая референс-семантика для use-case'ов покрывается
	// `internal/repo/repomock.AddressRepo`.
	if ref, ok := r.refs[id]; ok && ref != nil {
		cp := *ref
		return &cp, nil
	}
	return nil, repo.ErrNotFound
}

func (r *addressReader) ReferencesForAddresses(_ context.Context, ids []string) (map[string]*domain.AddressReference, error) {
	out := make(map[string]*domain.AddressReference, len(ids))
	for _, id := range ids {
		if ref, ok := r.refs[id]; ok && ref != nil {
			cp := *ref
			out[id] = &cp
		}
	}
	return out, nil
}

// ---- Address writer ----

type addressWriter struct {
	w *writerImpl
}

// Reader-методы writer'а — поверх localAddrs (writer видит свои writes).
func (aw *addressWriter) Get(_ context.Context, id string) (*kacho.AddressRecord, error) {
	if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	a, ok := aw.w.localAddrs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

// GetForUpdate — in-memory mock не моделирует row-lock; делегирует Get
// (сериализация проверяется integration-тестом на реальном Postgres).
func (aw *addressWriter) GetForUpdate(ctx context.Context, id string) (*kacho.AddressRecord, error) {
	return aw.Get(ctx, id)
}

func (aw *addressWriter) List(_ context.Context, f kacho.AddressFilter, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	var result []*kacho.AddressRecord
	for id, a := range aw.w.localAddrs {
		if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
			continue
		}
		if (f.ProjectID == "" || a.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(a.Name) == f.Name) {
			cp := *a
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — writer-side: фильтрация поверх множества разрешенных ids + те же
// in-memory предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (aw *addressWriter) ListByIDs(_ context.Context, f kacho.AddressFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.AddressRecord
	for id, a := range aw.w.localAddrs {
		if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if (f.ProjectID == "" || a.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(a.Name) == f.Name) {
			cp := *a
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (aw *addressWriter) GetByValue(_ context.Context, ext, intl, _ string) (*kacho.AddressRecord, error) {
	for id, a := range aw.w.localAddrs {
		if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
			continue
		}
		if ext != "" && a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ext {
			cp := *a
			return &cp, nil
		}
		if intl != "" && a.InternalIpv4 != nil && a.InternalIpv4.Address == intl {
			cp := *a
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (aw *addressWriter) ExistsIP(_ context.Context, ip string) (bool, error) {
	for _, a := range aw.w.localAddrs {
		if a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ip {
			return true, nil
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.Address == ip {
			return true, nil
		}
	}
	return false, nil
}

func (aw *addressWriter) GetReference(_ context.Context, _ string) (*domain.AddressReference, error) {
	return nil, repo.ErrNotFound
}

func (aw *addressWriter) ReferencesForAddresses(_ context.Context, _ []string) (map[string]*domain.AddressReference, error) {
	return map[string]*domain.AddressReference{}, nil
}

func (aw *addressWriter) Insert(_ context.Context, a *domain.Address) (*kacho.AddressRecord, error) {
	rec := &kacho.AddressRecord{Address: *a, CreatedAt: time.Now().UTC()}
	aw.w.localAddrs[a.ID] = rec
	cp := *rec
	return &cp, nil
}

func (aw *addressWriter) Update(_ context.Context, a *domain.Address) (*kacho.AddressRecord, error) {
	if _, deleted := aw.w.deletedAddrIDs[a.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := aw.w.localAddrs[a.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.Address = *a
	cp := *existing
	return &cp, nil
}

func (aw *addressWriter) Delete(_ context.Context, id string) error {
	if _, ok := aw.w.localAddrs[id]; !ok {
		return repo.ErrNotFound
	}
	if aw.w.deletedAddrIDs == nil {
		aw.w.deletedAddrIDs = make(map[string]struct{})
	}
	aw.w.deletedAddrIDs[id] = struct{}{}
	delete(aw.w.localAddrs, id)
	return nil
}

func (aw *addressWriter) DeleteGuarded(_ context.Context, id string) (*kacho.AddressRecord, error) {
	existing, ok := aw.w.localAddrs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if existing.DeletionProtection {
		return nil, fmt.Errorf("%w: address %s has deletion_protection enabled; clear it via Update before Delete", repo.ErrFailedPrecondition, id)
	}
	if existing.Used {
		return nil, fmt.Errorf("%w: address %s is in use", repo.ErrFailedPrecondition, id)
	}
	cp := *existing
	if aw.w.deletedAddrIDs == nil {
		aw.w.deletedAddrIDs = make(map[string]struct{})
	}
	aw.w.deletedAddrIDs[id] = struct{}{}
	delete(aw.w.localAddrs, id)
	return &cp, nil
}

func (aw *addressWriter) SetIPSpec(_ context.Context, id string, ext *domain.ExternalIpv4Spec, intn *domain.InternalIpv4Spec) (*kacho.AddressRecord, error) {
	if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	a, ok := aw.w.localAddrs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if ext != nil {
		a.ExternalIpv4 = ext
	}
	if intn != nil {
		a.InternalIpv4 = intn
	}
	cp := *a
	return &cp, nil
}

func (aw *addressWriter) SetInternalIPv6(_ context.Context, id string, spec *domain.InternalIpv6Spec) (*kacho.AddressRecord, error) {
	if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	a, ok := aw.w.localAddrs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if spec != nil {
		a.InternalIpv6 = spec
	}
	cp := *a
	return &cp, nil
}

// IPAM allocate-stubs — mock не моделирует freelist/cursor; возвращает
// ErrPoolExhausted, чтобы вызов сразу fail'ил. Для unit-тестов use-case'ов с
// pools=nil путь Allocate*External* НЕ должен достигать addressWriter (в
// CreateAddressUseCase есть guard `if u.pools != nil`).
func (aw *addressWriter) AllocateIPFromFreelist(_ context.Context, _, _ string) (string, error) {
	return "", repo.ErrPoolExhausted
}

func (aw *addressWriter) ReturnIPToFreelist(_ context.Context, _, _ string) error {
	return nil
}

func (aw *addressWriter) InitIPv6PoolCursor(_ context.Context, _ string) error {
	return nil
}

func (aw *addressWriter) AllocateExternalIPv6(_ context.Context, _, _, _ string) (string, error) {
	return "", repo.ErrPoolExhausted
}

func (aw *addressWriter) FreeExternalIPv6(_ context.Context, _ string) error {
	return nil
}

// Referrer-tracking stubs — минимальная семантика для compile-time gate.
func (aw *addressWriter) SetReference(_ context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	a, ok := aw.w.localAddrs[ref.AddressID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	a.Used = true
	cp := *ref
	cp.AttachedAt = time.Now()
	return &cp, nil
}

func (aw *addressWriter) MarkEphemeralInUse(_ context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	a, ok := aw.w.localAddrs[ref.AddressID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	a.Reserved = false
	a.Used = true
	cp := *ref
	cp.AttachedAt = time.Now()
	return &cp, nil
}

func (aw *addressWriter) ClearReference(_ context.Context, addressID string) error {
	a, ok := aw.w.localAddrs[addressID]
	if !ok {
		return repo.ErrNotFound
	}
	a.Used = false
	return nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.AddressReaderIface = (*addressReader)(nil)
	_ kacho.AddressWriterIface = (*addressWriter)(nil)
)
