// Package addresspool — use-case-структура admin-only ресурса AddressPool
// (skill evgeniy §2 B.1-B.4 + §6 G.1-G.7).
//
// Wave 5 batch 36 (KAC-94, skill `evgeniy`): миграция из старой «толстой»
// `internal/apps/kacho/services/addresspool/service.go` (AP-B.1) на
// use-case-структуру:
//
//	create.go               — CreateAddressPoolUseCase
//	update.go               — UpdateAddressPoolUseCase
//	delete.go               — DeleteAddressPoolUseCase
//	get.go                  — GetAddressPoolUseCase
//	list.go                 — ListAddressPoolsUseCase
//	check.go                — CheckUseCase (ambiguous-config diag)
//	explain_resolution.go   — ExplainResolutionUseCase
//	bindings.go             — Bind/Unbind use-case'ы + Cloud-selector wrappers
//	resolve.go              — cascade-resolve движок (используется address-UC'ами)
//	utilization.go          — GetPoolUtilization / ListPoolAddresses
//	handler.go              — тонкий gRPC server для InternalAddressPoolService
//	helpers.go              — CIDR-family-validation + IPv4-count helper
//
// AddressPool — admin-only ресурс (не выставляется через external TLS endpoint),
// поэтому Operation-flow и folder-AuthZ ему не нужны: каждый use-case
// синхронный, ответ — `*vpcv1.AddressPool` напрямую. Это сохраняется
// verbatim относительно legacy `*addresspool.AddressPoolService`.
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7):
// AddressPool / AddressPoolBinding / CloudPoolSelector переехали на CQRS-
// Repository (`kacho.Repository`). Use-case-слой больше не описывает узких
// port'ов на legacy `*repo.AddressPoolRepo` / `*repo.AddressPoolBindingRepo` /
// `*repo.CloudPoolSelectorRepo` — он работает через `kacho.Repository` с
// явным открытием `Reader(ctx)` / `Writer(ctx)`. Каждый mutate-use-case
// открывает writer, делает DML + outbox emit, потом Commit. Атомарность
// DML + outbox гарантируется одной pgx.Tx writer'а (G.5).
//
// Legacy `*repo.AddressPoolRepo` / `*repo.AddressPoolBindingRepo` /
// `*repo.CloudPoolSelectorRepo` НЕ удалены — на них завязаны
// `internal/repo/*_integration_test.go` (raw-SQL coverage of constraints).
// Финальное удаление — следующий sub-PR эпика A.7.
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
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Parity
// с `internal/apps/kacho/api/network/iface.go`.
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
// external_ipv6.address_pool_id; FK на стороне БД отсутствует (см. §16 в
// `kacho-vpc/CLAUDE.md`).
//
// Wave 5 A.7 sub-PR 1/6: AddressRepo осталась узким port'ом (а не через
// `kacho.Repository.Addresses()`), т.к. AddressPool-use-case'ы не требуют
// атомарной writer-TX (`Address.Insert` происходит из `CreateAddressUseCase`,
// который сам открывает свою writer-TX). `InitIPv6PoolCursor` — admin-side
// setup pool'а, идёт в writer-TX `CreateAddressPool/UpdateAddressPool` через
// `kacho.Repository.Writer().Addresses().InitIPv6PoolCursor` — НЕ через этот
// port (это duck-typed wrapper, см. update.go / create.go).
type AddressRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.AddressRecord, error)
}

// NetworkRepo — узкое чтение Network для BindAsNetworkDefault (FK-валидация).
// Wave 5 A.7 sub-PR 1/6: остаётся узким port'ом (parity с AddressRepo) —
// admin-side проверка существования Network не требует writer-TX.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
}

// SubnetReader — узкое чтение Subnet для cascade-resolve (internal IP path:
// subnet → network → network_default bind).
type SubnetReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
}

// ProjectClient — то, что use-case'ам AddressPool нужно от peer-сервиса
// kacho-iam (для cascade-resolve Step 3: project_id → cloud_id).
type ProjectClient interface {
	GetCloudIDFromProject(ctx context.Context, folderID string) (string, error)
}

// ZoneRegistry — port для проверки существования zone_id (compute domain после
// KAC-15: Geography — домен kacho-compute). nil-инстанс на composition root —
// валидно: zone-check тогда пропускается.
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
}
