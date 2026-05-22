package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory PrivateEndpoint reader/writer для kachomock. Wave 5 replicate
// (KAC-94, skill evgeniy §6 G.7): полное покрытие 8 ресурсов VPC kachomock-ом.
// Файл вынесен из `repository.go` отдельно — parity с `address.go` /
// `route_table.go`.
//
// PE — folder-level CRUD-ресурс, привязан к Network + Subnet (privatelink).
// PE-specific FK (network_id/subnet_id/address_id; миграция 0024) — обычные
// within-service refs на DB-уровне; mock не моделирует FK (это работа pg-impl),
// но Get/List/Insert/Update/Delete видят свои writes (G.2).

// ---- PrivateEndpoint reader ----

// privateEndpointReader — read-only snapshot PE.
type privateEndpointReader struct {
	snap map[string]*kacho.PrivateEndpointRecord
}

// Get возвращает копию PrivateEndpoint-записи по id (repo.ErrNotFound если нет).
func (r *privateEndpointReader) Get(_ context.Context, id string) (*kacho.PrivateEndpointRecord, error) {
	pe, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *pe
	return &cp, nil
}

// List возвращает PrivateEndpoint-записи, отфильтрованные по ProjectID/Name.
func (r *privateEndpointReader) List(_ context.Context, f kacho.PrivateEndpointFilter, _ kacho.Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
	var result []*kacho.PrivateEndpointRecord
	for _, pe := range r.snap {
		if (f.ProjectID == "" || pe.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(pe.Name) == f.Name) {
			cp := *pe
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ---- PrivateEndpoint writer ----

// privateEndpointWriter — write-«TX» PE. Writer видит свои writes (G.2) —
// Get/List поверх localPEs.
type privateEndpointWriter struct {
	w *writerImpl
}

// Get возвращает PrivateEndpoint-запись из writer-локального стора (исключая удалённые).
func (pw *privateEndpointWriter) Get(_ context.Context, id string) (*kacho.PrivateEndpointRecord, error) {
	if _, deleted := pw.w.deletedPEIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	pe, ok := pw.w.localPEs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *pe
	return &cp, nil
}

// List возвращает PrivateEndpoint-записи из writer-локального стора (исключая удалённые).
func (pw *privateEndpointWriter) List(_ context.Context, f kacho.PrivateEndpointFilter, _ kacho.Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
	var result []*kacho.PrivateEndpointRecord
	for id, pe := range pw.w.localPEs {
		if _, deleted := pw.w.deletedPEIDs[id]; deleted {
			continue
		}
		if (f.ProjectID == "" || pe.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(pe.Name) == f.Name) {
			cp := *pe
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// Insert сохраняет новый PrivateEndpoint в writer-локальный стор с CreatedAt = now.
func (pw *privateEndpointWriter) Insert(_ context.Context, pe *domain.PrivateEndpoint) (*kacho.PrivateEndpointRecord, error) {
	rec := &kacho.PrivateEndpointRecord{PrivateEndpoint: *pe, CreatedAt: time.Now().UTC()}
	pw.w.localPEs[pe.ID] = rec
	cp := *rec
	return &cp, nil
}

// Update перезаписывает domain-поля PrivateEndpoint в writer-локальном сторе.
func (pw *privateEndpointWriter) Update(_ context.Context, pe *domain.PrivateEndpoint) (*kacho.PrivateEndpointRecord, error) {
	if _, deleted := pw.w.deletedPEIDs[pe.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := pw.w.localPEs[pe.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.PrivateEndpoint = *pe
	cp := *existing
	return &cp, nil
}

// SetProjectID меняет ProjectID PrivateEndpoint в writer-локальном сторе (Move).
func (pw *privateEndpointWriter) SetProjectID(_ context.Context, id, folderID string) (*kacho.PrivateEndpointRecord, error) {
	if _, deleted := pw.w.deletedPEIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	pe, ok := pw.w.localPEs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	pe.ProjectID = folderID
	cp := *pe
	return &cp, nil
}

// Delete помечает PrivateEndpoint удалённым в writer-локальном сторе.
func (pw *privateEndpointWriter) Delete(_ context.Context, id string) error {
	if _, ok := pw.w.localPEs[id]; !ok {
		return repo.ErrNotFound
	}
	if pw.w.deletedPEIDs == nil {
		pw.w.deletedPEIDs = make(map[string]struct{})
	}
	pw.w.deletedPEIDs[id] = struct{}{}
	delete(pw.w.localPEs, id)
	return nil
}

// Assertion: privateEndpointReader/Writer implements iface.
var (
	_ kacho.PrivateEndpointReaderIface = (*privateEndpointReader)(nil)
	_ kacho.PrivateEndpointWriterIface = (*privateEndpointWriter)(nil)
)
