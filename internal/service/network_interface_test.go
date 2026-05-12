package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
)

// niRepoFake — минимальный in-memory NetworkInterfaceRepo для unit-теста internal-проекции.
type niRepoFake struct{ data map[string]*domain.NetworkInterface }

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
func (r *niRepoFake) ListByHypervisor(_ context.Context, hvID string) ([]*domain.NetworkInterface, error) {
	var out []*domain.NetworkInterface
	for _, n := range r.data {
		if n.Dataplane.HVID == hvID {
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
func (r *niRepoFake) SetInstance(_ context.Context, id, instanceID, niIndex string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterface, error) {
	n := r.data[id]
	n.InstanceID, n.Index, n.Status = instanceID, niIndex, st
	return n, nil
}
func (r *niRepoFake) SetDataplane(_ context.Context, id string, dp domain.NICDataplane, newStatus domain.NetworkInterfaceStatus, setStatus bool) (*domain.NetworkInterface, bool, error) {
	n := r.data[id]
	if dp.Revision < n.Dataplane.Revision {
		return n, false, nil
	}
	now := time.Now().UTC()
	dp.UpdatedAt = &now
	n.Dataplane = dp
	if setStatus {
		n.Status = newStatus
	}
	return n, true, nil
}
func (r *niRepoFake) Delete(_ context.Context, id string) error {
	if _, ok := r.data[id]; !ok {
		return ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func TestNetworkInterface_InternalProjectionAndDataplaneWriteback(t *testing.T) {
	ctx := context.Background()
	repo := newNIRepoFake()
	repo.data["nic-1"] = &domain.NetworkInterface{ID: "nic-1", FolderID: "f1", SubnetID: "s1", NetworkID: "n1",
		PrimaryV4Address: "10.0.0.5", Status: domain.NIStatusAvailable, CreatedAt: time.Now().UTC()}

	internal := NewNetworkInterfaceInternal(repo)

	// public projection — lean (no data-plane fields).
	pub := protoconv.NetworkInterface(repo.data["nic-1"])
	require.Equal(t, "10.0.0.5", pub.PrimaryV4Address)
	require.Equal(t, vpcv1.NetworkInterface_AVAILABLE, pub.Status)

	// write-back: ACTIVE -> internal fields filled, public status flips to ACTIVE.
	applied, err := internal.ReportNiDataplane(ctx, "nic-1", domain.NICDataplane{
		HVID: "hyp-a", SID: "fd00:ca01:0:0:00d4:7::", SIDSeq: 7, HostIface: "kh-7", Netns: "ns-7", GatewayIP: "169.254.1.7", ContainerID: "ctr7", Revision: 1,
	}, 2 /*NI_DATAPLANE_ACTIVE*/)
	require.NoError(t, err)
	require.True(t, applied)
	got, err := internal.Get(ctx, "nic-1")
	require.NoError(t, err)
	require.Equal(t, "hyp-a", got.Dataplane.HVID)
	require.Equal(t, uint32(7), got.Dataplane.SIDSeq)
	require.Equal(t, domain.NIStatusActive, got.Status)
	ipb := protoconv.InternalNetworkInterface(got)
	require.Equal(t, "hyp-a", ipb.HypervisorId)
	require.Equal(t, "kh-7", ipb.HostIface)
	require.NotNil(t, ipb.NetworkInterface)

	// stale revision -> ignored.
	applied, err = internal.ReportNiDataplane(ctx, "nic-1", domain.NICDataplane{HVID: "stale", Revision: 0}, 2)
	require.NoError(t, err)
	require.False(t, applied)
	got, _ = internal.Get(ctx, "nic-1")
	require.Equal(t, "hyp-a", got.Dataplane.HVID, "stale write-back ignored")

	// DELETED -> NIC removed.
	applied, err = internal.ReportNiDataplane(ctx, "nic-1", domain.NICDataplane{Revision: 2}, 4 /*NI_DATAPLANE_DELETED*/)
	require.NoError(t, err)
	require.True(t, applied)
	_, err = internal.Get(ctx, "nic-1")
	require.Equal(t, codes.NotFound, status.Code(err))
}
