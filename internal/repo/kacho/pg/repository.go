// Package pg — pgxpool-implementation CQRS-Repository (skill evgeniy §6 G.1-G.7).
//
// Wave 5 pilot (KAC-94): только Network. Структура спроектирована под replicate
// на 7 оставшихся ресурсов (Subnet/Address/RouteTable/SecurityGroup/Gateway/
// PrivateEndpoint/NetworkInterface) — добавление нового resource'а сводится к:
//  1. Файл `<resource>.go` с *resourceReader{tx pgx.Tx} + *resourceWriter{tx, emitter}.
//  2. Метод `Networks()` / `Subnets()` / ... в readerImpl и writerImpl ниже.
//  3. Расширение интерфейсов RepositoryReader / RepositoryWriter в
//     `internal/repo/kacho/iface.go` + новый iface_<resource>.go.
package pg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Repository — pgxpool-impl корневого CQRS-контракта.
type Repository struct {
	pool *pgxpool.Pool
}

// New собирает Repository поверх существующего pgxpool.Pool (pool создаётся в
// composition root, обычно из `kacho-corelib/db.NewPool`).
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Reader открывает read-only TX (read-committed). Возвращённый reader обязан
// быть закрыт через Close() — это rollback'ит TX и возвращает соединение в пул.
//
// Сейчас TX идёт на тот же мастер; когда появится slave-реплика — здесь нужно
// будет роутить на неё (skill evgeniy §6 G.4).
func (r *Repository) Reader(ctx context.Context) (kacho.RepositoryReader, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	return &readerImpl{tx: tx}, nil
}

// Writer открывает RW TX на мастере. Caller обязан вызвать Commit() либо Abort()
// (Abort идемпотентен — безопасно через defer сразу после открытия).
func (r *Repository) Writer(ctx context.Context) (kacho.RepositoryWriter, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	return &writerImpl{tx: tx}, nil
}

// Close — no-op (pool управляется composition root, не репозиторием). Метод
// есть в Repository-интерфейсе чтобы тестовый код мог .Close() мокать без
// reach'а в pool.
func (r *Repository) Close() {}

// readerImpl — read-only TX state.
type readerImpl struct {
	tx     pgx.Tx
	closed bool
}

// Networks возвращает Network-reader, привязанный к этой read-TX.
func (r *readerImpl) Networks() kacho.NetworkReaderIface {
	return &networkReader{tx: r.tx}
}

// Close rollback'ит read-TX (read-only TX — rollback не имеет side-effects).
// Идемпотентно. Игнорирует pgx.ErrTxClosed.
func (r *readerImpl) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if err := r.tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return err
	}
	return nil
}

// writerImpl — RW TX state.
type writerImpl struct {
	tx        pgx.Tx
	finalised bool // true после Commit() или Abort() — защита от double-finalize
}

// Networks возвращает Network-writer, привязанный к этой write-TX.
// Включает в себя reader-методы (G.2 — writer видит свои writes).
func (w *writerImpl) Networks() kacho.NetworkWriterIface {
	return &networkWriter{
		networkReader: networkReader{tx: w.tx},
		emitter:       &outboxEmitter{tx: w.tx},
	}
}

// Outbox возвращает emitter, привязанный к этой write-TX — DML + outbox-emit
// атомарны (skill evgeniy §6 G.5: атомарность гарантируется тем, что обе
// операции идут через одну pgx.Tx).
func (w *writerImpl) Outbox() kacho.OutboxEmitter {
	return &outboxEmitter{tx: w.tx}
}

// Commit финализирует write-TX. После Commit вызов Abort — no-op.
func (w *writerImpl) Commit() error {
	if w.finalised {
		return nil
	}
	w.finalised = true
	return w.tx.Commit(context.Background())
}

// Abort откатывает TX. Идемпотентен — после Commit no-op, можно ставить через
// defer сразу после открытия writer'а:
//
//	w, err := repo.Writer(ctx)
//	if err != nil { return ... }
//	defer w.Abort()
//	...
//	if err := w.Commit(); err != nil { return ... }
func (w *writerImpl) Abort() {
	if w.finalised {
		return
	}
	w.finalised = true
	_ = w.tx.Rollback(context.Background())
}

// outboxEmitter — emit в `vpc_outbox` через текущую TX writer'а. Делегирует
// repo.EmitVPC (который оборачивает kacho-corelib/outbox.Emit с фиксированной
// таблицей "vpc_outbox" + trigger pg_notify('vpc_outbox', ...)).
type outboxEmitter struct {
	tx pgx.Tx
}

// Emit добавляет outbox-row в той же tx, что и DML resource'а.
// payload nil → пустой JSON-объект (как у legacy emitVPC).
func (e *outboxEmitter) Emit(ctx context.Context, resource, id, action string, payload map[string]any) error {
	return repo.EmitVPC(ctx, e.tx, resource, id, action, payload)
}
