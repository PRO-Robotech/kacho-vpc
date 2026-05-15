// KAC-71: TDD red-phase service-level тесты для IPAM cascade family-skip
// после split AddressPool.cidr_blocks → v4_cidr_blocks + v6_cidr_blocks.
//
// Acceptance: docs/specs/sub-phase-1.x-addresspool-split-cidr-family-acceptance.md
//
// Покрытие — Group D (REQ-RESOLVE-01..07):
//   - D1: v4-only pool не резолвится для v6-allocate (Step 4/5 fall-through)
//   - D2: v6-only pool не резолвится для v4-allocate (Step 4/5 fall-through)
//   - D3: dual-stack pool резолвится для обоих family (Step 4 zone_default)
//   - D4: ExplainResolution на fall-through отдаёт matched_via="none" +
//     selected_pool пуст (через service-level ErrPoolNotResolved handling)
//   - D5: Step 3 label-selector — pool без нужной family пропускается;
//     cascade выбирает следующий matching-family pool
//   - D6: Step 1 per-address override family-skip
//   - D7: Step 2 per-network default family-skip
//
// Все тесты — failing на текущей реализации (cascade использует
// poolHasFamily через runtime-парсинг strings.Contains(":"); domain pool
// пока не имеет V4CIDRBlocks/V6CIDRBlocks полей). После rpc-implementer
// KAC-74 — позеленеют (poolHasFamily станет len(pool.V4CIDRBlocks)>0 /
// len(pool.V6CIDRBlocks)>0).
package addresspool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
)

// makeCascadeFixture — собирает полный набор моков + 1 Network (для Step 2
// тестов) + ZoneRegistry. Возвращает service + ссылки на repos для seed'инга.
type cascadeFixture struct {
	svc      *AddressPoolService
	poolRepo *stubAddressPoolRepo
	bindings *stubBindingRepo
	addrRepo *portmock.AddressRepo
	netRepo  *portmock.NetworkRepo
	subRepo  *portmock.SubnetRepo
	cloudSel *stubCloudSelRepo
}

func newCascadeFixture(_ *testing.T) *cascadeFixture {
	r := newStubAddressPoolRepo()
	br := newStubBindingRepo()
	cs := newStubCloudSelRepo()
	ar := portmock.NewAddressRepo()
	sr := portmock.NewSubnetRepo()
	nr := portmock.NewNetworkRepo()
	zr := portmock.NewZoneRegistry("ru-central1-c", "ru-central1-a")
	svc := NewAddressPoolService(r, br, cs, ar, nr, sr, &portmock.FolderClient{OK: true}, zr)
	return &cascadeFixture{
		svc: svc, poolRepo: r, bindings: br,
		addrRepo: ar, netRepo: nr, subRepo: sr, cloudSel: cs,
	}
}

// seedPool — insert pool с явными V4/V6 CIDR-блоками (split-shape).
func (f *cascadeFixture) seedPool(t *testing.T, name string, isDefault bool, zone string, v4, v6 []string, selector map[string]string) *domain.AddressPool {
	t.Helper()
	now := time.Now().UTC()
	p := &domain.AddressPool{
		ID:             ids.NewID("apl"),
		Name:           name,
		V4CIDRBlocks:   v4,
		V6CIDRBlocks:   v6,
		Kind:           domain.AddressPoolKindExternalPublic,
		ZoneID:         zone,
		IsDefault:      isDefault,
		SelectorLabels: selector,
		CreatedAt:      now,
		ModifiedAt:     now,
	}
	out, err := f.poolRepo.Insert(context.Background(), p)
	require.NoError(t, err)
	return out
}

// seedAddressV4Req — Address с external_ipv4 spec (запрос на v4-аллокацию).
// Wave 2 batch A (KAC-94): возвращает *domain.AddressRecord (repo-entity).
func (f *cascadeFixture) seedAddressV4Req(t *testing.T, folder, zone string) *domain.AddressRecord {
	t.Helper()
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: folder,
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	rec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)
	return rec
}

// seedAddressV6Req — Address с external_ipv6 spec (запрос на v6-аллокацию).
func (f *cascadeFixture) seedAddressV6Req(t *testing.T, folder, zone string) *domain.AddressRecord {
	t.Helper()
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: folder,
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: zone},
	}
	rec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)
	return rec
}

// --------------------------------------------------------------------------
// D1: v4-only pool не резолвится для v6-allocate.
//
// Given: глобальный v4-only default pool, нет v6-pool в zone.
// When: ResolvePoolForAddressObjFamily(..., FamilyV6).
// Then: cascade fall-through до global, нигде v6-CIDR нет → ErrPoolNotResolved.
// --------------------------------------------------------------------------

