package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory AddressPool reader/writer для kachomock. Wave 5 replicate
// (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.7): полное покрытие AddressPool
// + bindings + cloud-selector kachomock-ом — единый CQRS-mock для всех admin-
// only ресурсов IPAM.
//
// AddressPool — admin-only global ресурс (без folder_id), но в остальном
// parity с гавайки CRUD-pattern Gateway/Network.

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

func (r *addressPoolReader) FindBySelectorMatch(_ context.Context, sel map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*kacho.AddressPoolRecord, error) {
	if len(sel) == 0 {
		return nil, repo.ErrNotFound
	}
	if limit <= 0 {
		limit = 1
	}
	var out []*kacho.AddressPoolRecord
	for _, p := range r.snap {
		if p.Kind != kind {
			continue
		}
		if p.ZoneID != zoneID && p.ZoneID != "" {
			continue
		}
		if len(p.SelectorLabels) == 0 {
			continue
		}
		// containment: networkSelector ⊆ pool.SelectorLabels
		match := true
		for k, v := range sel {
			if p.SelectorLabels[k] != v {
				match = false
				break
			}
		}
		if match {
			cp := *p
			out = append(out, &cp)
		}
	}
	if len(out) == 0 {
		return nil, repo.ErrNotFound
	}
	// сортируем по убыванию точности (diff selector_labels - sel) и priority desc.
	sort.SliceStable(out, func(i, j int) bool {
		di := len(out[i].SelectorLabels) - len(sel)
		dj := len(out[j].SelectorLabels) - len(sel)
		if di != dj {
			return di < dj
		}
		return out[i].SelectorPriority > out[j].SelectorPriority
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *addressPoolReader) FindAmbiguousSelectorGroups(_ context.Context, _ string) ([][]*kacho.AddressPoolRecord, error) {
	return nil, nil
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
	// Фильтр deleted-ids.
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

func (aw *addressPoolWriter) FindBySelectorMatch(_ context.Context, sel map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*kacho.AddressPoolRecord, error) {
	rd := &addressPoolReader{snap: aw.w.localAPs}
	out, err := rd.FindBySelectorMatch(context.Background(), sel, zoneID, kind, limit)
	if err != nil {
		return nil, err
	}
	filtered := out[:0]
	for _, x := range out {
		if _, deleted := aw.w.deletedAPIDs[x.ID]; deleted {
			continue
		}
		filtered = append(filtered, x)
	}
	if len(filtered) == 0 {
		return nil, repo.ErrNotFound
	}
	return filtered, nil
}

func (aw *addressPoolWriter) FindAmbiguousSelectorGroups(_ context.Context, _ string) ([][]*kacho.AddressPoolRecord, error) {
	return nil, nil
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

// PopulateFreelistForPool — в mock'е no-op (тесты не проверяют freelist-state
// напрямую; они проверяют что use-case попал в этот метод — для этого
// существует SpyAddressPoolWriter wrapper). В CQRS-mock unit-тесте достаточно
// проверить, что Commit прошёл — атомарность DML+populate в одной TX.
func (aw *addressPoolWriter) PopulateFreelistForPool(_ context.Context, _ string) error {
	return nil
}

// Assertions: addressPoolReader/Writer implements iface.
var (
	_ kacho.AddressPoolReaderIface = (*addressPoolReader)(nil)
	_ kacho.AddressPoolWriterIface = (*addressPoolWriter)(nil)
)
