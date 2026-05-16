package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Wave 5 replicate (KAC-94, Address batch): in-memory mock-impl Address-CQRS
// (parity с NetworkRecord/SecurityGroupRecord/RouteTableRecord). Использовался
// для unit-тестов use-case'ов Address когда они мигрируют на kacho.Repository.
// Сейчас (Wave 5 replicate phase) Address use-case'ы продолжают работать через
// узкий `internal/repo/repomock.AddressRepo` — этот mock здесь нужен лишь для
// того, чтобы кmach.Repository интерфейс был полностью реализуем (compile-time
// gate). Атомарность writer-TX как у Network/SG.

// ---- Address reader ----

type addressReader struct {
	snap map[string]*kacho.AddressRecord
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
		if (f.FolderID == "" || a.FolderID == f.FolderID) &&
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

func (r *addressReader) GetReference(_ context.Context, _ string) (*domain.AddressReference, error) {
	// Mock не моделирует address_references — для unit-тестов use-case'ов
	// references-tracking покрывается `internal/repo/repomock.AddressRepo`.
	return nil, repo.ErrNotFound
}

func (r *addressReader) ReferencesForAddresses(_ context.Context, _ []string) (map[string]*domain.AddressReference, error) {
	return map[string]*domain.AddressReference{}, nil
}

// ---- Address writer ----

type addressWriter struct {
	w *writerImpl
}

// Reader-методы writer'а — поверх localAddrs (writer видит свои writes, G.2).
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

func (aw *addressWriter) List(_ context.Context, f kacho.AddressFilter, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	var result []*kacho.AddressRecord
	for id, a := range aw.w.localAddrs {
		if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
			continue
		}
		if (f.FolderID == "" || a.FolderID == f.FolderID) &&
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

func (aw *addressWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.AddressRecord, error) {
	if _, deleted := aw.w.deletedAddrIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	a, ok := aw.w.localAddrs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	a.FolderID = folderID
	cp := *a
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
// ErrPoolExhausted чтобы вызов сразу fail'ил. Для unit-тестов use-case'ов с
// pools=nil путь Allocate*External* НЕ должен достигать addressWriter (в
// CreateAddressUseCase есть `if u.pools != nil` guard).
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

// Referrer-tracking stubs (минимальная семантика для compile-time gate).
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

// Assertion: addressWriter/Reader implements iface.
var (
	_ kacho.AddressReaderIface = (*addressReader)(nil)
	_ kacho.AddressWriterIface = (*addressWriter)(nil)
)
