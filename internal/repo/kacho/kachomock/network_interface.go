package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory NetworkInterface reader/writer для kachomock. Wave 5 replicate
// (KAC-94, skill evgeniy §6 G.7): полное покрытие 8 ресурсов VPC kachomock-ом.
// Файл вынесен из `repository.go` отдельно — parity с `address.go` /
// `route_table.go`.
//
// NIC — самый «толстый» ресурс эпика KAC-2 (AWS-ENI-стиль first-class).
// Mock здесь покрывает:
//   - CRUD (Insert / UpdateMeta / Delete);
//   - ListBySubnet — для SubnetService.Delete precheck (NIC жёстко блокирует
//     свою подсеть через ON DELETE RESTRICT — миграция 0012, KAC-33);
//   - AttachToInstance с упрощённой CAS-семантикой (зеркало pg-impl single-
//     statement conditional UPDATE из KAC-52 / миграции 0016; mock не
//     моделирует concurrent-conflict через row-level lock, но проверяет
//     used_by-clash → ErrFailedPrecondition);
//   - DetachFromInstance — идемпотентный.
//
// **CAS-семантика — упрощённая** (одна row, lock-free); pg-impl на боевой БД
// использует single-statement conditional UPDATE c row-level lock (KAC-52,
// миграция 0016). Здесь — достаточно для unit-тестов use-case'ов. Real-world
// race-coverage — `network_interface_attach_race_integration_test.go`.
//
// **MAC-allocation** в mock не моделируется (caller-side responsibility:
// `service.doCreate` ставит mac через `macutil.GenerateMAC` и retry'ит на
// UNIQUE-collision на mac_address; mock-Insert просто принимает что есть).

// ---- NetworkInterface reader ----

// networkInterfaceReader — read-only snapshot NIC.
type networkInterfaceReader struct {
	snap map[string]*kacho.NetworkInterfaceRecord
}

func (r *networkInterfaceReader) Get(_ context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	n, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (r *networkInterfaceReader) List(_ context.Context, f kacho.NetworkInterfaceFilter, _ kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	var result []*kacho.NetworkInterfaceRecord
	for _, n := range r.snap {
		if (f.FolderID != "" && n.FolderID != f.FolderID) ||
			(f.SubnetID != "" && n.SubnetID != f.SubnetID) ||
			(f.InstanceID != "" && (n.UsedByType != "compute_instance" || n.UsedByID != f.InstanceID)) {
			continue
		}
		cp := *n
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (r *networkInterfaceReader) ListBySubnet(_ context.Context, subnetID string) ([]*kacho.NetworkInterfaceRecord, error) {
	var result []*kacho.NetworkInterfaceRecord
	for _, n := range r.snap {
		if n.SubnetID == subnetID {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// ---- NetworkInterface writer ----

// networkInterfaceWriter — write-«TX» NIC. Writer видит свои writes (G.2) —
// Get/List поверх localNIs.
type networkInterfaceWriter struct {
	w *writerImpl
}

func (nw *networkInterfaceWriter) Get(_ context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (nw *networkInterfaceWriter) List(_ context.Context, f kacho.NetworkInterfaceFilter, _ kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	var result []*kacho.NetworkInterfaceRecord
	for id, n := range nw.w.localNIs {
		if _, deleted := nw.w.deletedNIIDs[id]; deleted {
			continue
		}
		if (f.FolderID != "" && n.FolderID != f.FolderID) ||
			(f.SubnetID != "" && n.SubnetID != f.SubnetID) ||
			(f.InstanceID != "" && (n.UsedByType != "compute_instance" || n.UsedByID != f.InstanceID)) {
			continue
		}
		cp := *n
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (nw *networkInterfaceWriter) ListBySubnet(_ context.Context, subnetID string) ([]*kacho.NetworkInterfaceRecord, error) {
	var result []*kacho.NetworkInterfaceRecord
	for id, n := range nw.w.localNIs {
		if _, deleted := nw.w.deletedNIIDs[id]; deleted {
			continue
		}
		if n.SubnetID == subnetID {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (nw *networkInterfaceWriter) Insert(_ context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
	rec := &kacho.NetworkInterfaceRecord{NetworkInterface: *n, CreatedAt: time.Now().UTC()}
	nw.w.localNIs[n.ID] = rec
	cp := *rec
	return &cp, nil
}

func (nw *networkInterfaceWriter) UpdateMeta(_ context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[n.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := nw.w.localNIs[n.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	// Mutate the mutable fields (parity с pg-impl: name/description/labels/
	// security_group_ids/v4_address_ids/v6_address_ids — immutable: folder_id/
	// subnet_id/mac_address).
	existing.Name = n.Name
	existing.Description = n.Description
	existing.Labels = n.Labels
	existing.SecurityGroupIDs = n.SecurityGroupIDs
	existing.V4AddressIDs = n.V4AddressIDs
	existing.V6AddressIDs = n.V6AddressIDs
	cp := *existing
	return &cp, nil
}

func (nw *networkInterfaceWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	n.FolderID = folderID
	cp := *n
	return &cp, nil
}

// AttachToInstance — упрощённая CAS-семантика: если NIC уже attached к
// другому owner-у → ErrFailedPrecondition (зеркало pg-impl: миграция 0016 /
// KAC-52). Идемпотентный re-attach к тому же owner-у — успех.
func (nw *networkInterfaceWriter) AttachToInstance(_ context.Context, id, refType, refID, refName string) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if n.UsedByID != "" && n.UsedByID != refID {
		return nil, repo.ErrFailedPrecondition
	}
	n.UsedByType, n.UsedByID, n.UsedByName = refType, refID, refName
	n.Status = domain.NIStatusActive
	cp := *n
	return &cp, nil
}

// DetachFromInstance — idempotent: затирает used_by_* + status=AVAILABLE.
func (nw *networkInterfaceWriter) DetachFromInstance(_ context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	n.UsedByType, n.UsedByID, n.UsedByName = "", "", ""
	n.Status = domain.NIStatusAvailable
	cp := *n
	return &cp, nil
}

func (nw *networkInterfaceWriter) Delete(_ context.Context, id string) error {
	if _, ok := nw.w.localNIs[id]; !ok {
		return repo.ErrNotFound
	}
	if nw.w.deletedNIIDs == nil {
		nw.w.deletedNIIDs = make(map[string]struct{})
	}
	nw.w.deletedNIIDs[id] = struct{}{}
	delete(nw.w.localNIs, id)
	return nil
}

// Assertion: networkInterfaceReader/Writer implements iface.
var (
	_ kacho.NetworkInterfaceReaderIface = (*networkInterfaceReader)(nil)
	_ kacho.NetworkInterfaceWriterIface = (*networkInterfaceWriter)(nil)
)
