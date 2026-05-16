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

	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Repository — pgxpool-impl корневого CQRS-контракта.
//
// Skill evgeniy §6 G.4: Reader идёт на slave-реплику, Writer — на master.
// Два физических pgxpool.Pool — обычно одна и та же логическая БД, но
// slavePool читает из streaming-replica (hot_standby=on). Failover handled
// outside (PGBouncer / Patroni / etc.) — Repository только маршрутизирует.
//
// Если slavePool не настроен (nil или передан тот же master) — Reader
// открывает read-only TX на master-pool. Это структурный задел: код во всех
// use-case'ах уже разделён на Reader/Writer, и переключение на реальную
// реплику — это только wiring-изменение в `cmd/vpc/main.go` (`slavePool != nil`).
type Repository struct {
	master *pgxpool.Pool
	slave  *pgxpool.Pool
}

// New собирает Repository поверх master- и опц. slave-pool'ов (skill evgeniy
// §6 G.4).
//
//   - masterPool — RW pgxpool на primary; используется Writer + Reader-fallback.
//   - slavePool  — RO pgxpool на streaming-replica; если nil → Reader идёт на
//     master (fallback, текущее dev/prod-поведение). Когда реальная реплика
//     появляется — composition root передаёт второй pool, и Reader-TX уходят
//     на неё без изменений в use-case-слое.
//
// Pools создаются в composition root (обычно из `kacho-corelib/db.NewPool`).
func New(masterPool, slavePool *pgxpool.Pool) *Repository {
	if slavePool == nil {
		slavePool = masterPool
	}
	return &Repository{master: masterPool, slave: slavePool}
}

// Reader открывает read-only TX (read-committed) на **slave-pool'е**, если он
// настроен; иначе на master (fallback). Возвращённый reader обязан быть закрыт
// через Close() — это rollback'ит TX и возвращает соединение в пул.
//
// Skill evgeniy §6 G.4: разгружает master от read-нагрузки. На реплике —
// read-committed TX гарантированно read-only (streaming replica не принимает
// writes); на master fallback — `pgx.TxOptions{AccessMode: pgx.ReadOnly}`
// добавляет ту же гарантию на уровне Postgres.
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
// Skill evgeniy §6 G.4: writes всегда идут на primary; репликация на slave —
// асинхронная Postgres streaming replication, прозрачно для use-case'а.
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
// Wave 5 batch 33/34 (KAC-94).
func (r *readerImpl) SecurityGroups() kacho.SecurityGroupReaderIface {
	return &securityGroupReader{tx: r.tx}
}

// Addresses возвращает Address-reader, привязанный к этой read-TX. Wave 5
// replicate (KAC-94, skill evgeniy I.9/I.10).
func (r *readerImpl) Addresses() kacho.AddressReaderIface {
	return &addressReader{tx: r.tx}
}

// PrivateEndpoints возвращает PrivateEndpoint-reader, привязанный к этой read-TX.
// Wave 5 replicate (KAC-94).
func (r *readerImpl) PrivateEndpoints() kacho.PrivateEndpointReaderIface {
	return &privateEndpointReader{tx: r.tx}
}

// RouteTables возвращает RouteTable-reader, привязанный к этой read-TX.
// Wave 5 replicate (KAC-94).
func (r *readerImpl) RouteTables() kacho.RouteTableReaderIface {
	return &routeTableReader{tx: r.tx}
}

// NetworkInterfaces возвращает NIC-reader, привязанный к этой read-TX.
// Wave 5 replicate (KAC-94, NIC batch). См. doc-комментарий на iface_network_interface.go.
func (r *readerImpl) NetworkInterfaces() kacho.NetworkInterfaceReaderIface {
	return &networkInterfaceReader{tx: r.tx}
}

// Subnets возвращает Subnet-reader, привязанный к этой read-TX.
// Wave 5 replicate (KAC-94).
func (r *readerImpl) Subnets() kacho.SubnetReaderIface {
	return &subnetReader{tx: r.tx}
}

// Gateways возвращает Gateway-reader, привязанный к этой read-TX.
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7).
func (r *readerImpl) Gateways() kacho.GatewayReaderIface {
	return &gatewayReader{tx: r.tx}
}

// AddressPools возвращает AddressPool-reader, привязанный к этой read-TX.
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7).
func (r *readerImpl) AddressPools() kacho.AddressPoolReaderIface {
	return &addressPoolReader{tx: r.tx}
}

// AddressPoolBindings — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (r *readerImpl) AddressPoolBindings() kacho.AddressPoolBindingReaderIface {
	return &addressPoolBindingReader{tx: r.tx}
}

// CloudPoolSelectors — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (r *readerImpl) CloudPoolSelectors() kacho.CloudPoolSelectorReaderIface {
	return &cloudPoolSelectorReader{tx: r.tx}
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

