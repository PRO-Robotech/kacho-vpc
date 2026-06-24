// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package addresspool — use-case-слой admin-only ресурса AddressPool.
// Состав пакета:
//
//	create.go               — CreateAddressPoolUseCase
//	update.go               — UpdateAddressPoolUseCase
//	delete.go               — DeleteAddressPoolUseCase
//	get.go                  — GetAddressPoolUseCase
//	list.go                 — ListAddressPoolsUseCase
//	bindings.go             — BindAsNetworkDefault / UnbindNetworkDefault use-case'ы
//	resolve.go              — cascade-resolve движок (используется address-UC'ами)
//	utilization.go          — GetPoolUtilization / ListPoolAddresses
//	handler.go              — тонкий gRPC server для InternalAddressPoolService
//	helpers.go              — CIDR-family-validation + IPv4-count helper
//
// AddressPool — admin-only ресурс (не выставляется через external TLS endpoint),
// поэтому Operation-flow и project-AuthZ ему не нужны: каждый use-case
// синхронный, ответ — `*vpcv1.AddressPool` напрямую.
//
// AddressPool / AddressPoolBinding работают через CQRS-Repository
// (`kacho.Repository`) с явным открытием `Reader(ctx)` / `Writer(ctx)` — без
// узких port'ов на легаси-repo. Каждый mutate-use-case открывает writer, делает
// DML + outbox emit, затем Commit; атомарность DML + outbox гарантируется одной
// pgx.Tx writer'а.
//
// Единственный шаг cascade — network_default binding: pool, привязанный к сети
// как дефолтный, либо is_default-fallback по (zone_id, kind).
package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination / Filter — переиспользуем единые value-объекты `internal/repo`
// (alias'ы, не копии): caller-code и handler работают с привычными типами без
// двойной конверсии.
type (
	Pagination        = repo.Pagination
	AddressPoolFilter = repo.AddressPoolFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`),
// единообразно с другими ресурсными пакетами.
type (
	Repo          = kachorepo.Repository
	Reader        = kachorepo.RepositoryReader
	Writer        = kachorepo.RepositoryWriter
	OutboxEmitter = kachorepo.OutboxEmitter
)

// Re-export sentinel-ошибок repo-слоя как `var`. errors.Is(err,
// addresspool.ErrPoolNotResolved) совпадает с repo.ErrPoolNotResolved —
// одна и та же error-value. Нужно: handler делает
// `errors.Is(err, addresspool.ErrPoolNotResolved)` (см.
// `internal/handler/internal_address_pool_handler.go::ExplainResolution`).
var (
	ErrNotFound        = repo.ErrNotFound
	ErrPoolNotResolved = repo.ErrPoolNotResolved
)

// AddressRepo — узкое чтение Address для cascade-resolve + IPv6 cursor init.
// AddressPool ↔ Address связаны через JSONB external_ipv4.address_pool_id /
// external_ipv6.address_pool_id; FK на стороне БД отсутствует.
//
// Это намеренно узкий port (а не `kacho.Repository.Addresses()`), т.к.
// AddressPool-use-case'ы не требуют атомарной writer-TX: `Address.Insert`
// происходит из `CreateAddressUseCase`, который сам открывает свою writer-TX.
// `InitIPv6PoolCursor` — admin-side setup пула, идет в writer-TX
// `CreateAddressPool/UpdateAddressPool` через
// `kacho.Repository.Writer().Addresses().InitIPv6PoolCursor` — НЕ через этот
// port (см. update.go / create.go).
type AddressRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.AddressRecord, error)
}

// NetworkRepo — узкое чтение Network для BindAsNetworkDefault (FK-валидация).
// Намеренно узкий port (как AddressRepo) — admin-side проверка существования
// Network не требует writer-TX.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
}

// SubnetReader — узкое чтение Subnet для cascade-resolve (internal IP path:
// subnet → network → network_default bind).
type SubnetReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
}

// ZoneRegistry — port для проверки существования zone_id (Geography — leaf-домен
// kacho-geo; реализация — geo.v1.ZoneService.Get). nil-инстанс на composition
// root — валидно: zone-check тогда пропускается.
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
}
