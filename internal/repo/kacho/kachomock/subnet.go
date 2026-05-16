package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory Subnet reader/writer для kachomock. Wave 5 replicate (KAC-94, skill
// evgeniy §6 G.7): полное покрытие 8 ресурсов VPC kachomock-ом. Файл вынесен из
// `repository.go` отдельно от Address/RouteTable/Network — parity с
// `address.go` / `route_table.go`, чтобы каждый ресурс жил в собственном файле.
//
// Subnet-specific operations: AddressesBySubnet (внутри read-snapshot'а — для
// SubnetService.Delete precheck и ListUsedAddresses), SetCidrBlocks
// (AddCidrBlocks/RemoveCidrBlocks; EXCLUDE constraint на pg-side не
// моделируется), SetZoneID (Relocate — verbatim YC `FailedPrecondition`, но
// writer-iface метод оставлен для completeness).

// ---- Subnet reader ----

// subnetReader — read-only snapshot Subnet (+ addresses для AddressesBySubnet).
type subnetReader struct {
	snap  map[string]*kacho.SubnetRecord
	addrs map[string]*kacho.AddressRecord
}

func (r *subnetReader) Get(_ context.Context, id string) (*kacho.SubnetRecord, error) {
	s, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (r *subnetReader) List(_ context.Context, f kacho.SubnetFilter, _ kacho.Pagination) ([]*kacho.SubnetRecord, string, error) {
	var result []*kacho.SubnetRecord
	for _, s := range r.snap {
		if (f.FolderID == "" || s.FolderID == f.FolderID) &&
			(f.NetworkID == "" || s.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(s.Name) == f.Name) {
			cp := *s
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// AddressesBySubnet — filter by internal_ipv4.subnet_id / internal_ipv6.subnet_id.
// Simplified mock: фильтрует addrs по совпадению spec.SubnetID. Pagination в
// тестах не нужна — возвращаем всё за один вызов.
func (r *subnetReader) AddressesBySubnet(_ context.Context, subnetID string, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	var result []*kacho.AddressRecord
	for _, a := range r.addrs {
		if a.InternalIpv4 != nil && a.InternalIpv4.SubnetID == subnetID {
			cp := *a
			result = append(result, &cp)
			continue
		}
		if a.InternalIpv6 != nil && a.InternalIpv6.SubnetID == subnetID {
			cp := *a
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ---- Subnet writer ----

// subnetWriter — write-«TX» Subnet. Writer видит свои writes (G.2) —
// Get/List/AddressesBySubnet поверх localSubs.
type subnetWriter struct {
	w *writerImpl
}

func (sw *subnetWriter) Get(_ context.Context, id string) (*kacho.SubnetRecord, error) {
	if _, deleted := sw.w.deletedSubIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	s, ok := sw.w.localSubs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (sw *subnetWriter) List(_ context.Context, f kacho.SubnetFilter, _ kacho.Pagination) ([]*kacho.SubnetRecord, string, error) {
	var result []*kacho.SubnetRecord
	for id, s := range sw.w.localSubs {
		if _, deleted := sw.w.deletedSubIDs[id]; deleted {
			continue
		}
		if (f.FolderID == "" || s.FolderID == f.FolderID) &&
			(f.NetworkID == "" || s.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(s.Name) == f.Name) {
			cp := *s
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (sw *subnetWriter) AddressesBySubnet(_ context.Context, subnetID string, _ kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	var result []*kacho.AddressRecord
	for _, a := range sw.w.localAddrs {
		if a.InternalIpv4 != nil && a.InternalIpv4.SubnetID == subnetID {
			cp := *a
			result = append(result, &cp)
			continue
		}
		if a.InternalIpv6 != nil && a.InternalIpv6.SubnetID == subnetID {
			cp := *a
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (sw *subnetWriter) Insert(_ context.Context, s *domain.Subnet) (*kacho.SubnetRecord, error) {
	rec := &kacho.SubnetRecord{Subnet: *s, CreatedAt: time.Now().UTC()}
	sw.w.localSubs[s.ID] = rec
	cp := *rec
	return &cp, nil
}

func (sw *subnetWriter) Update(_ context.Context, s *domain.Subnet) (*kacho.SubnetRecord, error) {
	if _, deleted := sw.w.deletedSubIDs[s.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := sw.w.localSubs[s.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.Subnet = *s
	cp := *existing
	return &cp, nil
}

func (sw *subnetWriter) Delete(_ context.Context, id string) error {
	if _, ok := sw.w.localSubs[id]; !ok {
		return repo.ErrNotFound
	}
	if sw.w.deletedSubIDs == nil {
		sw.w.deletedSubIDs = make(map[string]struct{})
	}
	sw.w.deletedSubIDs[id] = struct{}{}
	delete(sw.w.localSubs, id)
	return nil
}

func (sw *subnetWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.SubnetRecord, error) {
	if _, deleted := sw.w.deletedSubIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	s, ok := sw.w.localSubs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	s.FolderID = folderID
	cp := *s
	return &cp, nil
}

func (sw *subnetWriter) SetCidrBlocks(_ context.Context, id string, v4, v6 []string) (*kacho.SubnetRecord, error) {
	if _, deleted := sw.w.deletedSubIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	s, ok := sw.w.localSubs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	s.V4CidrBlocks = v4
	s.V6CidrBlocks = v6
	cp := *s
	return &cp, nil
}

func (sw *subnetWriter) SetZoneID(_ context.Context, id, zoneID string) (*kacho.SubnetRecord, error) {
	if _, deleted := sw.w.deletedSubIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	s, ok := sw.w.localSubs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	s.ZoneID = zoneID
	cp := *s
	return &cp, nil
}

// Assertion: subnetReader/Writer implements iface.
var (
	_ kacho.SubnetReaderIface = (*subnetReader)(nil)
	_ kacho.SubnetWriterIface = (*subnetWriter)(nil)
)
