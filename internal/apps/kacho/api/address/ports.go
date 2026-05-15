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
// Локальные port-интерфейсы (а не type-alias на `internal/ports.*Repo`)
// — skill §6 G.2-G.3: каждый use-case-пакет описывает только то, что РЕАЛЬНО
// использует. AddressPoolService живёт в `internal/apps/kacho/services/addresspool/`
// (миграция на use-case-структуру — отдельная итерация) — здесь объявлен лишь
// port `PoolService`, которому `*addresspool.AddressPoolService` удовлетворяет
// в composition root.
package address

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/ports`.
type (
	Pagination    = ports.Pagination
	AddressFilter = ports.AddressFilter
)

// AddressRepo — то, что use-case'ам Address нужно от репозитория адресов.
//
// Все методы возвращают `*domain.AddressRecord` (skill evgeniy §4 D.1 / §7 H.1 —
// repo-entity несёт DB-managed CreatedAt). Insert/Update/SetIPSpec/SetInternalIPv6/
// SetFolderID принимают `*domain.Address` (без CreatedAt).
type AddressRepo interface {
	Get(ctx context.Context, id string) (*domain.AddressRecord, error)
	List(ctx context.Context, f AddressFilter, p Pagination) ([]*domain.AddressRecord, string, error)
	Insert(ctx context.Context, a *domain.Address) (*domain.AddressRecord, error)
	Update(ctx context.Context, a *domain.Address) (*domain.AddressRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.AddressRecord, error)
	GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*domain.AddressRecord, error)
	SetIPSpec(ctx context.Context, id string, externalIpv4 *domain.ExternalIpv4Spec, internalIpv4 *domain.InternalIpv4Spec) (*domain.AddressRecord, error)
	SetInternalIPv6(ctx context.Context, id string, spec *domain.InternalIpv6Spec) (*domain.AddressRecord, error)

	// PG-native freelist IPAM (v4).
	AllocateIPFromFreelist(ctx context.Context, poolID, addressID string) (string, error)
	ReturnIPToFreelist(ctx context.Context, poolID, ip string) error

	// Sparse counter-based IPv6 IPAM.
	AllocateExternalIPv6(ctx context.Context, poolID, addressID, zoneID string) (string, error)
	FreeExternalIPv6(ctx context.Context, addressID string) error

	// Referrer-tracking (kept here — used by Address.Delete + AddressReference UCs).
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	ClearReference(ctx context.Context, addressID string) error
	GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
	ReferencesForAddresses(ctx context.Context, addressIDs []string) (map[string]*domain.AddressReference, error)
}

// SubnetReader — узкое чтение Subnet, нужное Address use-case'ам:
//   - Create.validateInternalIPInSubnet (sync-проверка что explicit IP в CIDR);
//   - Create.doCreate / Allocate*IP / AllocateInternalIPv6 — FK-валидация подсети;
//   - ListBySubnet — child-list через AddressesBySubnet.
type SubnetReader interface {
	Get(ctx context.Context, id string) (*domain.SubnetRecord, error)
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*domain.AddressRecord, string, error)
}

// FolderClient — то, что use-case'ам Address нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а на request-path /
// в worker'е Create/Move.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}

// PoolService — узкий port AddressPoolService для cascade-резолва pool по
// family. Реализуется `*addresspool.AddressPoolService` (миграция AddressPool —
// отдельная итерация, см. ports.go header).
//
// Использует FamilyV4 / FamilyV6 как enum (alias на addresspool.AddressFamily —
// не вводим параллельный тип, чтобы вызывающий handler/cmd прозрачно
// переиспользовал константы pool service'а).
type PoolService interface {
	ResolvePoolForAddressObjFamily(ctx context.Context, addr *domain.AddressRecord, family addresspool.AddressFamily) (*addresspool.ResolvedPool, error)
}
