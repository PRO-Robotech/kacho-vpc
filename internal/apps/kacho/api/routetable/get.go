package routetable

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetRouteTableUseCase — простой read через CQRS Reader.
//
// Wave 5 replicate (KAC-94): возвращает `*kacho.RouteTableRecord` вместо
// `*domain.RouteTableRecord` (запись переехала в repo-leaf, см. §4 D.1).
type GetRouteTableUseCase struct {
	repo Repo
}

// NewGetRouteTableUseCase создаёт GetRouteTableUseCase.
func NewGetRouteTableUseCase(r Repo) *GetRouteTableUseCase {
	return &GetRouteTableUseCase{repo: r}
}

// Execute возвращает repo-entity RouteTable.
func (u *GetRouteTableUseCase) Execute(ctx context.Context, id string) (*kacho.RouteTableRecord, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	rt, gerr := rd.RouteTables().Get(ctx, id)
	if gerr != nil {
		return nil, mapRepoErr(gerr)
	}
	return rt, nil
}
