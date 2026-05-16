package network

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetNetworkUseCase — простой read; единственная «логика» — id-валидация и
// перевод repo-sentinel в gRPC status. Skill evgeniy §2 B.3: use-case можно
// было бы вообще опустить, но handler-у удобнее единый шов через use-case'ы.
//
// Wave 5 pilot (KAC-94, skill evgeniy §6 G.5): открывает Reader-TX явно через
// `repo.Reader(ctx)` — routing на slave-реплику станет automatic, когда та
// появится; пока на той же мастер-pool.
type GetNetworkUseCase struct {
	repo Repo
}

// NewGetNetworkUseCase создаёт GetNetworkUseCase.
func NewGetNetworkUseCase(r Repo) *GetNetworkUseCase {
	return &GetNetworkUseCase{repo: r}
}

// Execute возвращает repo-entity Network. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetNetworkUseCase) Execute(ctx context.Context, id string) (*kachorepo.NetworkRecord, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	n, err := r.Networks().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return n, nil
}
