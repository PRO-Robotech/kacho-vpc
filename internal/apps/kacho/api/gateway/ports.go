// Package gateway — use-case-структура ресурса Gateway (skill evgeniy §2 B.1-B.4).
//
// Wave 3b (KAC-94): replicate Wave 3a pilot Network → Gateway. Бизнес-логика
// CreateGatewayUseCase / UpdateGatewayUseCase / DeleteGatewayUseCase /
// MoveGatewayUseCase / GetGatewayUseCase / ListGatewaysUseCase / ListOperationsUseCase
// плюс тонкий gRPC-handler. Раньше монолитный `internal/service/gateway.go`
// (GatewayService) был fat-service со всеми методами в одном файле.
//
// Локальные port-интерфейсы (а не type-alias на `internal/ports.GatewayRepo`)
// — skill §6 G.2-G.3: каждый use-case-пакет описывает только то, что РЕАЛЬНО
// использует. Адаптерами выступают существующие `internal/repo/gateway_repo.go`
// и `internal/ports/portmock` — они уже реализуют `internal/ports.GatewayRepo`,
// который ⊇ локальному интерфейсу, поэтому Go-типизация работает без shim'ов.
package gateway

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// Pagination, GatewayFilter — пере-используем единые value-объекты `internal/ports`
// (alias'ы, не копии).
type (
	Pagination    = ports.Pagination
	GatewayFilter = ports.GatewayFilter
)

// GatewayRepo — то, что use-case'ам Gateway нужно от репозитория шлюзов.
//
// Все методы возвращают `*domain.GatewayRecord` (skill evgeniy §4 D.1 / §7 H.1 —
// repo-entity несёт DB-managed CreatedAt). Insert/Update принимают `*domain.Gateway`
// (без CreatedAt).
type GatewayRepo interface {
	Get(ctx context.Context, id string) (*domain.GatewayRecord, error)
	List(ctx context.Context, f GatewayFilter, p Pagination) ([]*domain.GatewayRecord, string, error)
	Insert(ctx context.Context, g *domain.Gateway) (*domain.GatewayRecord, error)
	Update(ctx context.Context, g *domain.Gateway) (*domain.GatewayRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.GatewayRecord, error)
}

// FolderClient — то, что use-case'ам Gateway нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
