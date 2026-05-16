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
	mu       sync.Mutex
	networks map[string]*domain.NetworkRecord
	outbox   []OutboxEvent
}

// NewRepository создаёт пустой mock-Repository.
func NewRepository() *Repository {
	return &Repository{
		networks: make(map[string]*domain.NetworkRecord),
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
func (r *Repository) Networks() []*domain.NetworkRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*domain.NetworkRecord, 0, len(r.networks))
	for _, n := range r.networks {
		res = append(res, n)
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
	snap := make(map[string]*domain.NetworkRecord, len(r.networks))
	for id, n := range r.networks {
		cp := *n
		snap[id] = &cp
	}
	return &readerImpl{snap: snap}, nil
}

// Writer открывает RW-«TX». Изменения буферизуются в self.local и видны только
// этому writer'у; на Commit flush в общий state.
func (r *Repository) Writer(_ context.Context) (kacho.RepositoryWriter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Скопировать current state в writer'овский «working set» — writer видит
	// свои writes (G.2) поверх committed-snapshot'а.
	local := make(map[string]*domain.NetworkRecord, len(r.networks))
	for id, n := range r.networks {
		cp := *n
		local[id] = &cp
	}
	return &writerImpl{
		parent: r,
		local:  local,
	}, nil
}

// Close — no-op.
func (r *Repository) Close() {}

// readerImpl — read-only snapshot. Закрытие — no-op (Mock не держит ресурс).
type readerImpl struct {
	snap map[string]*domain.NetworkRecord
}

func (rd *readerImpl) Networks() kacho.NetworkReaderIface {
	return &networkReader{snap: rd.snap}
}

func (rd *readerImpl) Close() error { return nil }

// writerImpl — write-«TX». local — working set, окончательно мерж'ится в
// parent.networks на Commit. local-outbox — буфер outbox-event'ов, на Commit
// добавляется в parent.outbox.
type writerImpl struct {
	parent      *Repository
	local       map[string]*domain.NetworkRecord
	localOutbox []OutboxEvent
	deletedIDs  map[string]struct{}
	finalised   bool
}

func (w *writerImpl) Networks() kacho.NetworkWriterIface {
	return &networkWriter{w: w}
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
	// Удалить помеченные на delete (если в local их нет).
	for id := range w.deletedIDs {
		delete(w.parent.networks, id)
	}
	// Применить writes.
	for id, n := range w.local {
		// Если id был помечен на удаление и сразу re-added в этом writer'е — упустим
		// этот edge-case (не используется в pilot'ных тестах).
		w.parent.networks[id] = n
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
	// Discard local + localOutbox.
}

// ---- Network reader ----

type networkReader struct {
	snap map[string]*domain.NetworkRecord
}

func (r *networkReader) Get(_ context.Context, id string) (*domain.NetworkRecord, error) {
	n, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (r *networkReader) List(_ context.Context, f kacho.NetworkFilter, _ kacho.Pagination) ([]*domain.NetworkRecord, string, error) {
	var result []*domain.NetworkRecord
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
func (nw *networkWriter) Get(_ context.Context, id string) (*domain.NetworkRecord, error) {
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

func (nw *networkWriter) List(_ context.Context, f kacho.NetworkFilter, _ kacho.Pagination) ([]*domain.NetworkRecord, string, error) {
	var result []*domain.NetworkRecord
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

func (nw *networkWriter) Insert(_ context.Context, n *domain.Network) (*domain.NetworkRecord, error) {
	rec := &domain.NetworkRecord{Network: *n, CreatedAt: time.Now().UTC()}
	nw.w.local[n.ID] = rec
	cp := *rec
	return &cp, nil
}

func (nw *networkWriter) Update(_ context.Context, n *domain.Network) (*domain.NetworkRecord, error) {
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

func (nw *networkWriter) SetFolderID(_ context.Context, id, folderID string) (*domain.NetworkRecord, error) {
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
