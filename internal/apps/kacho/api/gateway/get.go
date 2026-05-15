package gateway

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetGatewayUseCase — простой read; единственная «логика» — id-валидация и
// перевод repo-sentinel в gRPC status. Skill evgeniy §2 B.3: use-case можно
// было бы вообще опустить, но handler-у удобнее единый шов через use-case'ы.
type GetGatewayUseCase struct {
	repo GatewayRepo
}

// NewGetGatewayUseCase создаёт GetGatewayUseCase.
func NewGetGatewayUseCase(repo GatewayRepo) *GetGatewayUseCase {
	return &GetGatewayUseCase{repo: repo}
}

// Execute возвращает repo-entity Gateway. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetGatewayUseCase) Execute(ctx context.Context, id string) (*domain.GatewayRecord, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	g, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return g, nil
}