// SecurityGroups возвращает SecurityGroup-writer, привязанный к этой write-TX.
// G.2: writer видит свои writes (reader-методы — поверх той же pgx.Tx).
// Wave 5 batch 33/34 (KAC-94, skill evgeniy I.9/I.10).
func (w *writerImpl) SecurityGroups() kacho.SecurityGroupWriterIface {
	return &securityGroupWriter{
		securityGroupReader: securityGroupReader{tx: w.tx},
		emitter:             &outboxEmitter{tx: w.tx},
	}
}

// Addresses возвращает Address-writer, привязанный к этой write-TX. G.2: writer
// видит свои writes (reader-методы — поверх той же pgx.Tx). Wave 5 replicate
// (KAC-94, skill evgeniy I.9/I.10): IPAM allocate-flow атомарен — Insert(addr)
// + AllocateIPFromFreelist/AllocateExternalIPv6 + outbox-emit идут в одной
// writer-TX, либо все вместе видны, либо ни один (Abort/crash).
func (w *writerImpl) Addresses() kacho.AddressWriterIface {
	return &addressWriter{
		addressReader: addressReader{tx: w.tx},
		emitter:       &outboxEmitter{tx: w.tx},
	}
}

// PrivateEndpoints возвращает PrivateEndpoint-writer, привязанный к этой write-TX.
// G.2: writer видит свои writes. Wave 5 replicate (KAC-94).
func (w *writerImpl) PrivateEndpoints() kacho.PrivateEndpointWriterIface {
	return &privateEndpointWriter{
		privateEndpointReader: privateEndpointReader{tx: w.tx},
		emitter:               &outboxEmitter{tx: w.tx},
	}
}

// RouteTables возвращает RouteTable-writer, привязанный к этой write-TX.
// G.2: writer видит свои writes (reader-методы — поверх той же pgx.Tx).
// Wave 5 replicate (KAC-94).
func (w *writerImpl) RouteTables() kacho.RouteTableWriterIface {
	return &routeTableWriter{
		routeTableReader: routeTableReader{tx: w.tx},
		emitter:          &outboxEmitter{tx: w.tx},
	}
}

// NetworkInterfaces возвращает NIC-writer, привязанный к этой write-TX.
// G.2: writer видит свои writes. Wave 5 replicate (KAC-94, NIC batch). Includes
// atomic AttachToInstance CAS (KAC-52), idempotent DetachFromInstance, Insert
// с возможным MAC-collision sentinel (caller retry'ит с новым MAC).
func (w *writerImpl) NetworkInterfaces() kacho.NetworkInterfaceWriterIface {
	return &networkInterfaceWriter{
		networkInterfaceReader: networkInterfaceReader{tx: w.tx},
		emitter:                &outboxEmitter{tx: w.tx},
	}
}

// Subnets возвращает Subnet-writer, привязанный к этой write-TX.
// G.2: writer видит свои writes (reader-методы — поверх той же pgx.Tx).
// Wave 5 replicate (KAC-94).
func (w *writerImpl) Subnets() kacho.SubnetWriterIface {
	return &subnetWriter{
		subnetReader: subnetReader{tx: w.tx},
		emitter:      &outboxEmitter{tx: w.tx},
	}
}

// Gateways возвращает Gateway-writer, привязанный к этой write-TX.
// G.2: writer видит свои writes (reader-методы — поверх той же pgx.Tx).
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7).
func (w *writerImpl) Gateways() kacho.GatewayWriterIface {
	return &gatewayWriter{
		gatewayReader: gatewayReader{tx: w.tx},
		emitter:       &outboxEmitter{tx: w.tx},
	}
}

// AddressPools — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7).
// G.2: writer видит свои writes. AddressPool — admin-only, Create+
// PopulateFreelistForPool + outbox-emit идут атомарно в одной writer-TX.
func (w *writerImpl) AddressPools() kacho.AddressPoolWriterIface {
	return &addressPoolWriter{
		addressPoolReader: addressPoolReader{tx: w.tx},
		emitter:           &outboxEmitter{tx: w.tx},
	}
}

// AddressPoolBindings — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (w *writerImpl) AddressPoolBindings() kacho.AddressPoolBindingWriterIface {
	return &addressPoolBindingWriter{
		addressPoolBindingReader: addressPoolBindingReader{tx: w.tx},
		emitter:                  &outboxEmitter{tx: w.tx},
	}
}

// CloudPoolSelectors — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (w *writerImpl) CloudPoolSelectors() kacho.CloudPoolSelectorWriterIface {
	return &cloudPoolSelectorWriter{
		cloudPoolSelectorReader: cloudPoolSelectorReader{tx: w.tx},
		emitter:                 &outboxEmitter{tx: w.tx},
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
// helpers.EmitVPC (который оборачивает kacho-corelib/outbox.Emit с фиксированной
// таблицей "vpc_outbox" + trigger pg_notify('vpc_outbox', ...)).
type outboxEmitter struct {
	tx pgx.Tx
}

// Emit добавляет outbox-row в той же tx, что и DML resource'а.
// payload nil → пустой JSON-объект (как у legacy emitVPC).
func (e *outboxEmitter) Emit(ctx context.Context, resource, id, action string, payload map[string]any) error {
	return helpers.EmitVPC(ctx, e.tx, resource, id, action, payload)
}
