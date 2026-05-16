package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// KAC-48 — UNIQUE-constraint на network_interfaces.mac_address. Два NIC с
// одинаковым MAC внутри облака → Insert возвращает helpers.ErrMacCollision
// (не helpers.ErrAlreadyExists), чтобы NetworkInterfaceService.doCreate мог
// отличить retry-able MAC-коллизию от nominal duplicate-name UNIQUE-нарушения.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.
func TestIntegration_NICRepo_MacAddressUniqueness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	defer r.Close()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-mac", Name: domain.RcNameVPC("net-mac")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-mac", Name: domain.RcNameVPC("sub-mac"), NetworkID: net.ID, ZoneID: "ru-central1-a", V4CidrBlocks: []string{"10.20.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	mkNIC := func(id, name, mac string) *domain.NetworkInterface {
		return &domain.NetworkInterface{
			ID: id, FolderID: "folder-mac",
			Name: domain.RcNameVPC(name), SubnetID: sub.ID, MAC: mac,
			Status: domain.NIStatusAvailable,
		}
	}

	// 1. Первый NIC с заданным MAC — OK.
	firstID := ids.NewID(ids.PrefixSubnet)
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		first, e := w.NetworkInterfaces().Insert(ctx, mkNIC(firstID, "nic-mac-1", "0e:aa:aa:aa:aa:aa"))
		if e == nil {
			require.Equal(t, "0e:aa:aa:aa:aa:aa", first.MAC, "Insert возвращает сохранённый MAC")
		}
		return e
	}), "первый Insert с MAC 0e:aa:aa:aa:aa:aa должен пройти")

	// 2. Второй NIC с тем же MAC — helpers.ErrMacCollision.
	err = withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().Insert(ctx, mkNIC(ids.NewID(ids.PrefixSubnet), "nic-mac-2", "0e:aa:aa:aa:aa:aa"))
		return e
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, helpers.ErrMacCollision),
		"при дубле MAC ожидаем helpers.ErrMacCollision, получили: %v", err)

	// 3. Третий NIC с другим MAC — OK.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().Insert(ctx, mkNIC(ids.NewID(ids.PrefixSubnet), "nic-mac-3", "0e:aa:aa:aa:aa:bb"))
		return e
	}), "Insert с другим MAC должен пройти")

	// 4. Verify Get возвращает сохранённый MAC обратно.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.NetworkInterfaces().Get(ctx, firstID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	require.Equal(t, "0e:aa:aa:aa:aa:aa", got.MAC, "Get возвращает MAC из БД")
}
