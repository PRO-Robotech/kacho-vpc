// Package addresspool — usecase_test.go: unit-тесты use-case'ов через
// kachomock-Repository.
//
// Перенесено из `internal/apps/kacho/services/addresspool/
// service_split_test.go` (KAC-71 B-Group: split AddressPool.cidr_blocks →
// v4_cidr_blocks + v6_cidr_blocks) и `service_cascade_split_test.go`
// (KAC-71 D-Group: IPAM cascade family-skip).
//
// Wave 5 batch 36 (KAC-94, skill evgeniy §2 B.1): после переезда на
// use-case-структуру тесты вызывают каждый use-case напрямую.
//
// Wave 5 A.7 sub-PR 1/6 (KAC-94, skill evgeniy §6 G.1-G.7): AddressPool /
// Binding / CloudPoolSelector переехали на CQRS-Repository (`kacho.Repository`).
// Stubs `stub*AddressPoolRepo` / `stubBindingRepo` / `stubCloudSelRepo` удалены —
// заменены на `kachomock.Repository` (in-memory CQRS-impl с TX-семантикой и
// outbox-буфером). Network/Subnet/Address у kachomock тоже есть, но для cascade
// и Bind* тестов сохраняем legacy-mock'и (`repomock.NetworkRepo` / `SubnetRepo`
// / `AddressRepo`) — они подходят под узкие port'ы `NetworkRepo` / `SubnetReader`
// / `AddressRepo` через duck-typing (Get → *kacho.{Network,Subnet,Address}Record).
package addresspool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// --------------------------------------------------------------------------
// CQRS-Repository wrapper — KAC-94 A.7 sub-PR 1/6
//
// `kachomock.Repository` покрывает все 8 ресурсов + AddressPool + Binding +
// CloudPoolSelector. У него семантика in-memory TX (Writer накапливает
// изменения, Commit flush'ит). PopulateFreelistForPool — no-op в mock'е;
// чтобы тесты могли проверить «freelist materialized for pool X», мы
// оборачиваем Repository в `freelistRecordingRepo` ниже.
// --------------------------------------------------------------------------

// freelistRecordingRepo — kachomock-Repository + запись поlistCalls
// (PopulateFreelistForPool calls). Lightweight wrapper для B1 (Create v4-only
// pool — happy path) — там тест проверяет что use-case **позвал** populate.
type freelistRecordingRepo struct {
	inner *kachomock.Repository
	mu    sync.Mutex
	calls []string
}

func newFreelistRepo() *freelistRecordingRepo {
	return &freelistRecordingRepo{inner: kachomock.NewRepository()}
}

func (r *freelistRecordingRepo) Reader(ctx context.Context) (kachorepo.RepositoryReader, error) {
	return r.inner.Reader(ctx)
}

func (r *freelistRecordingRepo) Writer(ctx context.Context) (kachorepo.RepositoryWriter, error) {
	w, err := r.inner.Writer(ctx)
	if err != nil {
		return nil, err
	}
	return &freelistRecordingWriter{RepositoryWriter: w, parent: r}, nil
}

func (r *freelistRecordingRepo) Close() {}

func (r *freelistRecordingRepo) FreelistCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *freelistRecordingRepo) recordFreelist(poolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, poolID)
}

type freelistRecordingWriter struct {
	kachorepo.RepositoryWriter
	parent *freelistRecordingRepo
}

func (w *freelistRecordingWriter) AddressPools() kachorepo.AddressPoolWriterIface {
	return &freelistRecordingPoolWriter{
		AddressPoolWriterIface: w.RepositoryWriter.AddressPools(),
		parent:                 w.parent,
	}
}

type freelistRecordingPoolWriter struct {
	kachorepo.AddressPoolWriterIface
	parent *freelistRecordingRepo
}

func (pw *freelistRecordingPoolWriter) PopulateFreelistForPool(ctx context.Context, poolID string) error {
	pw.parent.recordFreelist(poolID)
	return pw.AddressPoolWriterIface.PopulateFreelistForPool(ctx, poolID)
}

// --------------------------------------------------------------------------
// Adapter-ы (parity с legacy-stubs)
// --------------------------------------------------------------------------

