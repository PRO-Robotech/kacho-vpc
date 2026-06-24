// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory AddressPool reader/writer для kachomock: полное покрытие AddressPool
// + bindings + cloud-selector — единый CQRS-mock для всех admin-only ресурсов IPAM.
//
// AddressPool — admin-only global ресурс (без project_id), но в остальном
// parity с CRUD-pattern Gateway/Network.

// ---- AddressPool reader ----

type addressPoolReader struct {
	snap map[string]*kacho.AddressPoolRecord
}

func (r *addressPoolReader) Get(_ context.Context, id string) (*kacho.AddressPoolRecord, error) {
	p, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *addressPoolReader) List(_ context.Context, f kacho.AddressPoolFilter, _ kacho.Pagination) ([]*kacho.AddressPoolRecord, string, error) {
	var result []*kacho.AddressPoolRecord
	for _, p := range r.snap {
		if f.Kind != domain.AddressPoolKindUnspecified && p.Kind != f.Kind {
			continue
		}
		if f.ZoneID != "" && p.ZoneID != f.ZoneID {
			continue
		}
		cp := *p
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (r *addressPoolReader) GetDefaultForZone(_ context.Context, zoneID string, kind domain.AddressPoolKind) (*kacho.AddressPoolRecord, error) {
	for _, p := range r.snap {
		if p.Kind == kind && p.IsDefault && p.ZoneID == zoneID {
			cp := *p
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (r *addressPoolReader) CountAddressesByPool(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (r *addressPoolReader) CountAddressesByPoolPerCIDR(_ context.Context, _ string) (map[string]int64, error) {
	return nil, nil
}

func (r *addressPoolReader) ListAddressesByPool(_ context.Context, _ string, _ string, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	return nil, "", nil
}

// ---- AddressPool writer ----

type addressPoolWriter struct {
	w *writerImpl
}

func (aw *addressPoolWriter) Get(_ context.Context, id string) (*kacho.AddressPoolRecord, error) {
	if _, deleted := aw.w.deletedAPIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	p, ok := aw.w.localAPs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (aw *addressPoolWriter) List(ctx context.Context, f kacho.AddressPoolFilter, p kacho.Pagination) ([]*kacho.AddressPoolRecord, string, error) {
	// Делегируем reader-семантике поверх writer'овского working-set'а.
	rd := &addressPoolReader{snap: aw.w.localAPs}
	out, tok, err := rd.List(ctx, f, p)
	if err != nil {
		return nil, "", err
	}
	// Отфильтровываем удаленные ids.
	filtered := out[:0]
	for _, x := range out {
		if _, deleted := aw.w.deletedAPIDs[x.ID]; deleted {
			continue
		}
		filtered = append(filtered, x)
	}
	return filtered, tok, nil
}

func (aw *addressPoolWriter) GetDefaultForZone(_ context.Context, zoneID string, kind domain.AddressPoolKind) (*kacho.AddressPoolRecord, error) {
	for id, p := range aw.w.localAPs {
		if _, deleted := aw.w.deletedAPIDs[id]; deleted {
			continue
		}
		if p.Kind == kind && p.IsDefault && p.ZoneID == zoneID {
			cp := *p
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (aw *addressPoolWriter) CountAddressesByPool(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (aw *addressPoolWriter) CountAddressesByPoolPerCIDR(_ context.Context, _ string) (map[string]int64, error) {
	return nil, nil
}

func (aw *addressPoolWriter) ListAddressesByPool(_ context.Context, _ string, _ string, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	return nil, "", nil
}

func (aw *addressPoolWriter) Insert(_ context.Context, p *domain.AddressPool) (*kacho.AddressPoolRecord, error) {
	cp := *p
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	rec := &kacho.AddressPoolRecord{AddressPool: cp}
	aw.w.localAPs[p.ID] = rec
	out := *rec
	return &out, nil
}

func (aw *addressPoolWriter) Update(_ context.Context, p *domain.AddressPool) (*kacho.AddressPoolRecord, error) {
	if _, deleted := aw.w.deletedAPIDs[p.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := aw.w.localAPs[p.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.AddressPool = *p
	out := *existing
	return &out, nil
}

func (aw *addressPoolWriter) Delete(_ context.Context, id string) error {
	if _, ok := aw.w.localAPs[id]; !ok {
		return repo.ErrNotFound
	}
	if aw.w.deletedAPIDs == nil {
		aw.w.deletedAPIDs = make(map[string]struct{})
	}
	aw.w.deletedAPIDs[id] = struct{}{}
	delete(aw.w.localAPs, id)
	return nil
}

// LockForUpdate — mock не моделирует row-lock; проверяет лишь существование pool.
func (aw *addressPoolWriter) LockForUpdate(_ context.Context, id string) error {
	if _, ok := aw.w.localAPs[id]; !ok {
		return repo.ErrNotFound
	}
	return nil
}

// PopulateFreelistForPool — в mock'е no-op (тесты не проверяют freelist-state
// напрямую; что use-case дошел до этого метода, проверяет SpyAddressPoolWriter
// wrapper). Для unit-теста достаточно убедиться, что Commit прошел — атомарность
// DML+populate в одной TX.
func (aw *addressPoolWriter) PopulateFreelistForPool(_ context.Context, _ string) error {
	return nil
}

// AddCidrToFreelist — фиксируем вызов в parent (через writer-state), чтобы
// unit-тест AddCidrBlocks мог проверить дельту после Commit. На Commit flush'ится
// в parent.freelistAddedCidrs (см. writerImpl.Commit).
func (aw *addressPoolWriter) AddCidrToFreelist(_ context.Context, poolID string, newV4Cidrs []string) error {
	if aw.w.localFreelistAdds == nil {
		aw.w.localFreelistAdds = make(map[string][]string)
	}
	aw.w.localFreelistAdds[poolID] = append(aw.w.localFreelistAdds[poolID], newV4Cidrs...)
	return nil
}

// DeleteFreelistForCidrs — no-op в mock'е (freelist-state не моделируется).
func (aw *addressPoolWriter) DeleteFreelistForCidrs(_ context.Context, _ string, _ []string) error {
	return nil
}

// CountAllocatedInCidrs — возвращает override из parent.allocatedInCidr (seeded
// тестом); cidr-список mock игнорирует. 0, если не seeded.
func (aw *addressPoolWriter) CountAllocatedInCidrs(_ context.Context, poolID string, _ []string) (int64, error) {
	aw.w.parent.mu.Lock()
	defer aw.w.parent.mu.Unlock()
	return aw.w.parent.allocatedInCidr[poolID], nil
}

// InsertCidrBlocks — no-op в mock'е. EXCLUDE-инвариант (пересечение CIDR) —
// DB-level (миграция 0004) и проверяется только в integration-тестах против
// реального Postgres (address_pool_overlap_integration_test.go). Для unit-теста
// достаточно, что use-case проходит через метод без ошибки (happy-path).
func (aw *addressPoolWriter) InsertCidrBlocks(_ context.Context, _ string, _ domain.AddressPoolKind, _, _ []string) error {
	return nil
}

// DeleteCidrBlocks — no-op в mock'е (address_pool_cidrs-state не моделируется;
// см. InsertCidrBlocks).
func (aw *addressPoolWriter) DeleteCidrBlocks(_ context.Context, _ string, _, _ []string) error {
	return nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.AddressPoolReaderIface = (*addressPoolReader)(nil)
	_ kacho.AddressPoolWriterIface = (*addressPoolWriter)(nil)
)
