// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package addresspool — usecase_test.go: unit-тесты use-case'ов через
// kachomock-Repository. Тесты вызывают каждый use-case напрямую.
//
// AddressPool / Binding работают через CQRS-Repository
// (`kacho.Repository`); в тестах это `kachomock.Repository` — in-memory
// CQRS-impl с TX-семантикой и outbox-буфером. Network/Subnet/Address у kachomock
// тоже есть, но для cascade- и Bind*-тестов используем mock'и
// (`repomock.NetworkRepo` / `SubnetRepo` / `AddressRepo`): они подходят под узкие
// port'ы `NetworkRepo` / `SubnetReader` / `AddressRepo` через duck-typing
// (Get → *kacho.{Network,Subnet,Address}Record).
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
// CQRS-Repository wrapper
//
// `kachomock.Repository` покрывает все 8 ресурсов + AddressPool + Binding.
// У него семантика in-memory TX (Writer накапливает
// изменения, Commit flush'ит). PopulateFreelistForPool — no-op в mock'е;
// чтобы тесты могли проверить «freelist materialized for pool X», мы
// оборачиваем Repository в `freelistRecordingRepo` ниже.
// --------------------------------------------------------------------------

// freelistRecordingRepo — kachomock-Repository + запись PopulateFreelistForPool-
// вызовов. Lightweight wrapper для теста happy-path Create v4-only pool, где
// проверяется, что use-case **позвал** populate.
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

// ResetFreelistAdds проксирует на inner kachomock, чтобы тест мог отделить
// дельту AddCidrBlocks от первичного populate в Create.
func (r *freelistRecordingRepo) ResetFreelistAdds(poolID string) {
	r.inner.ResetFreelistAdds(poolID)
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
// Adapter-ы
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

// useCasesFixture — общий набор зависимостей для use-case-тестов AddressPool.
type useCasesFixture struct {
	kr       *freelistRecordingRepo // kachomock + recorded freelist-calls
	addrRepo *repomock.AddressRepo
	netRepo  *networkRepoAdapter
	subRepo  *subnetRepoAdapter

	create    *CreateAddressPoolUseCase
	update    *UpdateAddressPoolUseCase
	deleteUC  *DeleteAddressPoolUseCase
	bindNet   *BindAsNetworkDefaultUseCase
	unbindNet *UnbindNetworkDefaultUseCase
	resolver  *ResolverService
}

func newUseCases(t *testing.T) *useCasesFixture {
	t.Helper()
	kr := newFreelistRepo()
	ar := repomock.NewAddressRepo()
	nr := newNetworkRepoAdapter()
	sr := newSubnetRepoAdapter()
	zr := repomock.NewZoneRegistry("zone-c", "zone-a", "zone-d")

	resolver := NewResolverService(kr, ar, sr)
	return &useCasesFixture{
		kr:       kr,
		addrRepo: ar, netRepo: nr, subRepo: sr,

		create:    NewCreateAddressPoolUseCase(kr, zr),
		update:    NewUpdateAddressPoolUseCase(kr),
		deleteUC:  NewDeleteAddressPoolUseCase(kr),
		bindNet:   NewBindAsNetworkDefaultUseCase(kr, nr),
		unbindNet: NewUnbindNetworkDefaultUseCase(kr),
		resolver:  resolver,
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

// seedPool вставляет pool в kachomock-state напрямую (минуя use-case), чтобы не
// тащить валидации и freelist-init в тестовые fixture'ы.
func (f *useCasesFixture) seedPool(t *testing.T, name string, isDefault bool, zone string, v4, v6 []string, selector map[string]string) *domain.AddressPool {
	t.Helper()
	now := time.Now().UTC()
	p := &domain.AddressPool{
		ID:             ids.NewID(ids.PrefixAddressPool),
		Name:           domain.RcNameVPC(name),
		V4CIDRBlocks:   v4,
		V6CIDRBlocks:   v6,
		Kind:           domain.AddressPoolKindExternalPublic,
		ZoneID:         zone,
		IsDefault:      isDefault,
		SelectorLabels: domain.LabelsFromMap(selector),
	}
	f.kr.inner.SeedAddressPool(&kachorepo.AddressPoolRecord{AddressPool: *p, CreatedAt: now, ModifiedAt: now})
	return p
}

// seedAddressV4Req — Address с external_ipv4 spec.
func (f *useCasesFixture) seedAddressV4Req(t *testing.T, project, zone string) *kachorepo.AddressRecord {
	t.Helper()
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: project,
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{ZoneID: zone},
	}
	rec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)
	return rec
}

// seedAddressV6Req — Address с external_ipv6 spec.
func (f *useCasesFixture) seedAddressV6Req(t *testing.T, project, zone string) *kachorepo.AddressRecord {
	t.Helper()
	a := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: project,
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: zone},
	}
	rec, err := f.addrRepo.Insert(context.Background(), a)
	require.NoError(t, err)
	return rec
}

