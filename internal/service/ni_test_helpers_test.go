package service

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// niRepoFake — минимальный in-memory NetworkInterfaceRepo для unit-тестов.
// Раньше жил в network_interface_test.go (удалён в KAC-79/KAC-36 вместе с
// internal-проекцией NIC); вынесен сюда, потому что subnet_test.go использует
// его для Subnet.Delete precondition-проверок (RESTRICT FK NIC→Subnet).
type niRepoFake struct {
	data map[string]*domain.NetworkInterface
}

func newNIRepoFake() *niRepoFake { return &niRepoFake{data: map[string]*domain.NetworkInterface{}} }

func (r *niRepoFake) Get(_ context.Context, id string) (*domain.NetworkInterface, error) {
	n, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (r *niRepoFake) List(context.Context, NetworkInterfaceFilter, Pagination) ([]*domain.NetworkInterface, string, error) {
	return nil, "", nil
}

func (r *niRepoFake) ListBySubnet(_ context.Context, subnetID string) ([]*domain.NetworkInterface, error) {
	var out []*domain.NetworkInterface
	for _, n := range r.data {
		if n.SubnetID == subnetID {
			cp := *n
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *niRepoFake) Insert(_ context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error) {
	r.data[n.ID] = n
	return n, nil
}

func (r *niRepoFake) UpdateMeta(_ context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error) {
	r.data[n.ID] = n
	return n, nil
}

func (r *niRepoFake) SetUsedBy(_ context.Context, id, refType, refID, refName string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterface, error) {
	n := r.data[id]
	if refID == "" {
		refType, refName = "", ""
	}
	n.UsedByType, n.UsedByID, n.UsedByName, n.Status = refType, refID, refName, st
	return n, nil
}

func (r *niRepoFake) Delete(_ context.Context, id string) error {
	if _, ok := r.data[id]; !ok {
		return ErrNotFound
	}
	delete(r.data, id)
	return nil
}
