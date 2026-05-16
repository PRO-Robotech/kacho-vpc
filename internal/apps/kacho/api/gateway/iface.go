// Package gateway — use-case-структура ресурса Gateway (skill evgeniy §2 B.1-B.4).
//
// Wave 3b (KAC-94): replicate Wave 3a pilot Network → Gateway. Бизнес-логика
// CreateGatewayUseCase / UpdateGatewayUseCase / DeleteGatewayUseCase /
// MoveGatewayUseCase / GetGatewayUseCase / ListGatewaysUseCase / ListOperationsUseCase
// плюс тонкий gRPC-handler. Раньше монолитный `internal/service/gateway.go`
// (GatewayService) был fat-service со всеми методами в одном файле.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): Gateway use-case'ы
// переезжают на CQRS-Repository (Reader / Writer split, parity с pilot Network
// и batch 33/34 SecurityGroup). Каждый mutating use-case открывает TX явно
// (`u.repo.Writer(ctx)`), эмитит outbox через `w.Outbox().Emit(...)` в той же
// TX, затем Commit — атомарность DML + outbox гарантирована (G.5).
package gateway

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination — пере-используем единый value-объект `internal/repo` (type-alias).
type (
	Pagination    = repo.Pagination
	GatewayFilter = kacho.GatewayFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.2 / G.5): use-case-слой
// открывает TX явно через `repo.Reader(ctx)` / `repo.Writer(ctx)` и видит
// разделение reader/writer в типе вызова — это закрывает «толстый сервис» (B.1)
// и фиксирует точку транзакции (G.5).
type (
	Repo               = kacho.Repository
	Reader             = kacho.RepositoryReader
	Writer             = kacho.RepositoryWriter
	GatewayReaderIface = kacho.GatewayReaderIface
	GatewayWriterIface = kacho.GatewayWriterIface
	OutboxEmitter      = kacho.OutboxEmitter
)

// FolderClient — то, что use-case'ам Gateway нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