// --------------------------------------------------------------------------
// Create v4-only pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B1_Create_V4Only_OK(t *testing.T) {
	f := newUseCases(t)

	p, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-v4-only",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"203.0.113.0/24"},
		V6CIDRBlocks: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.True(t, ids.IsValid(p.ID, ids.PrefixAddressPool), "id must be apl-prefixed crockford32")
	assert.Equal(t, []string{"203.0.113.0/24"}, p.V4CIDRBlocks)
	assert.Empty(t, p.V6CIDRBlocks, "v6_cidr_blocks must be empty for v4-only pool")
	// PopulateFreelistForPool вызван — pool готов к v4-аллокациям.
	calls := f.kr.FreelistCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, p.ID, calls[0])
}

// --------------------------------------------------------------------------
// Create v6-only pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B2_Create_V6Only_OK(t *testing.T) {
	f := newUseCases(t)

	p, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-v6-only",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Empty(t, p.V4CIDRBlocks)
	assert.Equal(t, []string{"2001:db8::/64"}, p.V6CIDRBlocks)
}

// --------------------------------------------------------------------------
// Create dual-stack pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B3_Create_DualStack_OK(t *testing.T) {
	f := newUseCases(t)

	p, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-dual-stack",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:1::/64"},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, []string{"198.51.100.0/24"}, p.V4CIDRBlocks)
	assert.Equal(t, []string{"2001:db8:1::/64"}, p.V6CIDRBlocks)
}

// --------------------------------------------------------------------------
// Create отвергается, если v4_cidr_blocks и v6_cidr_blocks оба пустые.
// --------------------------------------------------------------------------

func TestAddressPool_B5_Create_BothEmpty_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	_, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-empty",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
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
// cross-family CIDR placement → InvalidArgument
// --------------------------------------------------------------------------

func TestAddressPool_B6_Create_V6InV4Slot_InvalidArgument(t *testing.T) {
	f := newUseCases(t)

	_, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "pool-cross-family",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
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
		ZoneID:       "zone-c",
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
// Update больше НЕ меняет CIDR: description обновляется, v4/v6 остаются прежними.
// --------------------------------------------------------------------------

func TestAddressPool_KAC269_Update_DoesNotTouchCIDR(t *testing.T) {
	f := newUseCases(t)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "dual-upd",
		Description:  "old desc",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:ff::/64"},
	})
	require.NoError(t, err)

	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:          created.ID,
		UpdateMask:  []string{"description"},
		Description: "kac-269 update probe",
	})
	require.NoError(t, err)
	assert.Equal(t, "kac-269 update probe", string(updated.Description))
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks, "v4 untouched by Update")
	assert.Equal(t, []string{"2001:db8:ff::/64"}, updated.V6CIDRBlocks, "v6 untouched by Update")
}

