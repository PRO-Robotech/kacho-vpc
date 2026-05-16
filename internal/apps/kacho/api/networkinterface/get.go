package networkinterface

import (
	"context"

	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetNetworkInterfaceUseCase — простой read.
//
// Wave 5 replicate (KAC-94, NIC batch): открывает reader-TX через CQRS-iface
// (skill evgeniy §6 G.1-G.7). Reader идёт на тот же master-pool — когда появится
// slave-реплика, kacho.Repository.Reader будет роутить туда (G.4).
type GetNetworkInterfaceUseCase struct {
	repo Repo
}

// NewGetNetworkInterfaceUseCase создаёт GetNetworkInterfaceUseCase.
func NewGetNetworkInterfaceUseCase(r Repo) *GetNetworkInterfaceUseCase {
	return &GetNetworkInterfaceUseCase{repo: r}
}

// Execute возвращает repo-entity NIC.
func (u *GetNetworkInterfaceUseCase) Execute(ctx context.Context, id string) (*kachorepo.NetworkInterfaceRecord, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return got, nil
}
