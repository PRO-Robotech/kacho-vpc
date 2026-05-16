// Package kacho — CQRS-разделённый корневой контракт репозитория VPC.
//
// Phase 5 (skill evgeniy §6 G.1-G.7, KAC-94): Repository / RepositoryReader /
// RepositoryWriter явно разделяют read-path и write-path. Use-case-слой видит
// в типе вызова, читает он ресурс или меняет — это упрощает routing на slave-
// реплику (G.4) и фиксирует точку открытия транзакции (G.5).
//
// Pilot реализует Network (как первый ресурс из 8 в kacho-vpc). Остальные 7
// (Subnet/Address/RouteTable/SecurityGroup/Gateway/PrivateEndpoint/NetworkInterface)
// — replicate-фаза, отдельные subtasks эпика KAC-94.
//
// Адаптеры:
//   - `internal/repo/kacho/pg/` — pgxpool-impl (read-only TX vs RW TX).
//   - `internal/repo/kacho/kachomock/` — in-memory implementation для unit-тестов
//     use-case'ов (replaces repomock.NetworkRepo для Network-кода).
//
// Legacy `*repo.NetworkRepo` / `internal/repo/repomock.NetworkRepo` НЕ удаляются
// — на них завязаны admin-сервисы (networkinternal/addresspool/...) и
// существующие integration-тесты `internal/repo/network_repo_*test.go`,
// которые проверяют именно legacy-репо.
package kacho

import "context"

// Repository — корневой контракт repo-слоя VPC.
//
// Reader(ctx) открывает read-only TX (read-committed) — на slave-реплике, когда
// та будет; сейчас на той же мастер-pool. Caller обязан вызвать Close() после
// использования (rollback read-only TX).
//
// Writer(ctx) открывает RW TX на мастере. Caller обязан вызвать либо Commit(),
// либо Abort() (Abort идемпотентен — безопасно ставить через defer).
type Repository interface {
	Reader(ctx context.Context) (RepositoryReader, error)
	Writer(ctx context.Context) (RepositoryWriter, error)
	Close()
}

// RepositoryReader — read-only проекция репозитория. Все ресурс-специфичные
// reader'ы возвращаются через свой method.
//
// Pilot: Networks() + SecurityGroups() (последнее — batch 33/34, KAC-94: SG-CQRS
// нужен, чтобы Network.Create мог inline создать default-SG в одной writer-TX).
// Wave 5 replicate (KAC-94): Addresses() / RouteTables() / PrivateEndpoints() /
// NetworkInterfaces() / Subnets() добавлены — все 5 ресурсов переехали на CQRS
// repo (attach-race protection KAC-52, IPAM allocate в одной writer-TX, v4/v6
// cardinality CHECK на NIC, FK RESTRICT — все DB-уровневые, репо только маппит
// SQL → repo-sentinel).
// Дальнейшие итерации эпика: Gateways() — последний из 8.
type RepositoryReader interface {
	Networks() NetworkReaderIface
	SecurityGroups() SecurityGroupReaderIface
	Addresses() AddressReaderIface
	RouteTables() RouteTableReaderIface
	PrivateEndpoints() PrivateEndpointReaderIface
	NetworkInterfaces() NetworkInterfaceReaderIface
	Subnets() SubnetReaderIface
	// Gateways — Wave 5 replicate (KAC-94): Gateway read-iface на текущей read-TX.
	Gateways() GatewayReaderIface
	// Close завершает read-TX (rollback). Идемпотентно.
	Close() error
}

// RepositoryWriter — RW проекция репозитория. Writer виден свои writes
// (G.2 — writer extends reader). Outbox-emit живёт здесь же — это
// гарантирует атомарность DML + outbox в одной TX (skill evgeniy §6 G.5).
//
// Pilot: Networks() + SecurityGroups() + Outbox() (batch 33/34, KAC-94).
// Wave 5 replicate (KAC-94): Addresses() — Address Create/Update/Delete/Move +
// IPAM allocate (SetIPSpec/AllocateIPFromFreelist/AllocateExternalIPv6/…) теперь
// идут через единый writer.Addresses().*  Атомарность Insert + Allocate +
// outbox-emit гарантируется одной pgx.Tx writer'а (skill evgeniy I.9/I.10).
// RouteTables() — добавлен в этой же replicate-фазе. PrivateEndpoints() —
// тоже в этой же replicate-фазе (parity, FK network_id/subnet_id/address_id из
// миграции 0024 проверяются Postgres'ом в commit-time writer-TX).
// NetworkInterfaces() — NIC-CQRS writer (atomic AttachToInstance CAS KAC-52 +
// idempotent DetachFromInstance + Insert с возможным MAC-collision sentinel).
// Subnets() — Subnet-CQRS writer (последний из 7 после Gateway).
type RepositoryWriter interface {
	Networks() NetworkWriterIface
	SecurityGroups() SecurityGroupWriterIface
	Addresses() AddressWriterIface
	RouteTables() RouteTableWriterIface
	PrivateEndpoints() PrivateEndpointWriterIface
	NetworkInterfaces() NetworkInterfaceWriterIface
	Subnets() SubnetWriterIface
	// Gateways — Wave 5 replicate (KAC-94): Gateway write-iface на текущей write-TX.
	Gateways() GatewayWriterIface
	// Outbox — emit события в vpc_outbox в той же tx-области writer'а.
	Outbox() OutboxEmitter
	// Commit финализирует tx. После Commit вызов Abort — no-op.
	Commit() error
	// Abort откатывает tx. Идемпотентен — после Commit no-op, можно ставить
	// через `defer w.Abort()` сразу после открытия writer'а.
	Abort()
}

// OutboxEmitter — emit одного outbox-события (vpc_outbox row + trigger pg_notify).
// Использует pgx.Tx writer'а, поэтому DML + outbox commit'ятся атомарно: либо
// resource + event оба видны watcher'у, либо ничего (Abort).
//
// payload — произвольная map (snapshot resource'а после DML). nil → пустой JSON.
type OutboxEmitter interface {
	Emit(ctx context.Context, resource, id, action string, payload map[string]any) error
}
