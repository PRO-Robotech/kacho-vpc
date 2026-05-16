// Package address — use-case-структура ресурса Address (skill evgeniy §2 B.1-B.4).
//
// Wave 3 (KAC-94): replicate Wave 3a pilot Network → Address. Бизнес-логика
// AddressService переехала сюда: CreateAddressUseCase / UpdateAddressUseCase /
// DeleteAddressUseCase / MoveAddressUseCase / GetAddressUseCase /
// ListAddressesUseCase / GetByValueUseCase / ListBySubnetUseCase /
// ListOperationsUseCase плюс тонкий gRPC-handler. Multi-family allocation flow
// (external v4/v6, internal v4/v6) и composition с AddressPoolService — внутри
// CreateAddressUseCase.
//
// A.7 sub-PR 2 (KAC-94, skill evgeniy §6 G.1-G.7): Address переехал на CQRS-
// Repository вслед за Network (Wave 5 pilot) и Subnet/SG/RT/PE/NIC (Wave 5
// replicate). Use-case'ы Address теперь работают через `kacho.Repository`
// (Reader / Writer), а не напрямую через узкий `AddressRepo`. Каждый use-case
// открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`), и outbox-emit
// лежит в той же tx writer'а — атомарность DML + outbox гарантирована (G.5).
// Atomicity IPAM-flow (Insert + Allocate + Outbox) сохранена внутри одной
// writer-TX в CreateAddressUseCase.doCreate.
//
// Pool service для cascade-резолва AddressPool по family — `internal/apps/kacho/
// api/addresspool/` (use-case-структура, Wave 5 batch 36, KAC-94). Здесь
// объявлен лишь port `PoolService`, которому `*addresspool.ResolverService`
// удовлетворяет в composition root.
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
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов. Parity с
// `internal/apps/kacho/api/network/iface.go`.
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
// Subnet ещё не на CQRS-iface в этом sub-PR — продолжаем использовать legacy
// port-shape (возвращает `*kacho.SubnetRecord`). Реализуется legacy
// `*repo.SubnetRepo` в composition root.
type SubnetReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*kachorepo.AddressRecord, string, error)
}

// FolderClient — то, что use-case'ам Address нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а на request-path /
// в worker'е Create/Move.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}

// PoolService — узкий port AddressPool-resolver'а для cascade-резолва pool по
// family. Реализуется `*addresspool.ResolverService` (после переезда AP на
// use-case-структуру — Wave 5 batch 36, KAC-94).
//
// Использует FamilyV4 / FamilyV6 как enum (alias на addresspool.AddressFamily —
// не вводим параллельный тип, чтобы вызывающий handler/cmd прозрачно
// переиспользовал константы pool resolver'а).
type PoolService interface {
	ResolvePoolForAddressObjFamily(ctx context.Context, addr *kachorepo.AddressRecord, family addresspool.AddressFamily) (*addresspool.ResolvedPool, error)
}
