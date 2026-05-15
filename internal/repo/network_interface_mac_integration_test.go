package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// KAC-48 — UNIQUE-constraint на network_interfaces.mac_address. Два NIC с
// одинаковым MAC внутри облака → repo.Insert возвращает service.ErrMacCollision
// (не ErrAlreadyExists), чтобы NetworkInterfaceService.doCreate мог отличить
// retry-able MAC-коллизию от nominal duplicate-name UNIQUE-нарушения.
func TestIntegration_NICRepo_MacAddressUniqueness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	nicRepo := repo.NewNetworkInterfaceRepo(pool)

	now := time.Now().UTC().Truncate(time.Microsecond)

	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-mac", Name: domain.RcNameVPC("net-mac")}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-mac", CreatedAt: now,
		Name: "sub-mac", NetworkID: net.ID, ZoneID: "ru-central1-a", V4CidrBlocks: []string{"10.20.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)

	mkNIC := func(id, name, mac string) *domain.NetworkInterface {
		return &domain.NetworkInterface{
			ID: id, FolderID: "folder-mac", CreatedAt: now,
			Name: name, SubnetID: sub.ID, MAC: mac,
			Status: domain.NIStatusAvailable,
		}
	}

	// 1. Первый NIC с заданным MAC — OK.
	first, err := nicRepo.Insert(ctx, mkNIC(ids.NewID(ids.PrefixSubnet), "nic-mac-1", "0e:aa:aa:aa:aa:aa"))
	require.NoError(t, err, "первый Insert с MAC 0e:aa:aa:aa:aa:aa должен пройти")
	require.Equal(t, "0e:aa:aa:aa:aa:aa", first.MAC, "Insert возвращает сохранённый MAC")

	// 2. Второй NIC с тем же MAC, но другим именем (имя не должно triger'ить
	//    name-UNIQUE и подменять причину ошибки) — ErrMacCollision.
	_, err = nicRepo.Insert(ctx, mkNIC(ids.NewID(ids.PrefixSubnet), "nic-mac-2", "0e:aa:aa:aa:aa:aa"))
	require.Error(t, err)
	require.True(t, errors.Is(err, service.ErrMacCollision),
		"при дубле MAC ожидаем service.ErrMacCollision, получили: %v", err)

	// 3. Третий NIC с другим MAC — OK (constraint бьёт только по дублям).
	_, err = nicRepo.Insert(ctx, mkNIC(ids.NewID(ids.PrefixSubnet), "nic-mac-3", "0e:aa:aa:aa:aa:bb"))
	require.NoError(t, err, "Insert с другим MAC должен пройти")

	// 4. Verify Get возвращает сохранённый MAC обратно.
	got, err := nicRepo.Get(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "0e:aa:aa:aa:aa:aa", got.MAC, "Get возвращает MAC из БД")
}
