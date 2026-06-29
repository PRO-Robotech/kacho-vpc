// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package pg — pgxpool-реализация CQRS-Repository (Reader на реплику, Writer на master).
//
// Структура единообразна по всем ресурсам (Network/Subnet/Address/RouteTable/
// SecurityGroup/Gateway/NetworkInterface); добавление нового ресурса сводится к:
//  1. Файл `<resource>.go` с *resourceReader{tx pgx.Tx} + *resourceWriter{tx pgx.Tx}.
//  2. Метод `Networks()` / `Subnets()` / ... в readerImpl и writerImpl ниже.
//  3. Расширение интерфейсов RepositoryReader / RepositoryWriter в
//     `internal/repo/kacho/iface.go` + новый iface_<resource>.go.
package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Repository — pgxpool-реализация корневого CQRS-контракта.
//
// Reader идет на slave-реплику, Writer — на master. Два физических pgxpool.Pool —
// обычно одна и та же логическая БД, но slavePool читает из streaming-replica
// (hot_standby=on). Failover выполняется снаружи (PGBouncer / Patroni и т.п.) —
// Repository только маршрутизирует.
//
// Если slavePool не настроен (nil или передан тот же master) — Reader
// открывает read-only TX на master-pool. Это структурный задел: код во всех
// use-case'ах уже разделен на Reader/Writer, и переключение на реальную
// реплику — это только wiring-изменение в `cmd/vpc/main.go` (`slavePool != nil`).
type Repository struct {
	master *pgxpool.Pool
	slave  *pgxpool.Pool
}

// New собирает Repository поверх master- и опц. slave-pool'ов.
//
//   - masterPool — RW pgxpool на primary; используется Writer + Reader-fallback.
//   - slavePool  — RO pgxpool на streaming-replica; если nil → Reader идет на
//     master (fallback, текущее dev/prod-поведение). Когда реальная реплика
//     появляется — composition root передает второй pool, и Reader-TX уходят
//     на нее без изменений в use-case-слое.
//
// Pools создаются в composition root (обычно из `kacho-corelib/db.NewPool`).
func New(masterPool, slavePool *pgxpool.Pool) *Repository {
	if slavePool == nil {
		slavePool = masterPool
	}
	return &Repository{master: masterPool, slave: slavePool}
}

// Reader открывает read-only TX (read-committed) на **slave-pool'е**, если он
// настроен; иначе на master (fallback). Возвращенный reader обязан быть закрыт
// через Close() — это rollback'ит TX и возвращает соединение в пул.
//
// Разгружает master от read-нагрузки. На реплике read-committed TX гарантированно
// read-only (streaming replica не принимает writes); на master-fallback ту же
// гарантию на уровне Postgres дает `pgx.TxOptions{AccessMode: pgx.ReadOnly}`.
func (r *Repository) Reader(ctx context.Context) (kacho.RepositoryReader, error) {
	tx, err := r.slave.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	return &readerImpl{tx: tx}, nil
}

// Writer открывает RW TX на **master-pool'е**. Caller обязан вызвать Commit()
// либо Abort() (Abort идемпотентен — безопасно через defer сразу после открытия).
//
// Writes всегда идут на primary; репликация на slave — асинхронная Postgres
// streaming replication, прозрачно для use-case'а.
func (r *Repository) Writer(ctx context.Context) (kacho.RepositoryWriter, error) {
	tx, err := r.master.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	return &writerImpl{tx: tx}, nil
}

// Close — no-op (pool'ы управляются composition root, не репозиторием). Метод
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

// SecurityGroups возвращает SecurityGroup-reader, привязанный к этой read-TX.
func (r *readerImpl) SecurityGroups() kacho.SecurityGroupReaderIface {
	return &securityGroupReader{tx: r.tx}
}

// Addresses возвращает Address-reader, привязанный к этой read-TX.
func (r *readerImpl) Addresses() kacho.AddressReaderIface {
	return &addressReader{tx: r.tx}
}

// RouteTables возвращает RouteTable-reader, привязанный к этой read-TX.
func (r *readerImpl) RouteTables() kacho.RouteTableReaderIface {
	return &routeTableReader{tx: r.tx}
}

// NetworkInterfaces возвращает NIC-reader, привязанный к этой read-TX.
// См. doc-комментарий на iface_network_interface.go.
func (r *readerImpl) NetworkInterfaces() kacho.NetworkInterfaceReaderIface {
	return &networkInterfaceReader{tx: r.tx}
}

