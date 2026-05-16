// Package kachomock — in-memory реализация CQRS-Repository из
// `internal/repo/kacho`. Используется unit-тестами use-case'ов Network (Wave 5
// pilot, KAC-94). Поддерживает базовую TX-семантику:
//   - Writer накапливает изменения во вспомогательный «uncommitted» state и
//     виден сам себе (G.2 — Get/List после Insert внутри одной writer'а
//     возвращают пишемые данные).
//   - На Commit — flush в общий state.
//   - На Abort (или просто без Commit) — uncommitted state выкидывается.
//   - Параллельный Reader НЕ видит uncommitted writes (read-committed).
//   - Outbox-emit транзакционен (если Abort — события писались бы только во
//     внутренний буфер, который выкидывается).
//
// Mock сознательно НЕ покрывает 100% pgxpool-семантики (нет MVCC, нет lock'ов,
// нет ON CONFLICT). Его задача — проверить, что use-case-код корректно
// открывает TX, делает Commit/Abort, и outbox-emit на правильных путях.
package kachomock

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// OutboxEvent — снимок outbox-row (для проверок в тестах: что было emit'ed и
// в каком порядке).
type OutboxEvent struct {
	Resource string
	ID       string
	Action   string
	Payload  map[string]any
}

// Repository — in-memory mock корневого CQRS-контракта. Потокобезопасный
// (sync.Mutex на общем state — нужен для concurrent integration-like тестов).
//
// Wave 5 replicate (KAC-94): добавлен routeTables — parity с networks/SGs.
// RouteTables() reader/writer работают как Networks()/SecurityGroups() —
// in-memory state с TX-семантикой (writer accumulate'ит в local map, Commit
// flush'ит в parent state). PrivateEndpoints — добавлен в той же replicate-фазе.
type Repository struct {
	mu                sync.Mutex
	networks          map[string]*kacho.NetworkRecord
	securityGroups    map[string]*kacho.SecurityGroupRecord
	routeTables       map[string]*kacho.RouteTableRecord
	privateEndpoints  map[string]*kacho.PrivateEndpointRecord
	networkInterfaces map[string]*kacho.NetworkInterfaceRecord // Wave 5 replicate (NIC batch, KAC-94).
	addresses         map[string]*kacho.AddressRecord          // Wave 5 replicate (Address batch, KAC-94).
	outbox            []OutboxEvent
}

// NewRepository создаёт пустой mock-Repository.
func NewRepository() *Repository {
	return &Repository{
		networks:          make(map[string]*kacho.NetworkRecord),
		securityGroups:    make(map[string]*kacho.SecurityGroupRecord),
		routeTables:       make(map[string]*kacho.RouteTableRecord),
		privateEndpoints:  make(map[string]*kacho.PrivateEndpointRecord),
		networkInterfaces: make(map[string]*kacho.NetworkInterfaceRecord),
		addresses:         make(map[string]*kacho.AddressRecord),
	}
}

// Outbox возвращает копию выпущенных outbox-event'ов (post-commit only).
func (r *Repository) Outbox() []OutboxEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OutboxEvent, len(r.outbox))
	copy(out, r.outbox)
	return out
}

