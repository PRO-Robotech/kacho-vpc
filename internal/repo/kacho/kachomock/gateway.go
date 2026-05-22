package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory Gateway reader/writer для kachomock. Wave 5 replicate (KAC-94, skill
// evgeniy §6 G.7): полное покрытие 8 ресурсов VPC kachomock-ом. Файл вынесен из
// `repository.go` отдельно — parity с `address.go` / `route_table.go`.
//
// Gateway — folder-level CRUD-ресурс без специфичных domain-операций (нет
// AddCidrBlocks / Attach / etc.). Strict name-validation `corevalidate.NameGateway`
// (lowercase, no uppercase/underscore — verbatim YC) — это sync-side в handler'е,
// в mock'е не повторяется.

// ---- Gateway reader ----

// gatewayReader — read-only snapshot Gateway.
type gatewayReader struct {
	snap map[string]*kacho.GatewayRecord
}

// Get возвращает копию Gateway-записи по id (repo.ErrNotFound если нет).
func (r *gatewayReader) Get(_ context.Context, id string) (*kacho.GatewayRecord, error) {
	g, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *g
	return &cp, nil
}

// List возвращает Gateway-записи, отфильтрованные по ProjectID/Name.
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

// ---- Gateway writer ----

// gatewayWriter — write-«TX» Gateway. Writer видит свои writes (G.2) —
// Get/List поверх localGWs.
type gatewayWriter struct {
	w *writerImpl
}

// Get возвращает Gateway-запись из writer-локального стора (исключая удалённые).
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

// List возвращает Gateway-записи из writer-локального стора (исключая удалённые).
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

// Insert сохраняет новый Gateway в writer-локальный стор с CreatedAt = now.
func (gw *gatewayWriter) Insert(_ context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	rec := &kacho.GatewayRecord{Gateway: *g, CreatedAt: time.Now().UTC()}
	gw.w.localGWs[g.ID] = rec
	cp := *rec
	return &cp, nil
}

// Update перезаписывает domain-поля Gateway в writer-локальном сторе.
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

// SetProjectID меняет ProjectID Gateway в writer-локальном сторе (Move).
func (gw *gatewayWriter) SetProjectID(_ context.Context, id, folderID string) (*kacho.GatewayRecord, error) {
	if _, deleted := gw.w.deletedGWIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	g, ok := gw.w.localGWs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	g.ProjectID = folderID
	cp := *g
	return &cp, nil
}

// Delete помечает Gateway удалённым в writer-локальном сторе.
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

// Assertion: gatewayReader/Writer implements iface.
var (
	_ kacho.GatewayReaderIface = (*gatewayReader)(nil)
	_ kacho.GatewayWriterIface = (*gatewayWriter)(nil)
)
