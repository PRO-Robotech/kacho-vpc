// Package privateendpoint — use-case-структура ресурса PrivateEndpoint
// (skill evgeniy §2 B.1-B.4).
//
// Wave 3b (KAC-94): replicate Wave 3a pilot Network → PrivateEndpoint.
// Бизнес-логика CreatePrivateEndpointUseCase / UpdatePrivateEndpointUseCase /
// DeletePrivateEndpointUseCase / GetPrivateEndpointUseCase / ListPrivateEndpointsUseCase /
// ListOperationsUseCase плюс тонкий gRPC-handler.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): PrivateEndpoint переехал
// на CQRS-Repository (Reader / Writer split) — parity с Network/SG/Address/RT.
// Каждый use-case открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`),
// и outbox-emit лежит в той же tx writer'а — атомарность DML + outbox
// гарантирована (G.5).
//
// NB: у PrivateEndpoint нет Move RPC (он folder-level, но в YC verbatim API
// нет MovePrivateEndpoint). При появлении — добавить move.go.
package privateendpoint

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, PrivateEndpointFilter — пере-используем единые value-объекты `internal/repo`.
// Wave 5 replicate (KAC-94): pagination/filter уже type-alias'нуты в `repo.iface.go`
// на kachorepo-leaf аналоги (parity с Network).
type (
	Pagination            = repo.Pagination
	PrivateEndpointFilter = repo.PrivateEndpointFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): parity с network/ports.go.
type (
	Repo                       = kacho.Repository
	Reader                     = kacho.RepositoryReader
	Writer                     = kacho.RepositoryWriter
	PrivateEndpointReaderIface = kacho.PrivateEndpointReaderIface
	PrivateEndpointWriterIface = kacho.PrivateEndpointWriterIface
	OutboxEmitter              = kacho.OutboxEmitter
)

// NetworkReader — узкий read-интерфейс для проверки parent Network. Реализуется
// CQRS-`Repo.Reader().Networks()` или legacy `*repo.NetworkRepo` (через тестовый
// mock).
type NetworkReader interface {
	Get(ctx context.Context, id string) (*kacho.NetworkRecord, error)
}

// SubnetReader — узкий read-интерфейс для проверки parent Subnet. Subnet
// optional на PrivateEndpoint (oneof AddressSpec.InternalIpv4AddressSpec).
type SubnetReader interface {
	Get(ctx context.Context, id string) (*domain.SubnetRecord, error)
}

// FolderClient — peer-сервис kacho-resource-manager.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