// --------------------------------------------------------------------------
// Partial-update идет через FieldMask. Update не использует per-field флаги
// (update_is_default / replace_labels / ...); набор изменяемых полей задается
// через UpdateMask. Эти тесты фиксируют дисциплину: mask применяет только
// указанные поля, immutable/unknown в mask → InvalidArgument, пустой mask →
// full-PATCH мутабельных полей.
// --------------------------------------------------------------------------

func TestAddressPool_UpdateMask_IsDefault_Flips(t *testing.T) {
	f := newUseCases(t)
	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "mask-default",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
	})
	require.NoError(t, err)
	require.False(t, created.IsDefault)

	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:         created.ID,
		UpdateMask: []string{"is_default"},
		IsDefault:  true,
	})
	require.NoError(t, err)
	assert.True(t, updated.IsDefault, "is_default applied via mask")
}

func TestAddressPool_UpdateMask_FieldNotInMask_Ignored(t *testing.T) {
	f := newUseCases(t)
	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "mask-ignore",
		Description:  "keep me",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.101.0/24"},
	})
	require.NoError(t, err)

	// в mask только is_default; description из тела должен игнорироваться.
	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:          created.ID,
		UpdateMask:  []string{"is_default"},
		IsDefault:   true,
		Description: "should be ignored",
	})
	require.NoError(t, err)
	assert.True(t, updated.IsDefault)
	assert.Equal(t, "keep me", string(updated.Description), "description not in mask → untouched")
}

func TestAddressPool_UpdateMask_ImmutableZone_InvalidArgument(t *testing.T) {
	f := newUseCases(t)
	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "mask-immutable",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.102.0/24"},
	})
	require.NoError(t, err)

	_, err = f.update.Execute(context.Background(), UpdatePoolReq{
		ID:         created.ID,
		UpdateMask: []string{"zone_id"},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "immutable zone_id in mask → InvalidArgument")
}

func TestAddressPool_UpdateMask_UnknownField_InvalidArgument(t *testing.T) {
	f := newUseCases(t)
	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "mask-unknown",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.103.0/24"},
	})
	require.NoError(t, err)

	_, err = f.update.Execute(context.Background(), UpdatePoolReq{
		ID:         created.ID,
		UpdateMask: []string{"bogus_field"},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "unknown field in mask → InvalidArgument")
}

func TestAddressPool_UpdateMask_Empty_FullPatch(t *testing.T) {
	f := newUseCases(t)
	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "mask-empty",
		Description:  "old",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.104.0/24"},
	})
	require.NoError(t, err)

	// Пустой mask → full-PATCH: все мутабельные поля применяются из тела.
	updated, err := f.update.Execute(context.Background(), UpdatePoolReq{
		ID:               created.ID,
		Name:             "renamed",
		Description:      "new",
		IsDefault:        true,
		SelectorPriority: 7,
	})
	require.NoError(t, err)
	assert.Equal(t, "renamed", string(updated.Name))
	assert.Equal(t, "new", string(updated.Description))
	assert.True(t, updated.IsDefault)
	assert.Equal(t, int32(7), updated.SelectorPriority)
}

// --------------------------------------------------------------------------
// AddCidrBlocks v4 → блок добавлен + freelist пополнен только для новой дельты.
// --------------------------------------------------------------------------

