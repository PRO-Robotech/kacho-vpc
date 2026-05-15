// Package subnet — use-case-структура ресурса Subnet (skill evgeniy §2 B.1-B.4).
//
// Wave 3 (KAC-94): replicate Wave 3a pilot Network → Subnet. Бизнес-логика
// CreateSubnetUseCase / UpdateSubnetUseCase / DeleteSubnetUseCase /
// MoveSubnetUseCase / GetSubnetUseCase / ListSubnetsUseCase /
// AddCidrBlocksUseCase / RemoveCidrBlocksUseCase / RelocateUseCase /
// ListUsedAddressesUseCase / ListOperationsUseCase плюс тонкий gRPC-handler.
// Раньше монолитный `internal/service/subnet.go` (SubnetService) был fat-service
// со всеми методами в одном файле; теперь use-case'ы локализованы рядом с
// handler'ом (B.4 — локальность), repo-операции делегируются через **локальные**
// port-интерфейсы (ниже).
//
// Локальные интерфейсы (а не type-alias на `internal/ports.SubnetRepo`) — это
// сознательный выбор по skill §6 G.2-G.3: каждый use-case-пакет описывает только
// то, что РЕАЛЬНО использует. Адаптерами выступают существующие
// `internal/repo/subnet_repo.go` и `internal/ports/portmock` — они уже реализуют
// `internal/ports.SubnetRepo`, который ⊇ локальному интерфейсу, поэтому
// Go-типизация работает без shim'ов.
package subnet

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/ports`
// (alias'ы, не копии).
type (
	Pagination   = ports.Pagination
	SubnetFilter = ports.SubnetFilter
)

// SubnetRepo — то, что use-case'ам Subnet нужно от репозитория подсетей.
//
// Все методы возвращают `*domain.SubnetRecord` (skill evgeniy §4 D.1 / §7 H.1 —
// repo-entity несёт DB-managed CreatedAt). Insert/Update принимают `*domain.Subnet`
// (без CreatedAt).
type SubnetRepo interface {
	Get(ctx context.Context, id string) (*domain.SubnetRecord, error)
	List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.SubnetRecord, string, error)
	Insert(ctx context.Context, s *domain.Subnet) (*domain.SubnetRecord, error)
	Update(ctx context.Context, s *domain.Subnet) (*domain.SubnetRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.SubnetRecord, error)
	SetCidrBlocks(ctx context.Context, id string, v4, v6 []string) (*domain.SubnetRecord, error)
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*domain.AddressRecord, string, error)
}

// NetworkReader — узкий read-интерфейс для проверки parent Network в Create.
type NetworkReader interface {
	Get(ctx context.Context, id string) (*domain.NetworkRecord, error)
}

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
type NetworkInterfaceRepo interface {
	ListBySubnet(ctx context.Context, subnetID string) ([]*domain.NetworkInterfaceRecord, error)
}

// FolderClient — то, что use-case'ам Subnet нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а на request-path / в
// worker'е.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}

// ZoneRegistry — port для проверки существования зоны (таблица `zones`).
// Используется Create / Relocate (validateZoneID). После KAC-15 (Geography в
// kacho-compute) реализация — gRPC-клиент к `compute.v1.ZoneService.Get`.
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
}