func TestCascade_D1_V4OnlyPool_DoesNotResolveForV6(t *testing.T) {
	f := newCascadeFixture(t)

	// Глобальный default v4-only pool (zone="" → global).
	f.seedPool(t, "global-v4", true, "", []string{"203.0.113.0/24"}, nil, nil)

	a := f.seedAddressV6Req(t, "f-d1", "ru-central1-c")

	res, err := f.svc.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV6)
	require.Error(t, err, "v6-allocate must NOT pick v4-only pool")
	assert.True(t, errors.Is(err, ErrPoolNotResolved),
		"expected ErrPoolNotResolved, got %v", err)
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// D2: v6-only pool не резолвится для v4-allocate.
// --------------------------------------------------------------------------

func TestCascade_D2_V6OnlyPool_DoesNotResolveForV4(t *testing.T) {
	f := newCascadeFixture(t)
	f.seedPool(t, "global-v6", true, "", nil, []string{"2001:db8::/64"}, nil)

	a := f.seedAddressV4Req(t, "f-d2", "ru-central1-c")

	res, err := f.svc.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// D3: dual-stack pool — резолвится для обоих family (Step 4 zone_default).
// --------------------------------------------------------------------------

func TestCascade_D3_DualStackPool_ResolvesForBothFamilies(t *testing.T) {
	f := newCascadeFixture(t)
	dual := f.seedPool(t, "dual", true, "ru-central1-c",
		[]string{"198.51.100.0/24"}, []string{"2001:db8:ff::/64"}, nil)

	v4Addr := f.seedAddressV4Req(t, "f-d3", "ru-central1-c")
	v6Addr := f.seedAddressV6Req(t, "f-d3", "ru-central1-c")

	res, err := f.svc.ResolvePoolForAddressObjFamily(context.Background(), v4Addr, FamilyV4)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, dual.ID, res.Pool.ID)
	assert.Equal(t, "zone_default", res.MatchedVia)

	res2, err := f.svc.ResolvePoolForAddressObjFamily(context.Background(), v6Addr, FamilyV6)
	require.NoError(t, err)
	require.NotNil(t, res2)
	assert.Equal(t, dual.ID, res2.Pool.ID)
	assert.Equal(t, "zone_default", res2.MatchedVia)
}

// --------------------------------------------------------------------------
// D4: ExplainResolution на fall-through отдаёт matched_via="none".
//
// Handler-уровневая часть (HTTP 200 vs gRPC FailedPrecondition) — в
// handler-тестах. Service-уровень: ExplainResolution должен возвращать
// ErrPoolNotResolved для v6-запроса при v4-only pool — это контракт, по
// которому handler решает «вернуть OK + matched_via=none» (см. acceptance §10
// «handler change»). Здесь проверяем что service возвращает именно
// ErrPoolNotResolved (а не nil primary/runner-up без ошибки) — это
// необходимое условие для handler-логики.
// --------------------------------------------------------------------------

func TestCascade_D4_ExplainResolution_FallThrough_ReturnsErrPoolNotResolved(t *testing.T) {
	f := newCascadeFixture(t)
	f.seedPool(t, "v4-only", true, "ru-central1-c", []string{"203.0.113.0/24"}, nil, nil)

	a := f.seedAddressV6Req(t, "f-d4", "ru-central1-c")

	primary, runnerUp, err := f.svc.ExplainResolution(context.Background(), a.ID, "")
	require.Error(t, err, "ExplainResolution must return ErrPoolNotResolved when no family-match pool")
	assert.True(t, errors.Is(err, ErrPoolNotResolved),
		"sentinel must be ErrPoolNotResolved (handler converts to gRPC OK + matched_via=none)")
	assert.Nil(t, primary)
	assert.Nil(t, runnerUp)
}

// --------------------------------------------------------------------------
// D5: Step 3 (label-selector) — pool без нужной family пропускается;
// cascade выбирает global_default другой family.
//
// Setup:
//   - v4-only premium pool (selector={tier:premium}, zone=ru-central1-c).
//   - v6-only global default (zone="").
//   - Cloud selector {tier:premium} (через folderClient → cloud → cloudSel).
//   - Address с external_ipv6 spec в folder привязанном к этому cloud'у.
// Expect: cascade Step 3 находит premium-pool, family-фильтр пропускает (v6 пуст),
// fall-through до Step 5 → global_default v6.
// --------------------------------------------------------------------------

