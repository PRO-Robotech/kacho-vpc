package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory RouteTable reader/writer для kachomock. Wave 5 replicate (KAC-94):
// parity с Network/SG. Auto-association (KAC-56) в мок не моделируется — это
// DB-side PL/pgSQL trigger; для unit-тестов use-case'ов это не нужно
// (integration-тест на pg покрывает trigger).
//
// Файл вынесен отдельно от `repository.go`, чтобы не конфликтовать с
// параллельной работой над другими ресурсами в одном monolithic-файле.

// routeTableReader — read-only snapshot RT.
type routeTableReader struct {
	snap map[string]*kacho.RouteTableRecord
}

func (r *routeTableReader) Get(_ context.Context, id string) (*kacho.RouteTableRecord, error) {
	rt, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *rt
	return &cp, nil
}

func (r *routeTableReader) List(_ context.Context, f kacho.RouteTableFilter, _ kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
	var result []*kacho.RouteTableRecord
	for _, rt := range r.snap {
		if (f.FolderID == "" || rt.FolderID == f.FolderID) &&
			(f.NetworkID == "" || rt.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(rt.Name) == f.Name) {
			cp := *rt
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (r *routeTableReader) ListByNetwork(ctx context.Context, networkID string, p kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
	return r.List(ctx, kacho.RouteTableFilter{NetworkID: networkID}, p)
}

// routeTableWriter — write-«TX» RT. Writer видит свои writes (G.2) — Get/List
// поверх localRTs.
type routeTableWriter struct {
	w *writerImpl
}

func (rw *routeTableWriter) Get(_ context.Context, id string) (*kacho.RouteTableRecord, error) {
	if _, deleted := rw.w.deletedRTIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	rt, ok := rw.w.localRTs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *rt
	return &cp, nil
}

func (rw *routeTableWriter) List(_ context.Context, f kacho.RouteTableFilter, _ kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
	var result []*kacho.RouteTableRecord
	for id, rt := range rw.w.localRTs {
		if _, deleted := rw.w.deletedRTIDs[id]; deleted {
			continue
		}
		if (f.FolderID == "" || rt.FolderID == f.FolderID) &&
			(f.NetworkID == "" || rt.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(rt.Name) == f.Name) {
			cp := *rt
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (rw *routeTableWriter) ListByNetwork(ctx context.Context, networkID string, p kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
	return rw.List(ctx, kacho.RouteTableFilter{NetworkID: networkID}, p)
}

func (rw *routeTableWriter) Insert(_ context.Context, rt *domain.RouteTable) (*kacho.RouteTableRecord, error) {
	rec := &kacho.RouteTableRecord{RouteTable: *rt, CreatedAt: time.Now().UTC()}
	rw.w.localRTs[rt.ID] = rec
	cp := *rec
	return &cp, nil
}

func (rw *routeTableWriter) Update(_ context.Context, rt *domain.RouteTable) (*kacho.RouteTableRecord, error) {
	if _, deleted := rw.w.deletedRTIDs[rt.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := rw.w.localRTs[rt.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.RouteTable = *rt
	cp := *existing
	return &cp, nil
}

func (rw *routeTableWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.RouteTableRecord, error) {
	if _, deleted := rw.w.deletedRTIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	rt, ok := rw.w.localRTs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	rt.FolderID = folderID
	cp := *rt
	return &cp, nil
}

func (rw *routeTableWriter) Delete(_ context.Context, id string) error {
	if _, ok := rw.w.localRTs[id]; !ok {
		return repo.ErrNotFound
	}
	if rw.w.deletedRTIDs == nil {
		rw.w.deletedRTIDs = make(map[string]struct{})
	}
	rw.w.deletedRTIDs[id] = struct{}{}
	delete(rw.w.localRTs, id)
	return nil
}

// Assertion: routeTableReader/Writer implements iface (Wave 5 G.7 parity).
var (
	_ kacho.RouteTableReaderIface = (*routeTableReader)(nil)
	_ kacho.RouteTableWriterIface = (*routeTableWriter)(nil)
)