// networkRepoAdapter оборачивает repomock.NetworkRepo под port `NetworkRepo`.
type networkRepoAdapter struct {
	*repomock.NetworkRepo
}

func newNetworkRepoAdapter() *networkRepoAdapter {
	return &networkRepoAdapter{NetworkRepo: repomock.NewNetworkRepo()}
}

func (a *networkRepoAdapter) Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error) {
	return a.NetworkRepo.Get(ctx, id)
}

// subnetRepoAdapter оборачивает repomock.SubnetRepo под `SubnetReader`.
type subnetRepoAdapter struct {
	*repomock.SubnetRepo
}

func newSubnetRepoAdapter() *subnetRepoAdapter {
	return &subnetRepoAdapter{SubnetRepo: repomock.NewSubnetRepo()}
}

// projectClientAdapter — repomock.ProjectClient под port `ProjectClient`.
type projectClientAdapter struct {
	*repomock.ProjectClient
}

// useCasesFixture — общий набор зависимостей для use-case-тестов AddressPool.
type useCasesFixture struct {
	kr       *freelistRecordingRepo // kachomock + recorded freelist-calls
	addrRepo *repomock.AddressRepo
	netRepo  *networkRepoAdapter
	subRepo  *subnetRepoAdapter
	cloudSel string // helper-state, не используется напрямую (Set/Get идут через kr)

	create     *CreateAddressPoolUseCase
	update     *UpdateAddressPoolUseCase
	deleteUC   *DeleteAddressPoolUseCase
	bindNet    *BindAsNetworkDefaultUseCase
	bindAddr   *BindAsAddressOverrideUseCase
	unbindNet  *UnbindNetworkDefaultUseCase
	unbindAddr *UnbindAddressOverrideUseCase
	setCloud   *SetCloudPoolSelectorUseCase
	resolver   *ResolverService
	explain    *ExplainResolutionUseCase
}

func newUseCases(t *testing.T) *useCasesFixture {
	t.Helper()
	kr := newFreelistRepo()
	ar := repomock.NewAddressRepo()
	nr := newNetworkRepoAdapter()
	sr := newSubnetRepoAdapter()
	zr := repomock.NewZoneRegistry("ru-central1-c", "ru-central1-a", "ru-central1-d")
	fc := &projectClientAdapter{ProjectClient: &repomock.ProjectClient{OK: true}}

	resolver := NewResolverService(kr, ar, sr, fc)
	return &useCasesFixture{
		kr:       kr,
		addrRepo: ar, netRepo: nr, subRepo: sr,

		create:     NewCreateAddressPoolUseCase(kr, zr),
		update:     NewUpdateAddressPoolUseCase(kr),
		deleteUC:   NewDeleteAddressPoolUseCase(kr),
		bindNet:    NewBindAsNetworkDefaultUseCase(kr, nr),
		bindAddr:   NewBindAsAddressOverrideUseCase(kr, ar),
		unbindNet:  NewUnbindNetworkDefaultUseCase(kr),
		unbindAddr: NewUnbindAddressOverrideUseCase(kr),
		setCloud:   NewSetCloudPoolSelectorUseCase(kr),
		resolver:   resolver,
		explain:    NewExplainResolutionUseCase(ar, resolver),
	}
}

// poolGet — helper: достать pool через Reader-TX kachomock'а.
func (f *useCasesFixture) poolGet(t *testing.T, id string) *kachorepo.AddressPoolRecord {
	t.Helper()
	rd, err := f.kr.Reader(context.Background())
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	p, err := rd.AddressPools().Get(context.Background(), id)
	require.NoError(t, err)
	return p
}

// netDefaultBinding — helper.
func (f *useCasesFixture) netDefaultBinding(t *testing.T, networkID string) string {
	t.Helper()
	rd, err := f.kr.Reader(context.Background())
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	p, err := rd.AddressPoolBindings().GetNetworkDefault(context.Background(), networkID)
	require.NoError(t, err)
	return p
}

// addrOverrideBinding — helper.
func (f *useCasesFixture) addrOverrideBinding(t *testing.T, addressID string) string {
	t.Helper()
	rd, err := f.kr.Reader(context.Background())
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	p, err := rd.AddressPoolBindings().GetAddressOverride(context.Background(), addressID)
	require.NoError(t, err)
	return p
}