func TestCascade_D5_LabelSelector_FamilySkip(t *testing.T) {
	// Fixture с folderClient, у которого folder→cloud mapping.
	r := newStubAddressPoolRepo()
	br := newStubBindingRepo()
	cs := newStubCloudSelRepo()
	ar := portmock.NewAddressRepo()
	sr := portmock.NewSubnetRepo()
	nr := portmock.NewNetworkRepo()
	zr := portmock.NewZoneRegistry("ru-central1-c")

	// folderClient с reverse mapping folder-d5 → cloud-d5.
	fc := &portmock.FolderClient{OK: true}
	fc.CloudID = "cloud-d5"
	svc := NewAddressPoolService(r, br, cs, ar, nr, sr, fc, zr)

	now := time.Now().UTC()
	// Premium pool v4-only.
	premiumV4 := &domain.AddressPool{
		ID:             ids.NewID("apl"),
		Name:           "premium-v4",
		V4CIDRBlocks:   []string{"203.0.113.0/24"},
		Kind:           domain.AddressPoolKindExternalPublic,
		ZoneID:         "ru-central1-c",
		SelectorLabels: map[string]string{"tier": "premium"},
		CreatedAt:      now,
	}
	_, err := r.Insert(context.Background(), premiumV4)
	require.NoError(t, err)

	// Global default v6.
	globalV6 := &domain.AddressPool{
		ID:           ids.NewID("apl"),
		Name:         "global-v6",
		V6CIDRBlocks: []string{"2001:db8::/64"},
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "",
		IsDefault:    true,
		CreatedAt:    now,
	}
	_, err = r.Insert(context.Background(), globalV6)
	require.NoError(t, err)

	// CloudPoolSelector mapping cloud-d5 → {tier:premium}.
	require.NoError(t, cs.Set(context.Background(), "cloud-d5",
		map[string]string{"tier": "premium"}, "admin@test"))

	// Address — folder привязан к cloud-d5 (folderClient.CloudID="cloud-d5").
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-d5",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: "ru-central1-c"},
	}
	aRec, err := ar.Insert(context.Background(), a)
	require.NoError(t, err)
	_ = now // CreatedAt больше не в domain.Address (KAC-94, Wave 2 batch A); DB-managed.

	res, err := svc.ResolvePoolForAddressObjFamily(context.Background(), aRec, FamilyV6)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, globalV6.ID, res.Pool.ID,
		"v6-allocate must skip v4-only premium pool (Step 3) and fall through to global_default")
	assert.Equal(t, "global_default", res.MatchedVia)
}

// --------------------------------------------------------------------------
// D6: Step 1 — per-address override на pool не той family → family-фильтр
// пропускает, fall-through.
// --------------------------------------------------------------------------

func TestCascade_D6_AddressOverride_FamilySkip(t *testing.T) {
	f := newCascadeFixture(t)
	overrideV4 := f.seedPool(t, "override-v4", false, "ru-central1-c",
		[]string{"203.0.113.0/24"}, nil, nil)

	// Address с external_ipv6 spec (запрос на v6-allocate).
	a := f.seedAddressV6Req(t, "f-d6", "ru-central1-c")

	// Override → v4-only pool.
	require.NoError(t, f.bindings.SetAddressOverride(context.Background(),
		a.ID, overrideV4.ID))

	// Другого v6-pool нет → cascade проваливается до конца.
	res, err := f.svc.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV6)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// D7: Step 2 — per-network default pool не той family → family-фильтр
// пропускает, fall-through.
// --------------------------------------------------------------------------

func TestCascade_D7_NetworkDefault_FamilySkip(t *testing.T) {
	f := newCascadeFixture(t)
	netDefV6 := f.seedPool(t, "net-def-v6", false, "ru-central1-c",
		nil, []string{"2001:db8::/64"}, nil)

	// Network + Subnet — для internal v4 path (cascade Step 2).
	now := time.Now().UTC()
	netID := ids.NewID(ids.PrefixNetwork)
	_ = now // CreatedAt больше не в domain.Network (KAC-99/KAC-94); DB-managed.
	_, err := f.netRepo.Insert(context.Background(), &domain.Network{
		ID: netID, FolderID: "f-d7", Name: domain.RcNameVPC("net-bind-mismatch"),
	})
	require.NoError(t, err)
	subID := ids.NewID(ids.PrefixSubnet)
	_, err = f.subRepo.Insert(context.Background(), &domain.Subnet{
		ID: subID, FolderID: "f-d7", NetworkID: netID,
		ZoneID: "ru-central1-c", V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	// Network → v6-only pool (mismatch для v4-allocate).
	require.NoError(t, f.bindings.SetNetworkDefault(context.Background(), netID, netDefV6.ID))

	// Address — internal v4 в этой подсети.
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "f-d7",
		Type:         domain.AddressTypeInternal,
		IpVersion:    domain.IpVersionIPv4,
		InternalIpv4: &domain.InternalIpv4Spec{SubnetID: subID},
	}
	aRec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)

	// FamilyV4 на этом address → cascade Step 2 находит netDefV6, family-skip
	// пропускает, fall-through (другого v4-pool нет) → ErrPoolNotResolved.
	res, err := f.svc.ResolvePoolForAddressObjFamily(context.Background(), aRec, FamilyV4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}
