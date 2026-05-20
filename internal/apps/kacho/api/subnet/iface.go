// Package subnet — use-case-структура ресурса Subnet (skill evgeniy §2 B.1-B.4).
//
// Wave 3 (KAC-94): replicate Wave 3a pilot Network → Subnet. Бизнес-логика
// CreateSubnetUseCase / UpdateSubnetUseCase / DeleteSubnetUseCase /
// MoveSubnetUseCase / GetSubnetUseCase / ListSubnetsUseCase /
// AddCidrBlocksUseCase / RemoveCidrBlocksUseCase / RelocateUseCase /
// ListUsedAddressesUseCase / ListOperationsUseCase плюс тонкий gRPC-handler.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): Subnet переехал на CQRS-
// Repository вслед за Network (Wave 5 pilot) и SecurityGroup (batch 33/34).
// Use-case'ы Subnet теперь работают через `kacho.Repository` (Reader / Writer),
// а не напрямую через узкий `SubnetRepo`. Каждый use-case открывает TX явно
// (`u.repo.Writer(ctx)` или `Reader(ctx)`), и outbox-emit лежит в той же
// tx writer'а — атомарность DML + outbox гарантирована (G.5).
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
// (подсеть с NIC, приаттаченным к compute-инстансу, удалить нельзя). Optional —
// `nil` → проверка пропускается (FK RESTRICT в worker'е всё равно подберёт
// address-bearing NIC через цепочку NIC → Address → Subnet). NIC-репо живёт в
// `internal/service/network_interface.go` — wire через composition root.
//
// Wave 5 replicate (KAC-94, NIC batch): возвращает `*kacho.NetworkInterfaceRecord`
// — NIC-Record переехал из domain в repo-leaf.
type NetworkInterfaceRepo interface {
	ListBySubnet(ctx context.Context, subnetID string) ([]*kachorepo.NetworkInterfaceRecord, error)
}

// ProjectClient — то, что use-case'ам Subnet нужно от peer-сервиса
// kacho-iam: проверка существования folder'а на request-path / в
// worker'е.
type ProjectClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}

// ZoneRegistry — port для проверки существования зоны (таблица `zones`).
// Используется Create / Relocate (validateZoneID). После KAC-15 (Geography в
// kacho-compute) реализация — gRPC-клиент к `compute.v1.ZoneService.Get`.
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
}
