package subnet

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetSubnetUseCase — простой read; единственная «логика» — id-валидация и
// перевод repo-sentinel в gRPC status.
type GetSubnetUseCase struct {
	repo SubnetRepo
}

// NewGetSubnetUseCase создаёт GetSubnetUseCase.
func NewGetSubnetUseCase(repo SubnetRepo) *GetSubnetUseCase {
	return &GetSubnetUseCase{repo: repo}
}

// Execute возвращает repo-entity Subnet. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetSubnetUseCase) Execute(ctx context.Context, id string) (*domain.SubnetRecord, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	s, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return s, nil
}
