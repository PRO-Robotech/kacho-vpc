package routetable

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetRouteTableUseCase — простой read.
type GetRouteTableUseCase struct {
	repo RouteTableRepo
}

// NewGetRouteTableUseCase создаёт GetRouteTableUseCase.
func NewGetRouteTableUseCase(repo RouteTableRepo) *GetRouteTableUseCase {
	return &GetRouteTableUseCase{repo: repo}
}

// Execute возвращает repo-entity RouteTable.
func (u *GetRouteTableUseCase) Execute(ctx context.Context, id string) (*domain.RouteTableRecord, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, id); err != nil {
		return nil, err
	}
	rt, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return rt, nil
}
