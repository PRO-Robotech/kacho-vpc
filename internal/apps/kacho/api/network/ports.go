// Package network — use-case-структура ресурса Network (skill evgeniy §2 B.1-B.4).
//
// Wave 3a (KAC-94): здесь живёт «бизнес-логика» Network — CreateNetworkUseCase,
// UpdateNetworkUseCase, DeleteNetworkUseCase, MoveNetworkUseCase плюс тонкий
// gRPC-handler.
//
// Wave 5 pilot (KAC-94, skill evgeniy §6 G.1-G.7): Network — pilot для
// CQRS-Repository pattern. Use-case'ы Network теперь работают через
// `kacho.Repository` (Reader / Writer), а не напрямую через узкий `NetworkRepo`.
// Каждый use-case открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`),
// и outbox-emit лежит в той же tx writer'а — атомарность DML + outbox
// гарантирована (G.5).
//
// Остальные 7 ресурсов (Subnet/Address/RouteTable/SG/Gateway/PE/NIC) пока
// работают через legacy `*repo.NetworkRepo` etc. — это replicate-фаза эпика
// KAC-94, идёт отдельно после merge'а pilot'а.
package network

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/ports`
// (alias'ы, не копии). Иначе пришлось бы дублировать структуры или гонять между
// пакетами через двойную конверсию.
type (
	Pagination          = ports.Pagination
	NetworkFilter       = ports.NetworkFilter
	SubnetFilter        = ports.SubnetFilter
	RouteTableFilter    = ports.RouteTableFilter
	SecurityGroupFilter = ports.SecurityGroupFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
type (
	Repo               = kacho.Repository
	Reader             = kacho.RepositoryReader
	Writer             = kacho.RepositoryWriter
	NetworkReaderIface = kacho.NetworkReaderIface
	NetworkWriterIface = kacho.NetworkWriterIface
	OutboxEmitter      = kacho.OutboxEmitter
)

// SubnetReader — узкое чтение Subnet, нужное для ListSubnets / checkNetworkEmpty.
type SubnetReader interface {
	List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.SubnetRecord, string, error)
}

// RouteTableReader — узкое чтение RouteTable, нужное для ListRouteTables /
// checkNetworkEmpty.
type RouteTableReader interface {
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*domain.RouteTableRecord, string, error)
}

// SecurityGroupRepo — то, что use-case'ам Network нужно от репозитория SG: List
// (для checkNetworkEmpty / ListSecurityGroups), Insert (для inline default-SG),
// Delete (для cleanup default-SG при Network.Delete).
type SecurityGroupRepo interface {
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroupRecord, string, error)
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error)
	Delete(ctx context.Context, id string) error
}

// FolderClient — то, что use-case'ам Network нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а на request-path /
// в worker'е.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
