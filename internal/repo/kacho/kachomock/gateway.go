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

// In-memory Gateway reader/writer для kachomock. Файл вынесен из
// `repository.go` отдельно — parity с `address.go` / `route_table.go`.
//
// Gateway — project-level CRUD-ресурс без специфичных domain-операций (нет
// AddCidrBlocks / Attach / etc.). Strict name-validation `corevalidate.NameGateway`
// (lowercase, без uppercase/underscore) — это sync-side в handler'е, в mock'е не
// повторяется.

// ---- Gateway reader ----

// gatewayReader — read-only snapshot Gateway.
type gatewayReader struct {
	snap map[string]*kacho.GatewayRecord
}

func (r *gatewayReader) Get(_ context.Context, id string) (*kacho.GatewayRecord, error) {
	g, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *g
	return &cp, nil
}

func (r *gatewayReader) List(_ context.Context, f kacho.GatewayFilter, _ kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
	var result []*kacho.GatewayRecord
	for _, g := range r.snap {
		if (f.ProjectID == "" || g.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(g.Name) == f.Name) {
			cp := *g
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — фильтрация поверх множества разрешенных ids + те же in-memory
// предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (r *gatewayReader) ListByIDs(_ context.Context, f kacho.GatewayFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.GatewayRecord
	for _, g := range r.snap {
		if _, ok := allowed[g.ID]; !ok {
			continue
		}
		if (f.ProjectID == "" || g.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(g.Name) == f.Name) {
			cp := *g
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ---- Gateway writer ----

// gatewayWriter — write-«TX» Gateway. Writer видит свои writes —
// Get/List поверх localGWs.
type gatewayWriter struct {
	w *writerImpl
}

func (gw *gatewayWriter) Get(_ context.Context, id string) (*kacho.GatewayRecord, error) {
	if _, deleted := gw.w.deletedGWIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	g, ok := gw.w.localGWs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *g
	return &cp, nil
}

// GetForUpdate — in-memory mock не моделирует row-lock; семантика = Get.
func (gw *gatewayWriter) GetForUpdate(ctx context.Context, id string) (*kacho.GatewayRecord, error) {
	return gw.Get(ctx, id)
}

func (gw *gatewayWriter) List(_ context.Context, f kacho.GatewayFilter, _ kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
	var result []*kacho.GatewayRecord
	for id, g := range gw.w.localGWs {
		if _, deleted := gw.w.deletedGWIDs[id]; deleted {
			continue
		}
		if (f.ProjectID == "" || g.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(g.Name) == f.Name) {
			cp := *g
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — writer-side: фильтрация поверх множества разрешенных ids + те же
// in-memory предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (gw *gatewayWriter) ListByIDs(_ context.Context, f kacho.GatewayFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.GatewayRecord
	for id, g := range gw.w.localGWs {
		if _, deleted := gw.w.deletedGWIDs[id]; deleted {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if (f.ProjectID == "" || g.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(g.Name) == f.Name) {
			cp := *g
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (gw *gatewayWriter) Insert(_ context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	rec := &kacho.GatewayRecord{Gateway: *g, CreatedAt: time.Now().UTC()}
	gw.w.localGWs[g.ID] = rec
	cp := *rec
	return &cp, nil
}

func (gw *gatewayWriter) Update(_ context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	if _, deleted := gw.w.deletedGWIDs[g.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := gw.w.localGWs[g.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.Gateway = *g
	cp := *existing
	return &cp, nil
}

func (gw *gatewayWriter) Delete(_ context.Context, id string) error {
	if _, ok := gw.w.localGWs[id]; !ok {
		return repo.ErrNotFound
	}
	if gw.w.deletedGWIDs == nil {
		gw.w.deletedGWIDs = make(map[string]struct{})
	}
	gw.w.deletedGWIDs[id] = struct{}{}
	delete(gw.w.localGWs, id)
	return nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.GatewayReaderIface = (*gatewayReader)(nil)
	_ kacho.GatewayWriterIface = (*gatewayWriter)(nil)
)