func TestAddressPool_KAC269_AddCidrBlocks_V4_AppendsAndPopulatesDelta(t *testing.T) {
	f := newUseCases(t)
	addCidr := NewAddCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "add-v4",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
	})
	require.NoError(t, err)
	// Сбрасываем запись populate из Create — интересует только дельта Add.
	f.kr.ResetFreelistAdds(created.ID)

	updated, err := addCidr.Execute(context.Background(), created.ID,
		[]string{"203.0.113.0/24"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/24", "203.0.113.0/24"}, updated.V4CIDRBlocks)

	// Freelist пополнен ТОЛЬКО для нового CIDR (не для уже существующего).
	added := f.kr.inner.FreelistAddedCidrs(created.ID)
	assert.Equal(t, []string{"203.0.113.0/24"}, added,
		"only the new v4 cidr must be materialised into freelist")
}

// Повторный AddCidrBlocks того же блока — no-op для состава и НЕ материализует
// freelist повторно.
func TestAddressPool_KAC269_AddCidrBlocks_DedupExisting(t *testing.T) {
	f := newUseCases(t)
	addCidr := NewAddCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "add-dedup",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
	})
	require.NoError(t, err)
	f.kr.ResetFreelistAdds(created.ID)

	updated, err := addCidr.Execute(context.Background(), created.ID,
		[]string{"198.51.100.0/24"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks, "dedup: no duplicate")
	assert.Empty(t, f.kr.inner.FreelistAddedCidrs(created.ID),
		"existing cidr must NOT be re-materialised")
}

// v6-prefix в v4-слоте → InvalidArgument.
func TestAddressPool_KAC269_AddCidrBlocks_CrossFamily_InvalidArgument(t *testing.T) {
	f := newUseCases(t)
	addCidr := NewAddCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "add-fam",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
	})
	require.NoError(t, err)

	_, err = addCidr.Execute(context.Background(), created.ID,
		[]string{"2001:db8::/64"}, nil)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "is not an IPv4 prefix")
}

// --------------------------------------------------------------------------
// RemoveCidrBlocks с выделенным IP в CIDR → FailedPrecondition: нельзя удалить
// CIDR, из которого уже выданы адреса.
// --------------------------------------------------------------------------

func TestAddressPool_KAC269_RemoveCidrBlocks_InUse_FailedPrecondition(t *testing.T) {
	f := newUseCases(t)
	removeCidr := NewRemoveCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "rm-inuse",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24", "203.0.113.0/24"},
	})
	require.NoError(t, err)
	// Симулируем: в пуле есть выделенный external-IP, попадающий в удаляемый CIDR.
	f.kr.inner.SeedAllocatedInCidr(created.ID, 1)

	_, err = removeCidr.Execute(context.Background(), created.ID,
		[]string{"198.51.100.0/24"}, nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "has allocated addresses")

	// Пул НЕ изменился (TX abort).
	got := f.poolGet(t, created.ID)
	assert.ElementsMatch(t, []string{"198.51.100.0/24", "203.0.113.0/24"}, got.V4CIDRBlocks)
}

// Чистый CIDR (без выделенных адресов) удаляется.
func TestAddressPool_KAC269_RemoveCidrBlocks_Clean_OK(t *testing.T) {
	f := newUseCases(t)
	removeCidr := NewRemoveCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "rm-clean",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24", "203.0.113.0/24"},
	})
	require.NoError(t, err)
	// allocatedInCidr не засеян → 0 → CIDR чистый.

	updated, err := removeCidr.Execute(context.Background(), created.ID,
		[]string{"203.0.113.0/24"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/24"}, updated.V4CIDRBlocks)
}

// Удаляемый блок отсутствует в пуле → FailedPrecondition.
func TestAddressPool_KAC269_RemoveCidrBlocks_NotPresent_FailedPrecondition(t *testing.T) {
	f := newUseCases(t)
	removeCidr := NewRemoveCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "rm-nf",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
	})
	require.NoError(t, err)

	_, err = removeCidr.Execute(context.Background(), created.ID,
		[]string{"10.0.0.0/24"}, nil)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not found in address pool")
}

// Удаление последнего CIDR (пул остался бы без блоков) → InvalidArgument.
func TestAddressPool_KAC269_RemoveCidrBlocks_Empties_InvalidArgument(t *testing.T) {
	f := newUseCases(t)
	removeCidr := NewRemoveCidrBlocksUseCase(f.kr)

	created, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "rm-empty",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
	})
	require.NoError(t, err)

	_, err = removeCidr.Execute(context.Background(), created.ID,
		[]string{"198.51.100.0/24"}, nil)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "must not be both empty after removal")
}

