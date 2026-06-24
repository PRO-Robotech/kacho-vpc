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

// In-memory Network reader/writer для kachomock. Файл вынесен из
// `repository.go` отдельно — parity с `address.go` / `route_table.go` и др.
//
// Network-specific operations:
//   - SetDefaultSGID — узкая UPDATE-операция: устанавливает Network.default_security_group_id.
//     Используется inline в Network.Create при `KACHO_VPC_DEFAULT_SG_INLINE=true`,
//     когда default SG создается в той же writer-TX (см. CreateDefaultSGUseCase
//     + `network.create.doCreate`).

// ---- Network reader ----

type networkReader struct {
	snap map[string]*kacho.NetworkRecord
}

func (r *networkReader) Get(_ context.Context, id string) (*kacho.NetworkRecord, error) {
	n, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (r *networkReader) List(_ context.Context, f kacho.NetworkFilter, _ kacho.Pagination) ([]*kacho.NetworkRecord, string, error) {
	var result []*kacho.NetworkRecord
	for _, n := range r.snap {
		if (f.ProjectID == "" || n.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(n.Name) == f.Name) {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — фильтрация поверх множества разрешенных ids + те же in-memory
// предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (r *networkReader) ListByIDs(_ context.Context, f kacho.NetworkFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.NetworkRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.NetworkRecord
	for _, n := range r.snap {
		if _, ok := allowed[n.ID]; !ok {
			continue
		}
		if (f.ProjectID == "" || n.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(n.Name) == f.Name) {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ---- Network writer ----

type networkWriter struct {
	w *writerImpl
}

// Reader-методы writer'а — поверх local (writer видит свои writes).
func (nw *networkWriter) Get(_ context.Context, id string) (*kacho.NetworkRecord, error) {
	if _, deleted := nw.w.deletedIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.local[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

// GetForUpdate — in-memory mock не моделирует row-lock; семантика = Get.
func (nw *networkWriter) GetForUpdate(ctx context.Context, id string) (*kacho.NetworkRecord, error) {
	return nw.Get(ctx, id)
}

func (nw *networkWriter) List(_ context.Context, f kacho.NetworkFilter, _ kacho.Pagination) ([]*kacho.NetworkRecord, string, error) {
	var result []*kacho.NetworkRecord
	for id, n := range nw.w.local {
		if _, deleted := nw.w.deletedIDs[id]; deleted {
			continue
		}
		if (f.ProjectID == "" || n.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(n.Name) == f.Name) {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — writer-side: фильтрация поверх множества разрешенных ids + те же
// in-memory предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (nw *networkWriter) ListByIDs(_ context.Context, f kacho.NetworkFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.NetworkRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.NetworkRecord
	for id, n := range nw.w.local {
		if _, deleted := nw.w.deletedIDs[id]; deleted {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if (f.ProjectID == "" || n.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(n.Name) == f.Name) {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (nw *networkWriter) Insert(_ context.Context, n *domain.Network) (*kacho.NetworkRecord, error) {
	rec := &kacho.NetworkRecord{Network: *n, CreatedAt: time.Now().UTC()}
	nw.w.local[n.ID] = rec
	cp := *rec
	return &cp, nil
}

func (nw *networkWriter) Update(_ context.Context, n *domain.Network) (*kacho.NetworkRecord, error) {
	if _, deleted := nw.w.deletedIDs[n.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := nw.w.local[n.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.Network = *n
	cp := *existing
	return &cp, nil
}

// SetDefaultSGID — узкая UPDATE-операция (parity с pg-impl).
func (nw *networkWriter) SetDefaultSGID(_ context.Context, id, sgID string) (*kacho.NetworkRecord, error) {
	if _, deleted := nw.w.deletedIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.local[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	n.DefaultSecurityGroupID = sgID
	cp := *n
	return &cp, nil
}

func (nw *networkWriter) Delete(_ context.Context, id string) error {
	if _, ok := nw.w.local[id]; !ok {
		return repo.ErrNotFound
	}
	if nw.w.deletedIDs == nil {
		nw.w.deletedIDs = make(map[string]struct{})
	}
	nw.w.deletedIDs[id] = struct{}{}
	delete(nw.w.local, id)
	return nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.NetworkReaderIface = (*networkReader)(nil)
	_ kacho.NetworkWriterIface = (*networkWriter)(nil)
)