// seedPool — insert pool в kachomock-state (минуя use-case, чтобы избежать
// валидаций и freelist-init для тестовых fixture'ов).
func (f *useCasesFixture) seedPool(t *testing.T, name string, isDefault bool, zone string, v4, v6 []string, selector map[string]string) *domain.AddressPool {
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
	f.kr.inner.SeedAddressPool(&kachorepo.AddressPoolRecord{AddressPool: *p})
	return p
}

// seedAddressV4Req — Address с external_ipv4 spec.
func (f *useCasesFixture) seedAddressV4Req(t *testing.T, folder, zone string) *kachorepo.AddressRecord {
	t.Helper()
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: folder,
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	rec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)
	return rec
}

// seedAddressV6Req — Address с external_ipv6 spec.
func (f *useCasesFixture) seedAddressV6Req(t *testing.T, folder, zone string) *kachorepo.AddressRecord {
	t.Helper()
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: folder,
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: zone},
	}
	rec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)
	return rec
}

// --------------------------------------------------------------------------
// B1: Create v4-only pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B1_Create_V4Only_OK(t *testing.T) {
	f := newUseCases(t)

	p, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-v4-only",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"203.0.113.0/24"},
		V6CIDRBlocks: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.True(t, ids.IsValid(p.ID, "apl"), "id must be apl-prefixed crockford32")
	assert.Equal(t, []string{"203.0.113.0/24"}, p.V4CIDRBlocks)
	assert.Empty(t, p.V6CIDRBlocks, "v6_cidr_blocks must be empty for v4-only pool")
	// PopulateFreelistForPool вызван — pool готов к v4-аллокациям.
	calls := f.kr.FreelistCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, p.ID, calls[0])
}

// --------------------------------------------------------------------------
// B2: Create v6-only pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B2_Create_V6Only_OK(t *testing.T) {
	f := newUseCases(t)

	p, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-v6-only",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Empty(t, p.V4CIDRBlocks)
	assert.Equal(t, []string{"2001:db8::/64"}, p.V6CIDRBlocks)
}

// --------------------------------------------------------------------------
// B3: Create dual-stack pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B3_Create_DualStack_OK(t *testing.T) {
	f := newUseCases(t)

	p, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-dual-stack",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:1::/64"},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, []string{"198.51.100.0/24"}, p.V4CIDRBlocks)
	assert.Equal(t, []string{"2001:db8:1::/64"}, p.V6CIDRBlocks)
}

// --------------------------------------------------------------------------
// B5: Create отвергается если v4_cidr_blocks и v6_cidr_blocks оба пустые.
// --------------------------------------------------------------------------

func TestAddressPool_B5_Create_BothEmpty_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	_, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-empty",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: nil,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected grpc status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "v4_cidr_blocks and v6_cidr_blocks must not be both empty")
}

// --------------------------------------------------------------------------
// B6: cross-family CIDR placement → InvalidArgument
// --------------------------------------------------------------------------

func TestAddressPool_B6_Create_V6InV4Slot_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	_, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-cross-family",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"2001:db8::/64"},
		V6CIDRBlocks: nil,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "v4_cidr_blocks[0]")
	assert.Contains(t, st.Message(), "is not an IPv4 prefix")
}

func TestAddressPool_B6_Create_V4InV6Slot_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	_, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-cross-family-2",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: []string{"10.0.0.0/24"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "v6_cidr_blocks[0]")
	assert.Contains(t, st.Message(), "is not an IPv6 prefix")
}

// --------------------------------------------------------------------------
// B7: Update — replace_v4_cidr_blocks=true заменяет v4, v6 не тронут
// --------------------------------------------------------------------------

func TestAddressPool_B7_Update_ReplaceV4Only(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:            created.ID,
		ReplaceV4CIDR: true,
		V4CIDRBlocks:  []string{"192.0.2.0/24"},
		// ReplaceV6CIDR не выставлен → v6 не трогаем
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.0/24"}, updated.V4CIDRBlocks,
		"v4 must be replaced")
	assert.Equal(t, []string{"2001:db8::/64"}, updated.V6CIDRBlocks,
		"v6 must remain untouched")
}