// --------------------------------------------------------------------------
// Bind*/Override family-agnostic (family не валидируется при bind)
// --------------------------------------------------------------------------

func TestAddressPool_B13_BindNetworkDefault_FamilyAgnostic(t *testing.T) {
	f := newUseCases(t)

	pool, err := f.create.Execute(context.Background(), CreatePoolReq{
		Name:         "v6-bind",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	netID := ids.NewID(ids.PrefixNetwork)
	_, err = f.netRepo.NetworkRepo.Insert(context.Background(), &domain.Network{
		ID: netID, ProjectID: "f1", Name: domain.RcNameVPC("net-v6-bind"),
	})
	require.NoError(t, err)

	// Bind должен пройти, несмотря на то что pool v6-only.
	err = f.bindNet.Execute(context.Background(), netID, pool.ID)
	require.NoError(t, err, "Bind* should be family-agnostic")

	assert.Equal(t, pool.ID, f.netDefaultBinding(t, netID))
}

// --------------------------------------------------------------------------
// v4-only pool не резолвится для v6-allocate.
// --------------------------------------------------------------------------

func TestCascade_D1_V4OnlyPool_DoesNotResolveForV6(t *testing.T) {
	f := newUseCases(t)
	f.seedPool(t, "global-v4", true, "", []string{"203.0.113.0/24"}, nil, nil)

	a := f.seedAddressV6Req(t, "f-d1", "zone-c")

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV6)
	require.Error(t, err, "v6-allocate must NOT pick v4-only pool")
	assert.True(t, errors.Is(err, ErrPoolNotResolved),
		"expected ErrPoolNotResolved, got %v", err)
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// v6-only pool не резолвится для v4-allocate.
// --------------------------------------------------------------------------

func TestCascade_D2_V6OnlyPool_DoesNotResolveForV4(t *testing.T) {
	f := newUseCases(t)
	f.seedPool(t, "global-v6", true, "", nil, []string{"2001:db8::/64"}, nil)

	a := f.seedAddressV4Req(t, "f-d2", "zone-c")

	res, err := f.resolver.ResolvePoolForAddressObjFamily(context.Background(), a, FamilyV4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPoolNotResolved))
	assert.Nil(t, res)
}

// --------------------------------------------------------------------------
// dual-stack pool резолвится для обоих family (через zone_default).
// --------------------------------------------------------------------------

func TestCascade_D3_DualStackPool_ResolvesForBothFamilies(t *testing.T) {
	f := newUseCases(t)
	dual := f.seedPool(t, "dual", true, "zone-c",
		[]string{"198.51.100.0/24"}, []string{"2001:db8:ff::/64"}, nil)

	v4Addr := f.seedAddressV4Req(t, "f-d3", "zone-c")
	v6Addr := f.seedAddressV6Req(t, "f-d3", "zone-c")

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
// per-network default: family-skip (v6-only default не подходит для v4-allocate).
// --------------------------------------------------------------------------

func TestCascade_D7_NetworkDefault_FamilySkip(t *testing.T) {
	f := newUseCases(t)
	netDefV6 := f.seedPool(t, "net-def-v6", false, "zone-c",
		nil, []string{"2001:db8::/64"}, nil)

	netID := ids.NewID(ids.PrefixNetwork)
	_, err := f.netRepo.NetworkRepo.Insert(context.Background(), &domain.Network{
		ID: netID, ProjectID: "f-d7", Name: domain.RcNameVPC("net-bind-mismatch"),
	})
	require.NoError(t, err)
	subID := ids.NewID(ids.PrefixSubnet)
	_, err = f.subRepo.SubnetRepo.Insert(context.Background(), &domain.Subnet{
		ID: subID, ProjectID: "f-d7", NetworkID: netID,
		ZoneID: "zone-c", V4CidrBlocks: []string{"10.0.0.0/24"},
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
