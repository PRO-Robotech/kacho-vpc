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
// Локальные port-интерфейсы (а не type-alias на `internal/repo.*`) — skill §6
// G.2-G.3: каждый use-case-пакет описывает только то, что РЕАЛЬНО использует.
// Адаптерами выступают существующие `internal/repo/network_interface_repo.go`,
// `internal/repo/subnet_repo.go`, `internal/repo/address_repo.go` — они уже
// реализуют соответствующие port-интерфейсы из `internal/repo`/`internal/service`,
// которые ⊇ локальным интерфейсам.
package networkinterface

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// Pagination — alias на единый value-объект `internal/repo`.
type Pagination = repo.Pagination

// NetworkInterfaceFilter — фильтр для List. Зеркалит `repo.NetworkInterfaceFilter`
// (репо реализует ports-shape; локальный alias избавляет от прокидывания
// ports-типа в use-case-package, но удерживает совместимость с repo-адаптером).
type NetworkInterfaceFilter = repo.NetworkInterfaceFilter

// NetworkInterfaceRepo — то, что use-case'ам NIC нужно от репозитория NIC.
//
// Все методы возвращают `*domain.NetworkInterfaceRecord` (skill evgeniy §4 D.1 /
// §7 H.1 — repo-entity несёт DB-managed CreatedAt). Insert/UpdateMeta принимают
// `*domain.NetworkInterface` (без CreatedAt). SetUsedBy — atomic CAS из миграции
// 0016 (KAC-52, ON CONFLICT DO UPDATE WHERE used_by_id=” OR used_by_id=$new),
// возвращает ErrFailedPrecondition при CAS-конфликте.
type NetworkInterfaceRepo interface {
	Get(ctx context.Context, id string) (*domain.NetworkInterfaceRecord, error)
	List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterfaceRecord, string, error)
	Insert(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterfaceRecord, error)
	UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterfaceRecord, error)
	// SetUsedBy атомарно выставляет/очищает denorm used_by-ссылку NIC (refID="" — очистка)
	// и публичный status. CAS-семантика на repo-уровне (миграция 0016, KAC-52).
	SetUsedBy(ctx context.Context, id, refType, refID, refName string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterfaceRecord, error)
	Delete(ctx context.Context, id string) error
}

// SubnetReader — узкий read-интерфейс для проверки parent Subnet в Create.
type SubnetReader interface {
	Get(ctx context.Context, id string) (*domain.SubnetRecord, error)
}

// AddressRepo — узкий интерфейс работы с Address-ресурсами, нужный NIC use-case'ам:
// валидация cross-resource (Address существует, нужной IP-версии, в той же подсети,
// не занят) + помечание used + referrer-tracking при attach/detach.
type AddressRepo interface {
	Get(ctx context.Context, id string) (*domain.AddressRecord, error)
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	ClearReference(ctx context.Context, addressID string) error
}

// FolderClient — то, что use-case'ам NIC нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
