// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package networkinterface — use-case-структура ресурса NetworkInterface (NIC).
//
// NIC — самостоятельный сетевой интерфейс, отвязанный от Instance. У него нет
// Move RPC (NIC привязан к Subnet, перемещение между project'ами не
// поддерживается). RPC AttachToInstance / DetachFromInstance отсутствуют —
// NIC-ресурс и `used_by`-колонки остаются, но через эти RPC не выставляются.
//
// Use-case'ы NIC работают через CQRS-Repository (Reader / Writer split). Каждый
// use-case открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`), outbox-emit
// лежит в той же writer-TX — атомарность DML + outbox гарантирована. Parent-Subnet
// validation в Create идет через `kachoRepo.Reader().Subnets().Get`; Reader-TX
// автоматически уходит на slave-pool, если он настроен.
//
// Address-attach/detach при NIC.Create / Update идёт через writer-TX
// (`w.Addresses()`) в ТОЙ ЖЕ транзакции, что и Insert/UpdateMeta(NIC) + outbox +
// fga-register — reservation и NIC коммитятся/откатываются атомарно (нет orphan
// used=true без persisted NIC при краше worker'а; project-rule #10/#11).
package networkinterface

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination — alias на единый value-объект `internal/repo/kacho` (legacy
// `repo.Pagination` сам уже alias на `kacho.Pagination` через iface.go).
type Pagination = kachorepo.Pagination

// NetworkInterfaceFilter — фильтр для List; alias на CQRS-iface
// `kacho.NetworkInterfaceFilter` (поля ProjectID/InstanceID/SubnetID/NetworkID).
type NetworkInterfaceFilter = kachorepo.NetworkInterfaceFilter

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
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
// не занят) + пометка used + referrer-tracking при attach/detach. Возвращает
// `*kacho.AddressRecord` (repo-entity из leaf-пакета `internal/repo/kacho`).
type AddressRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.AddressRecord, error)
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	ClearReference(ctx context.Context, addressID string) error
}

// ProjectClient — то, что use-case'ам NIC нужно от peer-сервиса
// kacho-iam: проверка существования project'а.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ListFilter — port per-object List-фильтра. Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)`. nil → unfiltered passthrough.
// bypass=false && len(allowedIDs)==0 → пустой List (no-leak).
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}
