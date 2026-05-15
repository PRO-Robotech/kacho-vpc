package privateendpoint

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetPrivateEndpointUseCase — простой read.
type GetPrivateEndpointUseCase struct {
	repo PrivateEndpointRepo
}

// NewGetPrivateEndpointUseCase создаёт GetPrivateEndpointUseCase.
func NewGetPrivateEndpointUseCase(repo PrivateEndpointRepo) *GetPrivateEndpointUseCase {
	return &GetPrivateEndpointUseCase{repo: repo}
}

// Execute возвращает repo-entity PrivateEndpoint.
func (u *GetPrivateEndpointUseCase) Execute(ctx context.Context, id string) (*domain.PrivateEndpointRecord, error) {
	if err := corevalidate.ResourceID("private endpoint", ids.PrefixPrivateEndpoint, id); err != nil {
		return nil, err
	}
	got, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return got, nil
}