// Networks возвращает копию state'а (для assertions в тестах).
func (r *Repository) Networks() []*kacho.NetworkRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.NetworkRecord, 0, len(r.networks))
	for _, n := range r.networks {
		res = append(res, n)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// SecurityGroups возвращает копию state'а (для assertions в тестах).
// Wave 5 batch 33/34 (KAC-94).
func (r *Repository) SecurityGroups() []*kacho.SecurityGroupRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.SecurityGroupRecord, 0, len(r.securityGroups))
	for _, sg := range r.securityGroups {
		res = append(res, sg)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// RouteTables возвращает копию state'а (для assertions в тестах).
// Wave 5 replicate (KAC-94).
func (r *Repository) RouteTables() []*kacho.RouteTableRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.RouteTableRecord, 0, len(r.routeTables))
	for _, rt := range r.routeTables {
		res = append(res, rt)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// PrivateEndpoints возвращает копию state'а (для assertions в тестах).
// Wave 5 replicate (KAC-94).
func (r *Repository) PrivateEndpoints() []*kacho.PrivateEndpointRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.PrivateEndpointRecord, 0, len(r.privateEndpoints))
	for _, pe := range r.privateEndpoints {
		res = append(res, pe)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// NetworkInterfaces возвращает копию state'а (для assertions в тестах).
// Wave 5 replicate (KAC-94, NIC batch).
func (r *Repository) NetworkInterfaces() []*kacho.NetworkInterfaceRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.NetworkInterfaceRecord, 0, len(r.networkInterfaces))
	for _, ni := range r.networkInterfaces {
		res = append(res, ni)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// Reader открывает read-only «TX». Snapshot текущего committed state'а
// заfreez'ен на момент открытия — параллельный Writer не виден этому Reader'у
// (read-committed semantics).
func (r *Repository) Reader(_ context.Context) (kacho.RepositoryReader, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	netSnap := make(map[string]*kacho.NetworkRecord, len(r.networks))
	for id, n := range r.networks {
		cp := *n
		netSnap[id] = &cp
	}
	sgSnap := make(map[string]*kacho.SecurityGroupRecord, len(r.securityGroups))
	for id, sg := range r.securityGroups {
		cp := *sg
		sgSnap[id] = &cp
	}
	rtSnap := make(map[string]*kacho.RouteTableRecord, len(r.routeTables))
	for id, rt := range r.routeTables {
		cp := *rt
		rtSnap[id] = &cp
	}
	peSnap := make(map[string]*kacho.PrivateEndpointRecord, len(r.privateEndpoints))
	for id, pe := range r.privateEndpoints {
		cp := *pe
		peSnap[id] = &cp
	}
	niSnap := make(map[string]*kacho.NetworkInterfaceRecord, len(r.networkInterfaces))
	for id, ni := range r.networkInterfaces {
		cp := *ni
		niSnap[id] = &cp
	}
	addrSnap := make(map[string]*kacho.AddressRecord, len(r.addresses))
	for id, a := range r.addresses {
		cp := *a
		addrSnap[id] = &cp
	}
	return &readerImpl{netSnap: netSnap, sgSnap: sgSnap, rtSnap: rtSnap, peSnap: peSnap, niSnap: niSnap, addrSnap: addrSnap}, nil
}

// Writer открывает RW-«TX». Изменения буферизуются в self.local и видны только
// этому writer'у; на Commit flush в общий state.
func (r *Repository) Writer(_ context.Context) (kacho.RepositoryWriter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Скопировать current state в writer'овский «working set» — writer видит
	// свои writes (G.2) поверх committed-snapshot'а.
	localNets := make(map[string]*kacho.NetworkRecord, len(r.networks))
	for id, n := range r.networks {
		cp := *n
		localNets[id] = &cp
	}
	localSGs := make(map[string]*kacho.SecurityGroupRecord, len(r.securityGroups))
	for id, sg := range r.securityGroups {
		cp := *sg
		localSGs[id] = &cp
	}
	localRTs := make(map[string]*kacho.RouteTableRecord, len(r.routeTables))
	for id, rt := range r.routeTables {
		cp := *rt
		localRTs[id] = &cp
	}
	localPEs := make(map[string]*kacho.PrivateEndpointRecord, len(r.privateEndpoints))
	for id, pe := range r.privateEndpoints {
		cp := *pe
		localPEs[id] = &cp
	}
	localNIs := make(map[string]*kacho.NetworkInterfaceRecord, len(r.networkInterfaces))
	for id, ni := range r.networkInterfaces {
		cp := *ni
		localNIs[id] = &cp
	}
	localAddrs := make(map[string]*kacho.AddressRecord, len(r.addresses))
	for id, a := range r.addresses {
		cp := *a
		localAddrs[id] = &cp
	}
	return &writerImpl{
		parent:     r,
		local:      localNets,
		localSGs:   localSGs,
		localRTs:   localRTs,
		localPEs:   localPEs,
		localNIs:   localNIs,
		localAddrs: localAddrs,
	}, nil
}

// Close — no-op.
func (r *Repository) Close() {}

// readerImpl — read-only snapshot. Закрытие — no-op (Mock не держит ресурс).
type readerImpl struct {
	netSnap  map[string]*kacho.NetworkRecord
	sgSnap   map[string]*kacho.SecurityGroupRecord
	rtSnap   map[string]*kacho.RouteTableRecord
	peSnap   map[string]*kacho.PrivateEndpointRecord
	niSnap   map[string]*kacho.NetworkInterfaceRecord // Wave 5 replicate (NIC batch, KAC-94).
	addrSnap map[string]*kacho.AddressRecord          // Wave 5 replicate (Address batch, KAC-94).
}

func (rd *readerImpl) Networks() kacho.NetworkReaderIface {
	return &networkReader{snap: rd.netSnap}
}

// SecurityGroups — read-only snapshot SG. Wave 5 batch 33/34 (KAC-94).
func (rd *readerImpl) SecurityGroups() kacho.SecurityGroupReaderIface {
	return &securityGroupReader{snap: rd.sgSnap}
}

// RouteTables — read-only snapshot RT. Wave 5 replicate (KAC-94).
func (rd *readerImpl) RouteTables() kacho.RouteTableReaderIface {
	return &routeTableReader{snap: rd.rtSnap}
}

// PrivateEndpoints — read-only snapshot PE. Wave 5 replicate (KAC-94).
func (rd *readerImpl) PrivateEndpoints() kacho.PrivateEndpointReaderIface {
	return &privateEndpointReader{snap: rd.peSnap}
}

// NetworkInterfaces — read-only snapshot NIC. Wave 5 replicate (KAC-94, NIC batch).
func (rd *readerImpl) NetworkInterfaces() kacho.NetworkInterfaceReaderIface {
	return &networkInterfaceReader{snap: rd.niSnap}
}

// Addresses — read-only snapshot Address. Wave 5 replicate (KAC-94, Address batch).
func (rd *readerImpl) Addresses() kacho.AddressReaderIface {
	return &addressReader{snap: rd.addrSnap}
}

func (rd *readerImpl) Close() error { return nil }

// writerImpl — write-«TX». local — working set, окончательно мерж'ится в
// parent.networks на Commit. local-outbox — буфер outbox-event'ов, на Commit
// добавляется в parent.outbox.
type writerImpl struct {
	parent         *Repository
	local          map[string]*kacho.NetworkRecord
	localSGs       map[string]*kacho.SecurityGroupRecord
	localPEs       map[string]*kacho.PrivateEndpointRecord
	localRTs       map[string]*kacho.RouteTableRecord
	localNIs       map[string]*kacho.NetworkInterfaceRecord // Wave 5 replicate (NIC batch, KAC-94).
	localAddrs     map[string]*kacho.AddressRecord          // Wave 5 replicate (Address batch, KAC-94).
	localOutbox    []OutboxEvent
	deletedIDs     map[string]struct{} // Network deletions
	deletedSGIDs   map[string]struct{} // SG deletions
	deletedPEIDs   map[string]struct{} // PE deletions
	deletedRTIDs   map[string]struct{} // RouteTable deletions
	deletedNIIDs   map[string]struct{} // NIC deletions (Wave 5 replicate, NIC batch).
	deletedAddrIDs map[string]struct{} // Address deletions (Wave 5 replicate, Address batch).
	finalised      bool
}

func (w *writerImpl) Networks() kacho.NetworkWriterIface {
	return &networkWriter{w: w}
}

// SecurityGroups возвращает SG-writer привязанный к этой «TX». Wave 5 batch 33/34 (KAC-94).
func (w *writerImpl) SecurityGroups() kacho.SecurityGroupWriterIface {
	return &securityGroupWriter{w: w}
}

// PrivateEndpoints возвращает PE-writer привязанный к этой «TX». Wave 5 replicate (KAC-94).
func (w *writerImpl) PrivateEndpoints() kacho.PrivateEndpointWriterIface {
	return &privateEndpointWriter{w: w}
}

// RouteTables возвращает RT-writer, привязанный к этой «TX». Wave 5 replicate (KAC-94).
func (w *writerImpl) RouteTables() kacho.RouteTableWriterIface {
	return &routeTableWriter{w: w}
}

// NetworkInterfaces возвращает NIC-writer, привязанный к этой «TX».
// Wave 5 replicate (KAC-94, NIC batch). Includes atomic AttachToInstance CAS
// (mirrors KAC-52 repo-side guard — see iface_network_interface.go) +
// idempotent DetachFromInstance + Insert.
func (w *writerImpl) NetworkInterfaces() kacho.NetworkInterfaceWriterIface {
	return &networkInterfaceWriter{w: w}
}

// Addresses возвращает Address-writer, привязанный к этой «TX». Wave 5
// replicate (KAC-94, Address batch).
func (w *writerImpl) Addresses() kacho.AddressWriterIface {
	return &addressWriter{w: w}
}

func (w *writerImpl) Outbox() kacho.OutboxEmitter {
	return &outboxEmitter{w: w}
}

func (w *writerImpl) Commit() error {
	if w.finalised {
		return nil
	}
	w.finalised = true
	w.parent.mu.Lock()
	defer w.parent.mu.Unlock()
	// Удалить помеченные на delete (Network).
	for id := range w.deletedIDs {
		delete(w.parent.networks, id)
	}
	// Применить writes (Network).
	for id, n := range w.local {
		// Если id был помечен на удаление и сразу re-added в этом writer'е — упустим
		// этот edge-case (не используется в pilot'ных тестах).
		w.parent.networks[id] = n
	}
	// Удалить помеченные на delete (SG).
	for id := range w.deletedSGIDs {
		delete(w.parent.securityGroups, id)
	}
	// Применить writes (SG).
	for id, sg := range w.localSGs {
		w.parent.securityGroups[id] = sg
	}
	// Удалить помеченные на delete (PE).
	for id := range w.deletedPEIDs {
		delete(w.parent.privateEndpoints, id)
	}
	// Применить writes (PE).
	for id, pe := range w.localPEs {
		w.parent.privateEndpoints[id] = pe
	}
	// Удалить помеченные на delete (RT). Wave 5 replicate (KAC-94).
	for id := range w.deletedRTIDs {
		delete(w.parent.routeTables, id)
	}
	// Применить writes (RT).
	for id, rt := range w.localRTs {
		w.parent.routeTables[id] = rt
	}
	// Удалить помеченные на delete (NIC). Wave 5 replicate (KAC-94, NIC batch).
	for id := range w.deletedNIIDs {
		delete(w.parent.networkInterfaces, id)
	}
	// Применить writes (NIC).
	for id, ni := range w.localNIs {
		w.parent.networkInterfaces[id] = ni
	}
	// Удалить помеченные на delete (Address). Wave 5 replicate (KAC-94, Address batch).
	for id := range w.deletedAddrIDs {
		delete(w.parent.addresses, id)
	}
	// Применить writes (Address).
	for id, a := range w.localAddrs {
		w.parent.addresses[id] = a
	}
	// Перенести outbox-events в общий state.
	w.parent.outbox = append(w.parent.outbox, w.localOutbox...)
	return nil
}

func (w *writerImpl) Abort() {
	if w.finalised {
		return
	}
	w.finalised = true
	// Discard local + localSGs + localOutbox.
}

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
		if (f.FolderID == "" || n.FolderID == f.FolderID) &&
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
		if (f.FolderID == "" || n.FolderID == f.FolderID) &&
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

func (nw *networkWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.NetworkRecord, error) {
	if _, deleted := nw.w.deletedIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.local[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	n.FolderID = folderID
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

// ---- SecurityGroup reader / writer ----

// securityGroupReader — read-only snapshot SG. Wave 5 batch 33/34 (KAC-94).
type securityGroupReader struct {
	snap map[string]*kacho.SecurityGroupRecord
}

func (r *securityGroupReader) Get(_ context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	sg, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

func (r *securityGroupReader) List(_ context.Context, f kacho.SecurityGroupFilter, _ kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	var result []*kacho.SecurityGroupRecord
	for _, sg := range r.snap {
		if (f.FolderID == "" || sg.FolderID == f.FolderID) &&
			(f.NetworkID == "" || sg.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(sg.Name) == f.Name) {
			cp := *sg
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// securityGroupWriter — write-«TX» SG. Wave 5 batch 33/34 (KAC-94). Writer
// видит свои writes (G.2) — Get/List поверх localSGs.
type securityGroupWriter struct {
	w *writerImpl
}

func (sw *securityGroupWriter) Get(_ context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

func (sw *securityGroupWriter) List(_ context.Context, f kacho.SecurityGroupFilter, _ kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	var result []*kacho.SecurityGroupRecord
	for id, sg := range sw.w.localSGs {
		if _, deleted := sw.w.deletedSGIDs[id]; deleted {
			continue
		}
		if (f.FolderID == "" || sg.FolderID == f.FolderID) &&
			(f.NetworkID == "" || sg.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(sg.Name) == f.Name) {
			cp := *sg
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (sw *securityGroupWriter) Insert(_ context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	rec := &kacho.SecurityGroupRecord{SecurityGroup: *sg, CreatedAt: time.Now().UTC()}
	sw.w.localSGs[sg.ID] = rec
	cp := *rec
	return &cp, nil
}

func (sw *securityGroupWriter) Update(_ context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[sg.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := sw.w.localSGs[sg.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.SecurityGroup = *sg
	cp := *existing
	return &cp, nil
}

func (sw *securityGroupWriter) Delete(_ context.Context, id string) error {
	if _, ok := sw.w.localSGs[id]; !ok {
		return repo.ErrNotFound
	}
	if sw.w.deletedSGIDs == nil {
		sw.w.deletedSGIDs = make(map[string]struct{})
	}
	sw.w.deletedSGIDs[id] = struct{}{}
	delete(sw.w.localSGs, id)
	return nil
}

func (sw *securityGroupWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	sg.FolderID = folderID
	cp := *sg
	return &cp, nil
}

// UpdateRules / UpdateRule — упрощённая семантика (без xmin-OCC; mock не
// моделирует concurrent-conflict). Достаточно для unit-тестов use-case'ов.
func (sw *securityGroupWriter) UpdateRules(_ context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[sgID]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[sgID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if len(deleteIDs) > 0 {
		toDel := make(map[string]struct{}, len(deleteIDs))
		for _, id := range deleteIDs {
			toDel[id] = struct{}{}
		}
		filtered := sg.Rules[:0]
		for _, r := range sg.Rules {
			if _, drop := toDel[r.ID]; drop {
				continue
			}
			filtered = append(filtered, r)
		}
		sg.Rules = filtered
	}
	sg.Rules = append(sg.Rules, add...)
	cp := *sg
	return &cp, nil
}

func (sw *securityGroupWriter) UpdateRule(_ context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[sgID]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[sgID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	applyMask := len(mask) > 0
	maskSet := map[string]struct{}{}
	for _, m := range mask {
		maskSet[m] = struct{}{}
	}
	found := false
	for i := range sg.Rules {
		if sg.Rules[i].ID != ruleID {
			continue
		}
		found = true
		if !applyMask {
			sg.Rules[i].Description = domain.RcDescription(description)
			sg.Rules[i].Labels = labels
		} else {
			if _, ok := maskSet["description"]; ok {
				sg.Rules[i].Description = domain.RcDescription(description)
			}
			if _, ok := maskSet["labels"]; ok {
				sg.Rules[i].Labels = labels
			}
		}
		break
	}
	if !found {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

// ---- PrivateEndpoint reader / writer ----

// privateEndpointReader — read-only snapshot PE. Wave 5 replicate (KAC-94).
type privateEndpointReader struct {
	snap map[string]*kacho.PrivateEndpointRecord
}

func (r *privateEndpointReader) Get(_ context.Context, id string) (*kacho.PrivateEndpointRecord, error) {
	pe, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *pe
	return &cp, nil
}

func (r *privateEndpointReader) List(_ context.Context, f kacho.PrivateEndpointFilter, _ kacho.Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
	var result []*kacho.PrivateEndpointRecord
	for _, pe := range r.snap {
		if (f.FolderID == "" || pe.FolderID == f.FolderID) &&
			(f.Name == "" || string(pe.Name) == f.Name) {
			cp := *pe
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// privateEndpointWriter — write-«TX» PE. Wave 5 replicate (KAC-94). Writer
// видит свои writes (G.2) — Get/List поверх localPEs.
type privateEndpointWriter struct {
	w *writerImpl
}

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

func (pw *privateEndpointWriter) List(_ context.Context, f kacho.PrivateEndpointFilter, _ kacho.Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
	var result []*kacho.PrivateEndpointRecord
	for id, pe := range pw.w.localPEs {
		if _, deleted := pw.w.deletedPEIDs[id]; deleted {
			continue
		}
		if (f.FolderID == "" || pe.FolderID == f.FolderID) &&
			(f.Name == "" || string(pe.Name) == f.Name) {
			cp := *pe
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (pw *privateEndpointWriter) Insert(_ context.Context, pe *domain.PrivateEndpoint) (*kacho.PrivateEndpointRecord, error) {
	rec := &kacho.PrivateEndpointRecord{PrivateEndpoint: *pe, CreatedAt: time.Now().UTC()}
	pw.w.localPEs[pe.ID] = rec
	cp := *rec
	return &cp, nil
}

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

func (pw *privateEndpointWriter) SetFolderID(_ context.Context, id, folderID string) (*kacho.PrivateEndpointRecord, error) {
	if _, deleted := pw.w.deletedPEIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	pe, ok := pw.w.localPEs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	pe.FolderID = folderID
	cp := *pe
	return &cp, nil
}

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

// ---- NetworkInterface reader / writer ----
// Wave 5 replicate (KAC-94, NIC batch). Mirrors SG/PE/RT mock-pattern.

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

// networkInterfaceWriter — write-«TX» NIC. Wave 5 replicate (KAC-94, NIC batch).
// Writer видит свои writes (G.2) — Get/List поверх localNIs. CAS-семантика
// AttachToInstance — упрощённая (одна row, lock-free); pg-impl на боевой БД
// использует single-statement conditional UPDATE c row-level lock (KAC-52, миграция
// 0016). Здесь — достаточно для unit-тестов use-case'ов.
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

// ---- Outbox emitter ----

type outboxEmitter struct {
	w *writerImpl
}

func (e *outboxEmitter) Emit(_ context.Context, resource, id, action string, payload map[string]any) error {
	// Скопируем payload (caller может его мутировать после Emit).
	cp := make(map[string]any, len(payload))
	for k, v := range payload {
		cp[k] = v
	}
	e.w.localOutbox = append(e.w.localOutbox, OutboxEvent{
		Resource: resource, ID: id, Action: action, Payload: cp,
	})
	return nil
}

// Assertion: Repository удовлетворяет интерфейсу kacho.Repository.
var _ kacho.Repository = (*Repository)(nil)
