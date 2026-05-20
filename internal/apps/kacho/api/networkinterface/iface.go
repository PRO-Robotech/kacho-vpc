// Package networkinterface — use-case-структура ресурса NetworkInterface (NIC,
// skill evgeniy §2 B.1-B.4).
//
// Wave 3 (KAC-94): NIC переехал с монолитного `internal/service/network_interface.go`
// (NetworkInterfaceService) на use-case-структуру. NIC — first-class сетевой
// интерфейс (AWS-ENI-style; epic KAC-2). У него **нет Move RPC** (NIC привязан
// к Subnet, перемещение между folder'ами не поддерживается verbatim YC API). У
// него есть две специфические операции — `AttachToInstance` / `DetachFromInstance`
// — с атомарным CAS-апдейтом `used_by_id` (миграция 0016, KAC-52; workspace
// CLAUDE.md §«Within-service refs — DB-уровень обязателен», запрет #10).
//
// Wave 5 replicate (KAC-94, NIC batch, skill evgeniy §6 G.1-G.7): use-case'ы NIC
// работают через CQRS-Repository (Reader / Writer split) — аналог Network pilot.
// Каждый use-case открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`), и
// outbox-emit лежит в той же tx writer'а — атомарность DML + outbox гарантирована
// (G.5). Это финальная защита от TOCTOU при NIC attach (KAC-52): single-statement
// AttachToInstance CAS на writer-TX закрывает race-window между Get → check →
// Update в use-case'е.
//
// A.7 sub-PR 3/6 (KAC-94): peer-port `SubnetReader` упразднён — parent-Subnet
// validation в Create идёт через `kachoRepo.Reader().Subnets().Get`. Это удаляет
// последнюю legacy-зависимость на `*repo.SubnetRepo` из NIC use-case'ов; Reader-TX
// автоматически уходит на slave-pool, если он настроен (G.4).
//
// Address-attach при NIC.Create / Update по-прежнему идёт через legacy `AddressRepo`
// (отдельная TX в `AddressRepo.SetReference`) — переход на единую writer-TX
// (Insert(NIC) + SetReference(addr) атомарно) — отдельный шаг следующей итерации
// (требует CQRS для Address с `SetReference` в writer-iface).
package networkinterface

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination — alias на единый value-объект `internal/repo/kacho` (legacy
// `repo.Pagination` сам уже alias на `kacho.Pagination` через iface.go).
type Pagination = kachorepo.Pagination

// NetworkInterfaceFilter — фильтр для List. Wave 5 replicate (KAC-94, NIC batch):
// alias на CQRS-iface `kacho.NetworkInterfaceFilter`. Поля идентичны legacy
// `repo.NetworkInterfaceFilter` (ProjectID/InstanceID/SubnetID/NetworkID) — обе
// структуры по сути одно и то же, после полной CQRS-миграции legacy `repo`-тип
// уберём.
type NetworkInterfaceFilter = kachorepo.NetworkInterfaceFilter

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов. Parity с
// `internal/apps/kacho/api/network/iface.go`.
type (
	Repo                        = kachorepo.Repository
	Reader                      = kachorepo.RepositoryReader
	Writer                      = kachorepo.RepositoryWriter
	NetworkInterfaceReaderIface = kachorepo.NetworkInterfaceReaderIface
	NetworkInterfaceWriterIface = kachorepo.NetworkInterfaceWriterIface
	OutboxEmitter               = kachorepo.OutboxEmitter
)

// AddressRepo — узкий интерфейс работы с Address-ресурсами, нужный NIC use-case'ам:
// валидация cross-resource (Address существует, нужной IP-версии, в той же подсети,
// не занят) + помечание used + referrer-tracking при attach/detach.
//
// Возвращает `*kacho.AddressRecord` — repo-entity переехала в leaf-пакет
// `internal/repo/kacho/entity_address.go` ранее в replicate-фазе (Wave 5).
type AddressRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.AddressRecord, error)
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	ClearReference(ctx context.Context, addressID string) error
}

// ProjectClient — то, что use-case'ам NIC нужно от peer-сервиса
// kacho-iam: проверка существования folder'а.
type ProjectClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