// Subnets возвращает Subnet-reader, привязанный к этой read-TX.
func (r *readerImpl) Subnets() kacho.SubnetReaderIface {
	return &subnetReader{tx: r.tx}
}

// Gateways возвращает Gateway-reader, привязанный к этой read-TX.
func (r *readerImpl) Gateways() kacho.GatewayReaderIface {
	return &gatewayReader{tx: r.tx}
}

// AddressPools возвращает AddressPool-reader, привязанный к этой read-TX.
func (r *readerImpl) AddressPools() kacho.AddressPoolReaderIface {
	return &addressPoolReader{tx: r.tx}
}

// AddressPoolBindings возвращает AddressPoolBinding-reader, привязанный к этой read-TX.
func (r *readerImpl) AddressPoolBindings() kacho.AddressPoolBindingReaderIface {
	return &addressPoolBindingReader{tx: r.tx}
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
// Включает в себя reader-методы — writer видит свои writes.
func (w *writerImpl) Networks() kacho.NetworkWriterIface {
	return &networkWriter{
		networkReader: networkReader{tx: w.tx},
	}
}

// SecurityGroups возвращает SecurityGroup-writer, привязанный к этой write-TX.
// Writer видит свои writes (reader-методы — поверх той же pgx.Tx).
func (w *writerImpl) SecurityGroups() kacho.SecurityGroupWriterIface {
	return &securityGroupWriter{
		securityGroupReader: securityGroupReader{tx: w.tx},
	}
}

// Addresses возвращает Address-writer, привязанный к этой write-TX. Writer видит
// свои writes (reader-методы — поверх той же pgx.Tx). IPAM allocate-flow атомарен:
// Insert(addr) + AllocateIPFromFreelist/AllocateExternalIPv6 + outbox-emit идут в
// одной writer-TX — либо все вместе видны, либо ни один (Abort/crash).
func (w *writerImpl) Addresses() kacho.AddressWriterIface {
	return &addressWriter{
		addressReader: addressReader{tx: w.tx},
	}
}

// RouteTables возвращает RouteTable-writer, привязанный к этой write-TX.
// Writer видит свои writes (reader-методы — поверх той же pgx.Tx).
func (w *writerImpl) RouteTables() kacho.RouteTableWriterIface {
	return &routeTableWriter{
		routeTableReader: routeTableReader{tx: w.tx},
	}
}

// NetworkInterfaces возвращает NIC-writer, привязанный к этой write-TX.
// Writer видит свои writes. Insert может вернуть MAC-collision sentinel
// (caller retry'ит с новым MAC).
func (w *writerImpl) NetworkInterfaces() kacho.NetworkInterfaceWriterIface {
	return &networkInterfaceWriter{
		networkInterfaceReader: networkInterfaceReader{tx: w.tx},
	}
}

// Subnets возвращает Subnet-writer, привязанный к этой write-TX.
// Writer видит свои writes (reader-методы — поверх той же pgx.Tx).
func (w *writerImpl) Subnets() kacho.SubnetWriterIface {
	return &subnetWriter{
		subnetReader: subnetReader{tx: w.tx},
	}
}

// Gateways возвращает Gateway-writer, привязанный к этой write-TX.
// Writer видит свои writes (reader-методы — поверх той же pgx.Tx).
func (w *writerImpl) Gateways() kacho.GatewayWriterIface {
	return &gatewayWriter{
		gatewayReader: gatewayReader{tx: w.tx},
	}
}

// AddressPools возвращает AddressPool-writer, привязанный к этой write-TX.
// Writer видит свои writes. AddressPool — admin-only; Create +
// PopulateFreelistForPool + outbox-emit идут атомарно в одной writer-TX.
func (w *writerImpl) AddressPools() kacho.AddressPoolWriterIface {
	return &addressPoolWriter{
		addressPoolReader: addressPoolReader{tx: w.tx},
	}
}

// AddressPoolBindings возвращает AddressPoolBinding-writer, привязанный к этой write-TX.
func (w *writerImpl) AddressPoolBindings() kacho.AddressPoolBindingWriterIface {
	return &addressPoolBindingWriter{
		addressPoolBindingReader: addressPoolBindingReader{tx: w.tx},
	}
}

// Outbox возвращает emitter, привязанный к этой write-TX — DML + outbox-emit
// атомарны: обе операции идут через одну pgx.Tx.
func (w *writerImpl) Outbox() kacho.OutboxEmitter {
	return &outboxEmitter{tx: w.tx}
}

// FGARegister возвращает emitter FGA-register-intent'ов, привязанный к этой
// write-TX. DML ресурса + register-intent commit'ятся атомарно в одной pgx.Tx
// writer'а — без dual-write.
func (w *writerImpl) FGARegister() kacho.FGARegisterEmitter {
	return &fgaRegisterEmitter{tx: w.tx}
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
// helpers.EmitVPC (который оборачивает kacho-corelib/outbox.Emit с фиксированной
// таблицей "vpc_outbox" + trigger pg_notify('vpc_outbox', ...)).
type outboxEmitter struct {
	tx pgx.Tx
}

// Emit добавляет outbox-row в той же tx, что и DML resource'а.
// payload nil → пустой JSON-объект (поведение helpers.EmitVPC).
func (e *outboxEmitter) Emit(ctx context.Context, resource, id, action string, payload map[string]any) error {
	return helpers.EmitVPC(ctx, e.tx, resource, id, action, payload)
}

// fgaRegisterEmitter — emit FGA-register-intent в `kacho_vpc.fga_register_outbox`
// через текущую TX writer'а. Каждый tuple → одна строка с заданным event_type.
// После INSERT срабатывает trigger pg_notify('kacho_vpc_fga_register_outbox',
// NEW.id) — будит register-drainer.
type fgaRegisterEmitter struct {
	tx pgx.Tx
}

func (e *fgaRegisterEmitter) EmitRegister(ctx context.Context, intent fgaregister.Intent) error {
	return e.emit(ctx, fgaregister.EventRegister, intent)
}

func (e *fgaRegisterEmitter) EmitUnregister(ctx context.Context, intent fgaregister.Intent) error {
	return e.emit(ctx, fgaregister.EventUnregister, intent)
}

// emit вставляет одну fga_register_outbox-строку на каждый Item intent'а в той
// же TX, что и DML ресурса (one row per item — drainer клеймит независимо).
// Пустой intent — no-op (ничего не пишем).
//
// payload (JSONB) несет tuple + mirror-feed (labels + parent_project_id), а
// монотонный source_version штампуется из БД-часов (now()) ПРЯМО в INSERT через
// jsonb_set — внутри этой writer-TX, без отдельной колонки. source_version
// совпадает с created_at той же строки; для последовательных мутаций одного
// объекта позднейшая TX коммитится позже → ее now() строго больше (монотонность
// per-object).
//
// resource_kind/resource_id (миграция 0008) заполняются из tuple.Object
// ("<kind>:<id>", напр. "vpc_network:net-…") — это позволяет corelib
// outbox/reconciler адресовать intent'ы по ресурсу (derive-from-state backfill +
// inverse-orphan GC). Drainer эти колонки НЕ читает.
func (e *fgaRegisterEmitter) emit(ctx context.Context, eventType string, intent fgaregister.Intent) error {
	for _, it := range intent.Items {
		payload, err := fgaregister.Encode(fgaregister.Payload{
			Tuple:           it.Tuple,
			Labels:          it.Labels,
			ParentProjectID: it.ParentProjectID,
		})
		if err != nil {
			return fmt.Errorf("fga register intent marshal: %w", err)
		}
		kind, id := splitObject(it.Tuple.Object)
		if _, err := e.tx.Exec(ctx,
			`INSERT INTO kacho_vpc.fga_register_outbox
			   (event_type, resource_kind, resource_id, payload, created_at)
			 VALUES ($1, $2, $3,
			         jsonb_set($4::jsonb, '{source_version}', to_jsonb(now())),
			         now())`,
			eventType, kind, id, payload); err != nil {
			return fmt.Errorf("fga register intent insert: %w", err)
		}
	}
	return nil
}

// splitObject разбивает FGA-object "<kind>:<id>" на (kind, id) для трассировочных
// колонок resource_kind/resource_id. Объект без ':' → ("", object) (graceful).
func splitObject(object string) (kind, id string) {
	if i := strings.IndexByte(object, ':'); i >= 0 {
		return object[:i], object[i+1:]
	}
	return "", object
}
