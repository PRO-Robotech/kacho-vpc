package gateway

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetGatewayUseCase — простой read; единственная «логика» — id-валидация и
// перевод repo-sentinel в gRPC status. Skill evgeniy §2 B.3: use-case можно
// было бы вообще опустить, но handler-у удобнее единый шов через use-case'ы.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): открывает read-only TX через
// `repo.Reader(ctx)`. Закрытие читателя — defer `rd.Close()` (no-op rollback на
// read-only TX, освобождает соединение).
type GetGatewayUseCase struct {
	repo Repo
}

// NewGetGatewayUseCase создаёт GetGatewayUseCase.
func NewGetGatewayUseCase(r Repo) *GetGatewayUseCase {
	return &GetGatewayUseCase{repo: r}
}

// Execute возвращает repo-entity Gateway. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetGatewayUseCase) Execute(ctx context.Context, id string) (*kacho.GatewayRecord, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	g, err := rd.Gateways().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return g, nil
}