// --------------------------------------------------------------------------
// B8: Update — replace_v6_cidr_blocks=true заменяет v6, v4 не тронут
// --------------------------------------------------------------------------

func TestAddressPool_B8_Update_ReplaceV6Only(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:            created.ID,
		ReplaceV6CIDR: true,
		V6CIDRBlocks:  []string{"2001:db8:2::/64"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks,
		"v4 must remain untouched")
	assert.Equal(t, []string{"2001:db8:2::/64"}, updated.V6CIDRBlocks,
		"v6 must be replaced")
}

// --------------------------------------------------------------------------
// B9: Update без обоих replace_v*=true → no-op для CIDR (но другие поля — ОК)
// --------------------------------------------------------------------------

func TestAddressPool_B9_Update_NoReplaceFlags_NoOpForCIDR(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual-noop",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:           created.ID,
		V4CIDRBlocks: []string{"10.99.99.0/24"},
		V6CIDRBlocks: []string{"2001:db8:dead::/64"},
		// ни ReplaceV4CIDR ни ReplaceV6CIDR не выставлены
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks,
		"v4 must remain (no replace flag)")
	assert.Equal(t, []string{"2001:db8::/64"}, updated.V6CIDRBlocks,
		"v6 must remain (no replace flag)")
}

// --------------------------------------------------------------------------
// B10: Update с обоими v4=[] v6=[] и оба replace_v*=true → InvalidArgument
// --------------------------------------------------------------------------

func TestAddressPool_B10_Update_ClearBoth_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	_, err = f.update.Execute(context.Background(), UpdatePoolReq{
		ID:            created.ID,
		ReplaceV4CIDR: true,
		V4CIDRBlocks:  []string{},
		ReplaceV6CIDR: true,
		V6CIDRBlocks:  []string{},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "v4_cidr_blocks and v6_cidr_blocks must not be both empty")
	// В БД pool остаётся без изменений (v4/v6 непустые).
	got := f.poolGet(t, created.ID)
	assert.NotEmpty(t, got.V4CIDRBlocks)
	assert.NotEmpty(t, got.V6CIDRBlocks)
}

// B10-symmetric: попытка очистить единственный непустой family (v4-only pool,
// replaceV4=true v4=[]) → InvalidArgument.
func TestAddressPool_B10_Update_ClearOnlyFamily_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "v4-only",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"203.0.113.0/24"},
	})
	require.NoError(t, err)

	_, err = f.update.Execute(context.Background(), UpdatePoolReq{
		ID:            created.ID,
		ReplaceV4CIDR: true,
		V4CIDRBlocks:  []string{},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "v4_cidr_blocks and v6_cidr_blocks must not be both empty")
}

// --------------------------------------------------------------------------
// B11: Update dual-stack pool, v6=[] + replace_v6=true → pool становится v4-only
// --------------------------------------------------------------------------

func TestAddressPool_B11_Update_ClearOneFamily_OnDualStack(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual-clear",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:ff::/64"},
	})
	require.NoError(t, err)

	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:            created.ID,
		ReplaceV6CIDR: true,
		V6CIDRBlocks:  []string{},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks,
		"v4 must remain")
	assert.Empty(t, updated.V6CIDRBlocks,
		"v6 must be cleared")
}

// --------------------------------------------------------------------------
// B12: Update без replace-флагов с непустым массивом → array body ignored,
// description обновляется
// --------------------------------------------------------------------------

func TestAddressPool_B12_Update_NoReplaceFlags_DescriptionUpdated(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual-noop",
		Description:  "old desc",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:ff::/64"},
	})
	require.NoError(t, err)

	newDesc := "noop update probe"
	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:           created.ID,
		Description:  &newDesc,
		V4CIDRBlocks: []string{"10.99.99.0/24"},      // ignored — no flag
		V6CIDRBlocks: []string{"2001:db8:dead::/64"}, // ignored — no flag
	})
	require.NoError(t, err)
	assert.Equal(t, "noop update probe", updated.Description)
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks)
	assert.Equal(t, []string{"2001:db8:ff::/64"}, updated.V6CIDRBlocks)
}

