// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package address — use-case-слой ресурса Address.
//
// Бизнес-логика разложена по use-case'ам: CreateAddressUseCase /
// UpdateAddressUseCase / DeleteAddressUseCase / GetAddressUseCase /
// ListAddressesUseCase / GetByValueUseCase / ListBySubnetUseCase /
// ListOperationsUseCase плюс тонкий gRPC-handler. Multi-family allocation flow
// (external v4/v6, internal v4/v6) и composition с AddressPoolService — внутри
// CreateAddressUseCase.
//
// Use-case'ы работают через CQRS-Repository (Reader / Writer), а не через узкий
// AddressRepo. Каждый открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`),
// и outbox-emit лежит в той же writer-TX — атомарность DML + outbox гарантирована.
// IPAM-flow (Insert + Allocate + Outbox) тоже атомарен внутри одной writer-TX в
// CreateAddressUseCase.doCreate.
//
// Pool service для cascade-резолва AddressPool по family живет в
// `internal/apps/kacho/api/addresspool/`. Здесь объявлен лишь port `PoolService`,
// которому `*addresspool.ResolverService` удовлетворяет в composition root.
package address

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/repo`.
type (
	Pagination    = repo.Pagination
	AddressFilter = repo.AddressFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
type (
	Repo               = kachorepo.Repository
	Reader             = kachorepo.RepositoryReader
	Writer             = kachorepo.RepositoryWriter
	AddressReaderIface = kachorepo.AddressReaderIface
	AddressWriterIface = kachorepo.AddressWriterIface
	OutboxEmitter      = kachorepo.OutboxEmitter
)

// SubnetReader — узкое чтение Subnet, нужное Address use-case'ам:
//   - Create.validateInternalIPInSubnet (sync-проверка что explicit IP в CIDR);
//   - Create.doCreate / Allocate*IP / AllocateInternalIPv6 — FK-валидация подсети;
//   - ListBySubnet — child-list через AddressesBySubnet.
//
// Port возвращает `*kacho.SubnetRecord`. В composition root реализуется
// `cqrsadapter.Subnet` поверх kachoRepo.
type SubnetReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*kachorepo.AddressRecord, string, error)
}

// ProjectClient — то, что use-case'ам Address нужно от peer-сервиса
// kacho-iam: проверка существования project'а на request-path /
// в worker'е Create.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ListFilter — port per-object List-фильтра. Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)`. nil → unfiltered passthrough.
// bypass=false && len(allowedIDs)==0 → пустой List (no-leak).
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}

// PoolService — узкий port AddressPool-resolver'а для cascade-резолва pool по
// family. Реализуется `*addresspool.ResolverService`.
//
// Использует FamilyV4 / FamilyV6 как enum (alias на addresspool.AddressFamily —
// не вводим параллельный тип, чтобы вызывающий handler/cmd прозрачно
// переиспользовал константы pool resolver'а).
type PoolService interface {
	ResolvePoolForAddressObjFamily(ctx context.Context, addr *kachorepo.AddressRecord, family addresspool.AddressFamily) (*addresspool.ResolvedPool, error)
}
