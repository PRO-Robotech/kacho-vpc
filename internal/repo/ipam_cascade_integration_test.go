package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// stubFolderClient maps folder_id -> cloud_id for the IPAM cascade step-3
// (folder -> cloud -> cloud_pool_selector). Returns "" (no cloud) for unknown
// folders, which makes the cascade skip the label-selector step.
type stubFolderClient struct {
	clouds map[string]string
}

func (s stubFolderClient) Exists(_ context.Context, _ string) (bool, error) { return true, nil }
func (s stubFolderClient) GetCloudID(_ context.Context, folderID string) (string, error) {
	return s.clouds[folderID], nil
}

// TestIntegration_IPAM_Cascade_FiveSteps wires real pgxpool + real repos against
// the testcontainers Postgres and a stub FolderClient, then drives the 5-step
// AddressPool resolve cascade end-to-end:
//
//	step 1 address_override -> step 2 network_default -> step 3 cloud-label-selector
//	-> step 4 zone_default -> step 5 global_default
//
// plus the inverse-containment edge (cloud selector has an extra label not in
// any pool -> falls through to zone_default, NOT the special pool).
func TestIntegration_IPAM_Cascade_FiveSteps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Geography (Region/Zone) переехала в kacho-compute (эпик KAC-15): в схеме
	// kacho_vpc больше нет таблиц regions/zones — zone_id хранится как обычная
	// строка без FK; существование зоны валидируется на request-path вызовом
	// compute.v1.ZoneService.Get (см. internal/clients/compute_client.go).
	poolRepo := repo.NewAddressPoolRepo(pool)
	bindRepo := repo.NewAddressPoolBindingRepo(pool)
	cloudSelRepo := repo.NewCloudPoolSelectorRepo(pool)
	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	addrRepo := repo.NewAddressRepo(pool)

	const zone = "ru-central1-a"

	now := time.Now().UTC()
	mkPool := func(name, zoneID string, isDefault bool, selector map[string]string, cidr string) *domain.AddressPool {
		// KAC-71: cidr_blocks split — все тестовые pool'ы — v4-only (/24 v4 префиксы),
		// поэтому кладём в V4CIDRBlocks.
		p := &domain.AddressPool{
			ID:             ids.NewID("apl"),
			Name:           name,
			V4CIDRBlocks:   []string{cidr},
			Kind:           domain.AddressPoolKindExternalPublic,
			ZoneID:         zoneID,
			IsDefault:      isDefault,
			SelectorLabels: selector,
			CreatedAt:      now,
			ModifiedAt:     now,
		}
		out, e := poolRepo.Insert(ctx, p)
		require.NoError(t, e)
		return out
	}

	// 5 pools, one per cascade level (all kind=EXTERNAL_PUBLIC).
	globalPool := mkPool("global-default", "", true, nil, "198.18.0.0/24")                                         // step 5
	zonePool := mkPool("zone-default", zone, true, nil, "198.18.1.0/24")                                           // step 4
	selectorPool := mkPool("premium-selector", zone, false, map[string]string{"tier": "premium"}, "198.18.2.0/24") // step 3
	networkBindingPool := mkPool("network-bound", zone, false, nil, "198.18.3.0/24")                               // step 2
	overridePool := mkPool("address-override", zone, false, nil, "198.18.4.0/24")                                  // step 1

	// AllocateExternalIP теперь использует address_pool_free_ips (миграция 0014).
	// Pool'ы созданы напрямую через poolRepo.Insert (не через
	// InternalAddressPoolService.Create, который сам зовёт PopulateFreelistForPool),
	// поэтому материализуем freelist руками — иначе Allocate-ассерты ниже упали бы
	// в FailedPrecondition "address pool exhausted".
	for _, p := range []*domain.AddressPool{globalPool, zonePool, selectorPool, networkBindingPool, overridePool} {
		require.NoError(t, poolRepo.PopulateFreelistForPool(ctx, p.ID))
	}

	// Network + subnet for the internal-address (step-2) path.
	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-step2", Name: domain.RcNameVPC("net-step2")}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-step2", CreatedAt: now,
		Name: "sub-step2", NetworkID: net.ID, ZoneID: zone, V4CidrBlocks: []string{"10.10.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)
	require.NoError(t, bindRepo.SetNetworkDefault(ctx, net.ID, networkBindingPool.ID))

	folderClient := stubFolderClient{clouds: map[string]string{
		"folder-step1": "cloud-step1",
		"folder-step3": "cloud-step3", // has a matching selector
		"folder-edge":  "cloud-edge",  // selector with an extra label not in any pool
		// folder-step2 / folder-step4 / folder-step5 -> no cloud => step 3 skipped
	}}
	require.NoError(t, cloudSelRepo.Set(ctx, "cloud-step3", map[string]string{"tier": "premium"}, "admin@test"))
	require.NoError(t, cloudSelRepo.Set(ctx, "cloud-edge", map[string]string{"tier": "premium", "customer": "acme"}, "admin@test"))

	apSvc := service.NewAddressPoolService(poolRepo, bindRepo, cloudSelRepo, addrRepo, netRepo, subnetRepo, folderClient, nil) // zoneReg=nil → zone-existence-check пропускается (тест не про неё)
	addrSvc := service.NewAddressService(addrRepo, subnetRepo, folderClient, nil, apSvc)

	// --- address fixtures ---

	// step 1: external address with an explicit address-override binding.
	a1 := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-step1", CreatedAt: now, Name: "a-step1",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	_, err = addrRepo.Insert(ctx, a1)
	require.NoError(t, err)
	require.NoError(t, bindRepo.SetAddressOverride(ctx, a1.ID, overridePool.ID))

	// step 2: internal address whose subnet's network has a network_default binding.
	a2 := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-step2", CreatedAt: now, Name: "a-step2",
		Type: domain.AddressTypeInternal, IpVersion: domain.IpVersionIPv4,
		InternalIpv4: &domain.InternalIpv4Spec{SubnetID: sub.ID},
	}
	_, err = addrRepo.Insert(ctx, a2)
	require.NoError(t, err)

	// step 3: external address whose folder->cloud has a selector matching selectorPool.
	a3 := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-step3", CreatedAt: now, Name: "a-step3",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	_, err = addrRepo.Insert(ctx, a3)
	require.NoError(t, err)

	// step 4: external address with a zone but no override / binding / matching cloud selector.
	a4 := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-step4", CreatedAt: now, Name: "a-step4",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	_, err = addrRepo.Insert(ctx, a4)
	require.NoError(t, err)

	// step 5: external address with NO zone -> only the global default applies.
	a5 := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-step5", CreatedAt: now, Name: "a-step5",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: ""},
	}
	_, err = addrRepo.Insert(ctx, a5)
	require.NoError(t, err)

	// edge: external address whose folder->cloud selector has an extra label
	// ({tier:premium, customer:acme}) not present in selectorPool's
	// selector_labels ({tier:premium}) -> inverse-containment fails ->
	// cascade falls through to zone_default, NOT the special pool.
	aEdge := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-edge", CreatedAt: now, Name: "a-edge",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	_, err = addrRepo.Insert(ctx, aEdge)
	require.NoError(t, err)

	// --- assertions: ResolvePoolForAddress picks the expected pool / MatchedVia ---

	cases := []struct {
		name       string
		addressID  string
		wantPoolID string
		wantVia    string
	}{
		{"step1_address_override", a1.ID, overridePool.ID, "address_override"},
		{"step2_network_default", a2.ID, networkBindingPool.ID, "network_default"},
		{"step3_cloud_label_selector", a3.ID, selectorPool.ID, "label_selector"},
		{"step4_zone_default", a4.ID, zonePool.ID, "zone_default"},
		{"step5_global_default", a5.ID, globalPool.ID, "global_default"},
		{"edge_inverse_containment_falls_to_zone_default", aEdge.ID, zonePool.ID, "zone_default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, rerr := apSvc.ResolvePoolForAddress(ctx, tc.addressID)
			require.NoError(t, rerr)
			require.NotNil(t, res)
			assert.Equal(t, tc.wantPoolID, res.Pool.ID, "wrong pool resolved")
			assert.Equal(t, tc.wantVia, res.MatchedVia, "wrong cascade step matched")
		})
	}

	// --- and AllocateExternalIP actually allocates an IP from the resolved pool ---
	for _, tc := range []struct {
		name       string
		addressID  string
		wantPoolID string
	}{
		{"allocate_step1", a1.ID, overridePool.ID},
		{"allocate_step3", a3.ID, selectorPool.ID},
		{"allocate_step4", a4.ID, zonePool.ID},
		{"allocate_step5", a5.ID, globalPool.ID},
		{"allocate_edge", aEdge.ID, zonePool.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, aerr := addrSvc.AllocateExternalIP(ctx, tc.addressID)
			require.NoError(t, aerr)
			require.NotNil(t, res)
			assert.NotEmpty(t, res.IP, "an IP must be allocated")
			assert.Equal(t, tc.wantPoolID, res.PoolID, "IP must come from the cascade-resolved pool")
			// Idempotency: a second call returns the same IP, AlreadyAllocated=true.
			res2, aerr2 := addrSvc.AllocateExternalIP(ctx, tc.addressID)
			require.NoError(t, aerr2)
			assert.Equal(t, res.IP, res2.IP)
			assert.True(t, res2.AlreadyAllocated)
		})
	}
}
