package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetAddressPoolUseCase — sync read AddressPool по id. Wave 5 A.7 sub-PR 1/6:
// открывает Reader-TX kacho.Repository (G.4: чтение уходит на slave-pool если
// настроен; иначе master fallback).
type GetAddressPoolUseCase struct {
	repo Repo
}

// NewGetAddressPoolUseCase собирает use-case.
func NewGetAddressPoolUseCase(r Repo) *GetAddressPoolUseCase {
	return &GetAddressPoolUseCase{repo: r}
}

// Execute возвращает AddressPool по id. ErrNotFound если не существует.
func (u *GetAddressPoolUseCase) Execute(ctx context.Context, id string) (*domain.AddressPool, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	rec, err := rd.AddressPools().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := rec.AddressPool
	return &out, nil
}
