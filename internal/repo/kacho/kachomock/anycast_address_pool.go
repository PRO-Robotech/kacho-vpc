// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory AnycastAddressPool reader/writer для kachomock. Моделирует CRUD +
// pivot пул↔сеть + seed-override живых аллокаций. EXCLUDE-инвариант непересечения
// CIDR-претензий — DB-level (миграция 0012), проверяется только в integration.

// ---- AnycastAddressPool reader ----

type anycastAddressPoolReader struct {
	snap   map[string]*kacho.AnycastAddressPoolRecord
	attach map[string]map[string]struct{}
	alloc  map[string]int64
	addrs  map[string]*kacho.AddressRecord
}

func (r *anycastAddressPoolReader) Get(_ context.Context, id string) (*kacho.AnycastAddressPoolRecord, error) {
	p, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *anycastAddressPoolReader) List(ctx context.Context, f kacho.AnycastAddressPoolFilter, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	return r.list(ctx, f, nil, p)
}

func (r *anycastAddressPoolReader) ListByIDs(ctx context.Context, f kacho.AnycastAddressPoolFilter, ids []string, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	allow := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		allow[id] = struct{}{}
	}
	return r.list(ctx, f, allow, p)
}

func (r *anycastAddressPoolReader) list(_ context.Context, f kacho.AnycastAddressPoolFilter, allow map[string]struct{}, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	var all []*kacho.AnycastAddressPoolRecord
	for _, rec := range r.snap {
		if f.ProjectID != "" && rec.ProjectID != f.ProjectID {
			continue
		}
		if f.Name != "" && string(rec.Name) != f.Name {
			continue
		}
		if allow != nil {
			if _, ok := allow[rec.ID]; !ok {
				continue
			}
		}
		if f.NetworkID != "" {
			if _, ok := r.attach[rec.ID][f.NetworkID]; !ok {
				continue
			}
		}
		cp := *rec
		all = append(all, &cp)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID < all[j].ID
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	// Keyset-пагинация по (created_at, id) — parity с pg-impl.
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		filtered := all[:0]
		for _, rec := range all {
			if rec.CreatedAt.After(ts) || (rec.CreatedAt.Equal(ts) && rec.ID > id) {
				filtered = append(filtered, rec)
			}
		}
		all = filtered
	}
	var nextToken string
	if int64(len(all)) > pageSize {
		last := all[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		all = all[:pageSize]
	}
	return all, nextToken, nil
}

func (r *anycastAddressPoolReader) AllocatedBlocks(_ context.Context) ([]string, error) {
	var out []string
	for _, rec := range r.snap {
		out = append(out, rec.CIDRBlocks...)
	}
	return out, nil
}

func (r *anycastAddressPoolReader) CountAttachments(_ context.Context, poolID string) (int64, error) {
	return int64(len(r.attach[poolID])), nil
}

func (r *anycastAddressPoolReader) IsAttached(_ context.Context, poolID, networkID string) (bool, error) {
	_, ok := r.attach[poolID][networkID]
	return ok, nil
}

// CountAllocationsInNetwork — seed-override (SeedAnycastAllocation) плюс
// фактические anycast-Address'ы пула в сети (зеркаль pg-запроса по
// anycast.pool_id + anycast_network_id), чтобы alloc→detach-guard ловился в unit.
func (r *anycastAddressPoolReader) CountAllocationsInNetwork(_ context.Context, poolID, networkID string) (int64, error) {
	n := r.alloc[poolID+"|"+networkID]
	for _, a := range r.addrs {
		if a.Anycast != nil && a.Anycast.AnycastPoolID == poolID && a.Anycast.NetworkID == networkID {
			n++
		}
	}
	return n, nil
}

// DefaultForFamily — платформенный is_default INTERNAL-пул семейства.
func (r *anycastAddressPoolReader) DefaultForFamily(_ context.Context, ipVersion domain.IpVersion) (*kacho.AnycastAddressPoolRecord, error) {
	for _, rec := range r.snap {
		if rec.IsDefault && rec.Scope == domain.AnycastScopeInternal && rec.IPVersion == ipVersion {
			cp := *rec
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

// ---- AnycastAddressPool writer ----

type anycastAddressPoolWriter struct {
	w *writerImpl
}

// reader строит read-проекцию поверх working-set'а writer'а (с учётом deleted).
func (aw *anycastAddressPoolWriter) reader() *anycastAddressPoolReader {
	snap := make(map[string]*kacho.AnycastAddressPoolRecord, len(aw.w.localAAPs))
	for id, p := range aw.w.localAAPs {
		if _, deleted := aw.w.deletedAAPIDs[id]; deleted {
			continue
		}
		snap[id] = p
	}
	return &anycastAddressPoolReader{snap: snap, attach: aw.w.localAAPAttach, alloc: aw.w.parent.anycastAlloc, addrs: aw.w.localAddrs}
}

func (aw *anycastAddressPoolWriter) Get(ctx context.Context, id string) (*kacho.AnycastAddressPoolRecord, error) {
	return aw.reader().Get(ctx, id)
}

func (aw *anycastAddressPoolWriter) List(ctx context.Context, f kacho.AnycastAddressPoolFilter, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	return aw.reader().List(ctx, f, p)
}

func (aw *anycastAddressPoolWriter) ListByIDs(ctx context.Context, f kacho.AnycastAddressPoolFilter, ids []string, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	return aw.reader().ListByIDs(ctx, f, ids, p)
}

func (aw *anycastAddressPoolWriter) AllocatedBlocks(ctx context.Context) ([]string, error) {
	return aw.reader().AllocatedBlocks(ctx)
}

func (aw *anycastAddressPoolWriter) CountAttachments(ctx context.Context, poolID string) (int64, error) {
	return aw.reader().CountAttachments(ctx, poolID)
}

func (aw *anycastAddressPoolWriter) IsAttached(ctx context.Context, poolID, networkID string) (bool, error) {
	return aw.reader().IsAttached(ctx, poolID, networkID)
}

func (aw *anycastAddressPoolWriter) CountAllocationsInNetwork(ctx context.Context, poolID, networkID string) (int64, error) {
	return aw.reader().CountAllocationsInNetwork(ctx, poolID, networkID)
}

func (aw *anycastAddressPoolWriter) DefaultForFamily(ctx context.Context, ipVersion domain.IpVersion) (*kacho.AnycastAddressPoolRecord, error) {
	return aw.reader().DefaultForFamily(ctx, ipVersion)
}

func (aw *anycastAddressPoolWriter) Insert(_ context.Context, p *domain.AnycastAddressPool) (*kacho.AnycastAddressPoolRecord, error) {
	now := time.Now().UTC()
	rec := &kacho.AnycastAddressPoolRecord{AnycastAddressPool: *p, CreatedAt: now}
	aw.w.localAAPs[p.ID] = rec
	out := *rec
	return &out, nil
}

func (aw *anycastAddressPoolWriter) Update(_ context.Context, p *domain.AnycastAddressPool) (*kacho.AnycastAddressPoolRecord, error) {
	if _, deleted := aw.w.deletedAAPIDs[p.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := aw.w.localAAPs[p.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	// CreatedAt сохраняется (DB-managed); поля name/description/labels из domain.
	existing.AnycastAddressPool = *p
	out := *existing
	return &out, nil
}

func (aw *anycastAddressPoolWriter) Delete(_ context.Context, id string) error {
	if _, ok := aw.w.localAAPs[id]; !ok {
		return repo.ErrNotFound
	}
	if aw.w.deletedAAPIDs == nil {
		aw.w.deletedAAPIDs = make(map[string]struct{})
	}
	aw.w.deletedAAPIDs[id] = struct{}{}
	delete(aw.w.localAAPs, id)
	delete(aw.w.localAAPAttach, id)
	return nil
}

func (aw *anycastAddressPoolWriter) AttachNetwork(_ context.Context, poolID, networkID string, _ []string) error {
	if aw.w.localAAPAttach[poolID] == nil {
		aw.w.localAAPAttach[poolID] = make(map[string]struct{})
	}
	aw.w.localAAPAttach[poolID][networkID] = struct{}{}
	return nil
}

func (aw *anycastAddressPoolWriter) DetachNetwork(_ context.Context, poolID, networkID string) error {
	if nets, ok := aw.w.localAAPAttach[poolID]; ok {
		delete(nets, networkID)
	}
	return nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.AnycastAddressPoolReaderIface = (*anycastAddressPoolReader)(nil)
	_ kacho.AnycastAddressPoolWriterIface = (*anycastAddressPoolWriter)(nil)
)
