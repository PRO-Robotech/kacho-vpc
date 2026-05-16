package privateendpoint

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetPrivateEndpointUseCase — простой read через CQRS-Reader.
type GetPrivateEndpointUseCase struct {
	repo Repo
}

// NewGetPrivateEndpointUseCase создаёт GetPrivateEndpointUseCase.
func NewGetPrivateEndpointUseCase(r Repo) *GetPrivateEndpointUseCase {
	return &GetPrivateEndpointUseCase{repo: r}
}

// Execute возвращает repo-entity PrivateEndpoint. Wave 5 replicate (KAC-94):
// открывает read-only TX через `repo.Reader(ctx)` и закрывает её через
// `rd.Close()` (rollback read-only TX). Parity с Network/RT/SG.
func (u *GetPrivateEndpointUseCase) Execute(ctx context.Context, id string) (*kacho.PrivateEndpointRecord, error) {
	if err := corevalidate.ResourceID("private endpoint", ids.PrefixPrivateEndpoint, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	got, err := rd.PrivateEndpoints().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return got, nil
}
