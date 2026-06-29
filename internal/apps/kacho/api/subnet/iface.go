// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package subnet — use-case-структура ресурса Subnet: бизнес-логика
// CreateSubnetUseCase / UpdateSubnetUseCase / DeleteSubnetUseCase /
// GetSubnetUseCase / ListSubnetsUseCase / AddCidrBlocksUseCase /
// RemoveCidrBlocksUseCase / ListUsedAddressesUseCase / ListOperationsUseCase
// плюс тонкий gRPC-handler.
//
// Use-case'ы работают через CQRS `kacho.Repository` (Reader / Writer), а не
// напрямую через узкий repo-интерфейс. Каждый use-case открывает TX явно
// (`u.repo.Writer(ctx)` или `Reader(ctx)`), и outbox-emit лежит в той же
// tx writer'а — атомарность DML + outbox гарантирована.
package subnet

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/repo`
// (alias'ы, не копии). Иначе пришлось бы дублировать структуры или гонять между
// пакетами через двойную конверсию.
type (
	Pagination   = repo.Pagination
	SubnetFilter = repo.SubnetFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
type (
	Repo              = kachorepo.Repository
	Reader            = kachorepo.RepositoryReader
	Writer            = kachorepo.RepositoryWriter
	SubnetReaderIface = kachorepo.SubnetReaderIface
	SubnetWriterIface = kachorepo.SubnetWriterIface
	OutboxEmitter     = kachorepo.OutboxEmitter
)

// AddressRefRepo — узкий интерфейс для обогащения ListUsedAddresses записями
// referrer'ов (кто использует адрес). Optional — `nil` → references[] пуст
// (graceful degradation). Используется только в ListUsedAddressesUseCase.
type AddressRefRepo interface {
	ReferencesForAddresses(ctx context.Context, addressIDs []string) (map[string]*domain.AddressReference, error)
}

// NetworkInterfaceRepo — узкий интерфейс для precondition-проверки в Delete
// (подсеть с NIC, приаттаченным к инстансу, удалить нельзя). Optional —
// `nil` → проверка пропускается (FK RESTRICT в worker'е все равно подберет
// address-bearing NIC через цепочку NIC → Address → Subnet). NIC-репо живет в
// `internal/repo/kacho/pg/network_interface.go` — wire через composition root.
type NetworkInterfaceRepo interface {
	ListBySubnet(ctx context.Context, subnetID string) ([]*kachorepo.NetworkInterfaceRecord, error)
}

// ProjectClient — то, что use-case'ам Subnet нужно от peer-сервиса
// kacho-iam: проверка существования project'а на request-path / в
// worker'е.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ZoneRegistry — port для проверки существования зоны (используется Create,
// validateZoneID). Реализация — gRPC-клиент к `geo.v1.ZoneService.Get`
// (Geography — leaf-домен kacho-geo).
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
}

// ListFilter — port per-object List-фильтра. Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)` поверх AuthorizeService.ListObjects.
// nil → use-case делает unfiltered passthrough (list-filter disabled / dev).
//
//   - allowedIDs: explicit set разрешенных subnet-id (repo.ListByIDs → WHERE id=ANY).
//   - bypass:     true → фильтр не сужает (wildcard scope_grant) → обычный repo.List.
//   - err:        infra недоступна → fail-closed (Unavailable у use-case).
//
// bypass=false && len(allowedIDs)==0 → пустой List (no-leak).
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}
