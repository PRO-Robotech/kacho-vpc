package networkinterface

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetNetworkInterfaceUseCase — простой read.
type GetNetworkInterfaceUseCase struct {
	repo NetworkInterfaceRepo
}

// NewGetNetworkInterfaceUseCase создаёт GetNetworkInterfaceUseCase.
func NewGetNetworkInterfaceUseCase(repo NetworkInterfaceRepo) *GetNetworkInterfaceUseCase {
	return &GetNetworkInterfaceUseCase{repo: repo}
}

// Execute возвращает repo-entity NIC.
func (u *GetNetworkInterfaceUseCase) Execute(ctx context.Context, id string) (*domain.NetworkInterfaceRecord, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	got, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return got, nil
}
