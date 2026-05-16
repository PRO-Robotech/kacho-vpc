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
type Repository struct {
	mu             sync.Mutex
	networks       map[string]*kacho.NetworkRecord
	securityGroups map[string]*domain.SecurityGroupRecord
	outbox         []OutboxEvent
}

// NewRepository создаёт пустой mock-Repository.
func NewRepository() *Repository {
	return &Repository{
		networks:       make(map[string]*kacho.NetworkRecord),
		securityGroups: make(map[string]*domain.SecurityGroupRecord),
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
func (r *Repository) SecurityGroups() []*domain.SecurityGroupRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*domain.SecurityGroupRecord, 0, len(r.securityGroups))
	for _, sg := range r.securityGroups {
		res = append(res, sg)
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
	sgSnap := make(map[string]*domain.SecurityGroupRecord, len(r.securityGroups))
	for id, sg := range r.securityGroups {
		cp := *sg
		sgSnap[id] = &cp
	}
	return &readerImpl{netSnap: netSnap, sgSnap: sgSnap}, nil
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
	localSGs := make(map[string]*domain.SecurityGroupRecord, len(r.securityGroups))
	for id, sg := range r.securityGroups {
		cp := *sg
		localSGs[id] = &cp
	}
	return &writerImpl{
		parent:   r,
		local:    localNets,
		localSGs: localSGs,
	}, nil
}

// Close — no-op.
func (r *Repository) Close() {}

// readerImpl — read-only snapshot. Закрытие — no-op (Mock не держит ресурс).
type readerImpl struct {
	netSnap map[string]*kacho.NetworkRecord
	sgSnap  map[string]*domain.SecurityGroupRecord
}

func (rd *readerImpl) Networks() kacho.NetworkReaderIface {
	return &networkReader{snap: rd.netSnap}
}

// SecurityGroups — read-only snapshot SG. Wave 5 batch 33/34 (KAC-94).
func (rd *readerImpl) SecurityGroups() kacho.SecurityGroupReaderIface {
	return &securityGroupReader{snap: rd.sgSnap}
}

func (rd *readerImpl) Close() error { return nil }

// writerImpl — write-«TX». local — working set, окончательно мерж'ится в
// parent.networks на Commit. local-outbox — буфер outbox-event'ов, на Commit
// добавляется в parent.outbox.
type writerImpl struct {
	parent       *Repository
	local        map[string]*kacho.NetworkRecord
	localSGs     map[string]*domain.SecurityGroupRecord
	localOutbox  []OutboxEvent
	deletedIDs   map[string]struct{} // Network deletions
	deletedSGIDs map[string]struct{} // SG deletions
	finalised    bool
}

func (w *writerImpl) Networks() kacho.NetworkWriterIface {
	return &networkWriter{w: w}
}

// SecurityGroups возвращает SG-writer привязанный к этой «TX». Wave 5 batch 33/34 (KAC-94).
func (w *writerImpl) SecurityGroups() kacho.SecurityGroupWriterIface {
	return &securityGroupWriter{w: w}
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
	snap map[string]*domain.SecurityGroupRecord
}

func (r *securityGroupReader) Get(_ context.Context, id string) (*domain.SecurityGroupRecord, error) {
	sg, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

func (r *securityGroupReader) List(_ context.Context, f kacho.SecurityGroupFilter, _ kacho.Pagination) ([]*domain.SecurityGroupRecord, string, error) {
	var result []*domain.SecurityGroupRecord
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

func (sw *securityGroupWriter) Get(_ context.Context, id string) (*domain.SecurityGroupRecord, error) {
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

func (sw *securityGroupWriter) List(_ context.Context, f kacho.SecurityGroupFilter, _ kacho.Pagination) ([]*domain.SecurityGroupRecord, string, error) {
	var result []*domain.SecurityGroupRecord
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

func (sw *securityGroupWriter) Insert(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error) {
	rec := &domain.SecurityGroupRecord{SecurityGroup: *sg, CreatedAt: time.Now().UTC()}
	sw.w.localSGs[sg.ID] = rec
	cp := *rec
	return &cp, nil
}

func (sw *securityGroupWriter) Update(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error) {
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

func (sw *securityGroupWriter) SetFolderID(_ context.Context, id, folderID string) (*domain.SecurityGroupRecord, error) {
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
func (sw *securityGroupWriter) UpdateRules(_ context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*domain.SecurityGroupRecord, error) {
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

func (sw *securityGroupWriter) UpdateRule(_ context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*domain.SecurityGroupRecord, error) {
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
