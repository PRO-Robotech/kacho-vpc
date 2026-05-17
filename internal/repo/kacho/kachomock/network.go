package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory Network reader/writer для kachomock. Wave 5 pilot (KAC-94, skill
// evgeniy §6 G.7): полное покрытие 8 ресурсов VPC kachomock-ом. Network был
// первым ресурсом на CQRS-Repository pilot'е (Wave 5 KAC-94); файл вынесен из
// `repository.go` отдельно — parity с `address.go` / `route_table.go` и др.
//
// Network-specific operations:
//   - SetProjectID — Move (NetworkService.Move).
//   - SetDefaultSGID — узкая UPDATE-операция: устанавливает Network.default_security_group_id.
//     Используется inline в Network.Create при `KACHO_VPC_DEFAULT_SG_INLINE=true`,
//     когда default SG создаётся в той же writer-TX (см. CreateDefaultSGUseCase
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

// ---- Network writer ----

type networkWriter struct {
	w *writerImpl
}

// Reader-методы writer'а — поверх local (writer видит свои writes, G.2).
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

func (nw *networkWriter) SetProjectID(_ context.Context, id, folderID string) (*kacho.NetworkRecord, error) {
	if _, deleted := nw.w.deletedIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.local[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	n.ProjectID = folderID
	cp := *n
	return &cp, nil
}

// SetDefaultSGID — узкая UPDATE-операция (parity с pg-impl). Wave 5 batch 33/34 (KAC-94).
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

// Assertion: networkReader/Writer implements iface.
var (
	_ kacho.NetworkReaderIface = (*networkReader)(nil)
	_ kacho.NetworkWriterIface = (*networkWriter)(nil)
)
