package subnet

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetSubnetUseCase — простой read; единственная «логика» — id-валидация и
// перевод repo-sentinel в gRPC status.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): открывает Reader-TX явно
// через `repo.Reader(ctx)` — routing на slave-реплику станет automatic, когда
// та появится; пока на той же мастер-pool.
type GetSubnetUseCase struct {
	repo Repo
}

// NewGetSubnetUseCase создаёт GetSubnetUseCase.
func NewGetSubnetUseCase(r Repo) *GetSubnetUseCase {
	return &GetSubnetUseCase{repo: r}
}

// Execute возвращает repo-entity Subnet. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetSubnetUseCase) Execute(ctx context.Context, id string) (*kachorepo.SubnetRecord, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	s, err := r.Subnets().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return s, nil
}