// --------------------------------------------------------------------------
// B13: Bind*/Override family-agnostic (не валидирует family при bind)
// --------------------------------------------------------------------------

func TestAddressPool_B13_BindNetworkDefault_FamilyAgnostic(t *testing.T) {
	f := newUseCases(t)

	pool, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "v6-bind",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	netID := ids.NewID(ids.PrefixNetwork)
	_, err = f.netRepo.NetworkRepo.Insert(context.Background(), &domain.Network{
		ID: netID, ProjectID: "f1", Name: domain.RcNameVPC("net-v6-bind"),
	})
	require.NoError(t, err)

	// Bind — должно пройти НЕ смотря на то что pool v6-only.
	err = f.bindNet.Execute(context.Background(), netID, pool.ID)
	require.NoError(t, err, "Bind* should be family-agnostic")

	assert.Equal(t, pool.ID, f.netDefaultBinding(t, netID))
}

func TestAddressPool_B13_BindAddressOverride_FamilyAgnostic(t *testing.T) {
	f := newUseCases(t)

	pool, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "v4-bind-override",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"203.0.113.0/24"},
	})
	require.NoError(t, err)

	addrID := ids.NewID(ids.PrefixAddress)
	_, err = f.addrRepo.Insert(context.Background(), &domain.Address{
		ID: addrID, ProjectID: "f1",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: "ru-central1-c"},
	})
	require.NoError(t, err)

	err = f.bindAddr.Execute(context.Background(), addrID, pool.ID)
	require.NoError(t, err, "Override Bind should be family-agnostic")

	assert.Equal(t, pool.ID, f.addrOverrideBinding(t, addrID))
}

// --------------------------------------------------------------------------
// D1: v4-only pool не резолвится для v6-allocate.
// --------------------------------------------------------------------------

func TestCascade_D1_V4OnlyPool_DoesNotResolveForV6(t *testing.T) {
	f := newUseCases(t)
	f.seedPool(t, "global-v4", true, "", []string{"203.0.113.0/24"}, nil, nil)

	a := f.seedAddressV6Req(t, "f-d1", "ru-central1-c")

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV6)
	require.Error(t, err, "v6-allocate must NOT pick v4-only pool")
	assert.True(t, errors.Is(err, ErrPoolNotResolved),
		"expected ErrPoolNotResolved, got %v", err)
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// D2: v6-only pool не резолвится для v4-allocate.
// --------------------------------------------------------------------------

func TestCascade_D2_V6OnlyPool_DoesNotResolveForV4(t *testing.T) {
	f := newUseCases(t)
	f.seedPool(t, "global-v6", true, "", nil, []string{"2001:db8::/64"}, nil)

	a := f.seedAddressV4Req(t, "f-d2", "ru-central1-c")

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// D3: dual-stack pool — резолвится для обоих family (Step 4 zone_default).
// --------------------------------------------------------------------------

func TestCascade_D3_DualStackPool_ResolvesForBothFamilies(t *testing.T) {
	f := newUseCases(t)
	dual := f.seedPool(t, "dual", true, "ru-central1-c",
		[]string{"198.51.100.0/24"}, []string{"2001:db8:ff::/64"}, nil)

	v4Addr := f.seedAddressV4Req(t, "f-d3", "ru-central1-c")
	v6Addr := f.seedAddressV6Req(t, "f-d3", "ru-central1-c")

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), v4Addr, FamilyV4)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, dual.ID, res.Pool.ID)
	assert.Equal(t, "zone_default", res.MatchedVia)

	res2, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), v6Addr, FamilyV6)
	require.NoError(t, err)
	require.NotNil(t, res2)
	assert.Equal(t, dual.ID, res2.Pool.ID)
	assert.Equal(t, "zone_default", res2.MatchedVia)
}

// --------------------------------------------------------------------------
// D4: ExplainResolution на fall-through отдаёт ErrPoolNotResolved.
// --------------------------------------------------------------------------

