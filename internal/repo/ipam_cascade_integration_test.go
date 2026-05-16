package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	addressapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// stubFolderClient maps folder_id -> cloud_id for IPAM cascade step-3.
type stubFolderClient struct {
	clouds map[string]string
}

func (s stubFolderClient) Exists(_ context.Context, _ string) (bool, error) { return true, nil }
func (s stubFolderClient) GetCloudID(_ context.Context, folderID string) (string, error) {
	return s.clouds[folderID], nil
}

// TestIntegration_IPAM_Cascade_FiveSteps wires real pgxpool + CQRS repo + a stub
// FolderClient, then drives the 5-step AddressPool resolve cascade end-to-end.
//
// KAC-94 A.7 sub-PR 5/6: переписан полностью на CQRS Writer/Reader (раньше
// часть setup'а шла через legacy *_repo.go).
func TestIntegration_IPAM_Cascade_FiveSteps(t *testing.T) {
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

	const zone = "ru-central1-a"

	now := time.Now().UTC()
	mkPool := func(name, zoneID string, isDefault bool, selector map[string]string, cidr string) *domain.AddressPool {
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
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.AddressPools().Insert(ctx, p)
			return e
		}))
		return p
	}

	globalPool := mkPool("global-default", "", true, nil, "198.18.0.0/24")
	zonePool := mkPool("zone-default", zone, true, nil, "198.18.1.0/24")
	selectorPool := mkPool("premium-selector", zone, false, map[string]string{"tier": "premium"}, "198.18.2.0/24")
	networkBindingPool := mkPool("network-bound", zone, false, nil, "198.18.3.0/24")
	overridePool := mkPool("address-override", zone, false, nil, "198.18.4.0/24")

	for _, p := range []*domain.AddressPool{globalPool, zonePool, selectorPool, networkBindingPool, overridePool} {
		pID := p.ID
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			return w.AddressPools().PopulateFreelistForPool(ctx, pID)
		}))
	}

	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-step2", Name: domain.RcNameVPC("net-step2")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-step2",
		Name: domain.RcNameVPC("sub-step2"), NetworkID: net.ID, ZoneID: zone, V4CidrBlocks: []string{"10.10.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.AddressPoolBindings().SetNetworkDefault(ctx, net.ID, networkBindingPool.ID)
	}))

	folderClient := stubFolderClient{clouds: map[string]string{
		"folder-step1": "cloud-step1",
		"folder-step3": "cloud-step3",
		"folder-edge":  "cloud-edge",
	}}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.CloudPoolSelectors().Set(ctx, "cloud-step3", map[string]string{"tier": "premium"}, "admin@test")
	}))
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.CloudPoolSelectors().Set(ctx, "cloud-edge", map[string]string{"tier": "premium", "customer": "acme"}, "admin@test")
	}))

	// ResolverService + AllocateUseCase. Они принимают `kacho.Repository` напрямую.
	// Legacy подсетный/адресный сервис-параметры (subnetRepo/addrRepo) — пока
	// нужны для constructor'а; передаём legacy *Repo (они только-чтение через
	// shim_kacho и инициируются от того же pool'а).
	subnetRepo := repo.NewSubnetRepo(pool)
	addrRepo := repo.NewAddressRepo(pool)
	apResolver := addresspool.NewResolverService(r, addrRepo, subnetRepo, folderClient)
	addrSvc := addressapp.NewAllocateUseCase(r, subnetRepo, apResolver)

	mkAddr := func(folderID, name string, t domain.AddressType, v domain.IpVersion, ext *domain.ExternalIpv4Spec, intSpec *domain.InternalIpv4Spec) *domain.Address {
		return &domain.Address{
			ID: ids.NewID(ids.PrefixAddress), FolderID: folderID, Name: domain.RcNameVPC(name),
			Type: t, IpVersion: v, ExternalIpv4: ext, InternalIpv4: intSpec,
		}
	}

	insertAddr := func(a *domain.Address) {
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().Insert(ctx, a)
			return e
		}))
	}

	a1 := mkAddr("folder-step1", "a-step1", domain.AddressTypeExternal, domain.IpVersionIPv4, &domain.ExternalIpv4Spec{ZoneID: zone}, nil)
	insertAddr(a1)
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.AddressPoolBindings().SetAddressOverride(ctx, a1.ID, overridePool.ID)
	}))

	a2 := mkAddr("folder-step2", "a-step2", domain.AddressTypeInternal, domain.IpVersionIPv4, nil, &domain.InternalIpv4Spec{SubnetID: sub.ID})
	insertAddr(a2)

	a3 := mkAddr("folder-step3", "a-step3", domain.AddressTypeExternal, domain.IpVersionIPv4, &domain.ExternalIpv4Spec{ZoneID: zone}, nil)
	insertAddr(a3)

	a4 := mkAddr("folder-step4", "a-step4", domain.AddressTypeExternal, domain.IpVersionIPv4, &domain.ExternalIpv4Spec{ZoneID: zone}, nil)
	insertAddr(a4)

	a5 := mkAddr("folder-step5", "a-step5", domain.AddressTypeExternal, domain.IpVersionIPv4, &domain.ExternalIpv4Spec{ZoneID: ""}, nil)
	insertAddr(a5)

	aEdge := mkAddr("folder-edge", "a-edge", domain.AddressTypeExternal, domain.IpVersionIPv4, &domain.ExternalIpv4Spec{ZoneID: zone}, nil)
	insertAddr(aEdge)

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
			res, rerr := apResolver.ResolvePoolForAddress(ctx, tc.addressID)
			require.NoError(t, rerr)
			require.NotNil(t, res)
			assert.Equal(t, tc.wantPoolID, res.Pool.ID, "wrong pool resolved")
			assert.Equal(t, tc.wantVia, res.MatchedVia, "wrong cascade step matched")
		})
	}

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
			res2, aerr2 := addrSvc.AllocateExternalIP(ctx, tc.addressID)
			require.NoError(t, aerr2)
			assert.Equal(t, res.IP, res2.IP)
			assert.True(t, res2.AlreadyAllocated)
		})
	}
}