func TestCascade_D4_ExplainResolution_FallThrough_ReturnsErrPoolNotResolved(t *testing.T) {
	f := newUseCases(t)
	f.seedPool(t, "v4-only", true, "ru-central1-c", []string{"203.0.113.0/24"}, nil, nil)

	a := f.seedAddressV6Req(t, "f-d4", "ru-central1-c")

	primary, runnerUp, err := f.explain.Execute(context.Background(), a.ID, "")
	require.Error(t, err, "ExplainResolution must return ErrPoolNotResolved when no family-match pool")
	assert.True(t, errors.Is(err, ErrPoolNotResolved),
		"sentinel must be ErrPoolNotResolved (handler converts to gRPC OK + matched_via=none)")
	assert.Nil(t, primary)
	assert.Nil(t, runnerUp)
}

// --------------------------------------------------------------------------
// D5: Step 3 label-selector — pool без нужной family пропускается; cascade
// выбирает global_default другой family.
// --------------------------------------------------------------------------

func TestCascade_D5_LabelSelector_FamilySkip(t *testing.T) {
	// Кастомный fixture с projectClient.CloudID="cloud-d5".
	kr := newFreelistRepo()
	ar := repomock.NewAddressRepo()
	sr := newSubnetRepoAdapter()

	fc := &projectClientAdapter{ProjectClient: &repomock.ProjectClient{OK: true}}
	fc.CloudID = "cloud-d5"

	resolver := NewResolverService(kr, ar, sr, fc)

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
	kr.inner.SeedAddressPool(&kachorepo.AddressPoolRecord{AddressPool: *premiumV4})

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
	kr.inner.SeedAddressPool(&kachorepo.AddressPoolRecord{AddressPool: *globalV6})

	kr.inner.SeedCloudPoolSelector(&domain.CloudPoolSelector{
		CloudID:  "cloud-d5",
		Selector: map[string]string{"tier": "premium"},
		SetAt:    now, SetBy: "admin@test",
	})

	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: "folder-d5",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: "ru-central1-c"},
	}
	aRec, err := ar.Insert(context.Background(), a)
	require.NoError(t, err)

	res, err := resolver.ResolvePoolForAddressObjFamily(context.Background(), aRec, FamilyV6)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, globalV6.ID, res.Pool.ID,
		"v6-allocate must skip v4-only premium pool (Step 3) and fall through to global_default")
	assert.Equal(t, "global_default", res.MatchedVia)
}

// --------------------------------------------------------------------------
// D6: Step 1 — per-address override family-skip.
// --------------------------------------------------------------------------

func TestCascade_D6_AddressOverride_FamilySkip(t *testing.T) {
	f := newUseCases(t)
	overrideV4 := f.seedPool(t, "override-v4", false, "ru-central1-c",
		[]string{"203.0.113.0/24"}, nil, nil)

	a := f.seedAddressV6Req(t, "f-d6", "ru-central1-c")

	f.kr.inner.SeedAddressOverrideBinding(a.ID, overrideV4.ID)

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV6)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// D7: Step 2 — per-network default family-skip.
// --------------------------------------------------------------------------

func TestCascade_D7_NetworkDefault_FamilySkip(t *testing.T) {
	f := newUseCases(t)
	netDefV6 := f.seedPool(t, "net-def-v6", false, "ru-central1-c",
		nil, []string{"2001:db8::/64"}, nil)

	netID := ids.NewID(ids.PrefixNetwork)
	_, err := f.netRepo.NetworkRepo.Insert(context.Background(), &domain.Network{
		ID: netID, ProjectID: "f-d7", Name: domain.RcNameVPC("net-bind-mismatch"),
	})
	require.NoError(t, err)
	subID := ids.NewID(ids.PrefixSubnet)
	_, err = f.subRepo.SubnetRepo.Insert(context.Background(), &domain.Subnet{
		ID: subID, ProjectID: "f-d7", NetworkID: netID,
		ZoneID: "ru-central1-c", V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	f.kr.inner.SeedNetworkDefaultBinding(netID, netDefV6.ID)

	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: "f-d7",
		Type:         domain.AddressTypeInternal,
		IpVersion:    domain.IpVersionIPv4,
		InternalIpv4: &domain.InternalIpv4Spec{SubnetID: subID},
	}
	aRec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), aRec, FamilyV4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}

// _ = repo: для будущих тестов, если потребуется использовать sentinels из repo.
var _ = repo.ErrNotFound
