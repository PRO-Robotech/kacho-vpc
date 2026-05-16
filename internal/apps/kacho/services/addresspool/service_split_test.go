// KAC-71: TDD red-phase service-level тесты для split AddressPool.cidr_blocks
// → v4_cidr_blocks + v6_cidr_blocks.
//
// Acceptance: docs/specs/sub-phase-1.x-addresspool-split-cidr-family-acceptance.md
//
// Покрытие — Group B (REQ-IPL-CR-01..06, REQ-IPL-UPD-01..06,
// REQ-IPL-BIND-FAMILY-AGNOSTIC):
//   - B1/B2/B3 — Create v4-only / v6-only / dual-stack
//   - B4 — Create отвергается если оба пусты (InvalidArgument)
//   - B5 — Create отвергается при cross-family (IPv6 в v4_cidr_blocks или
//     наоборот, InvalidArgument)
//   - B7 — Update replace_v4=true заменяет v4, v6 не тронут
//   - B8 — Update replace_v6=true заменяет v6, v4 не тронут
//   - B9 — Update без обоих replace_v*=true → array body ignored
//     (poll-update no-op для CIDR-полей)
//   - B10 — Update попытка очистить оба family → InvalidArgument
//   - B11 — Update dual-stack pool, v6=[] + replace_v6=true → pool становится v4-only
//   - B12 — Update без replace-флагов с непустым массивом — body массивы
//     игнорируются, description обновляется
//   - B13 — Bind* family-agnostic (не валидирует family, фильтр только на resolve)
//
// Все тесты — failing на текущей реализации (domain.AddressPool пока имеет
// CIDRBlocks (старое поле); UpdatePoolReq пока имеет ReplaceCIDR, не
// ReplaceV4CIDR/ReplaceV6CIDR). После rpc-implementer KAC-74 — позеленеют.
package addresspool

import (
	"context"
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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// --------------------------------------------------------------------------
// Local fakes — portmock не содержит реализаций AddressPoolRepo /
// AddressPoolBindingRepo / CloudPoolSelectorRepo. Делаем их inline здесь,
// чтобы не загромождать общий portmock package.
// --------------------------------------------------------------------------

type stubAddressPoolRepo struct {
	mu    sync.Mutex
	pools map[string]*domain.AddressPool
	// freelistCalls регистрирует PopulateFreelistForPool — для assertion'ов.
	freelistCalls []string
}

func newStubAddressPoolRepo() *stubAddressPoolRepo {
	return &stubAddressPoolRepo{pools: map[string]*domain.AddressPool{}}
}

func (r *stubAddressPoolRepo) Get(_ context.Context, id string) (*domain.AddressPool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pools[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *stubAddressPoolRepo) List(_ context.Context, _ repo.AddressPoolFilter, _ repo.Pagination) ([]*domain.AddressPool, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.AddressPool, 0, len(r.pools))
	for _, p := range r.pools {
		cp := *p
		out = append(out, &cp)
	}
	return out, "", nil
}

func (r *stubAddressPoolRepo) Insert(_ context.Context, p *domain.AddressPool) (*domain.AddressPool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *p
	r.pools[p.ID] = &cp
	return p, nil
}

func (r *stubAddressPoolRepo) Update(_ context.Context, p *domain.AddressPool) (*domain.AddressPool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pools[p.ID]; !ok {
		return nil, repo.ErrNotFound
	}
	cp := *p
	r.pools[p.ID] = &cp
	return p, nil
}

func (r *stubAddressPoolRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pools[id]; !ok {
		return repo.ErrNotFound
	}
	delete(r.pools, id)
	return nil
}

func (r *stubAddressPoolRepo) GetDefaultForZone(_ context.Context, zoneID string, kind domain.AddressPoolKind) (*domain.AddressPool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pools {
		if p.Kind == kind && p.IsDefault && p.ZoneID == zoneID {
			cp := *p
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (r *stubAddressPoolRepo) FindBySelectorMatch(_ context.Context, sel map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*domain.AddressPool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.AddressPool
	for _, p := range r.pools {
		if p.Kind != kind || (p.ZoneID != zoneID && p.ZoneID != "") {
			continue
		}
		if len(p.SelectorLabels) == 0 {
			continue
		}
		// containment: networkSelector ⊆ pool.SelectorLabels
		match := true
		for k, v := range sel {
			if p.SelectorLabels[k] != v {
				match = false
				break
			}
		}
		if match {
			cp := *p
			out = append(out, &cp)
		}
	}
	if len(out) == 0 {
		return nil, repo.ErrNotFound
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *stubAddressPoolRepo) FindAmbiguousSelectorGroups(_ context.Context, _ string) ([][]*domain.AddressPool, error) {
	return nil, nil
}

func (r *stubAddressPoolRepo) CountAddressesByPool(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (r *stubAddressPoolRepo) CountAddressesByPoolPerCIDR(_ context.Context, _ string) (map[string]int64, error) {
	return nil, nil
}

func (r *stubAddressPoolRepo) ListAddressesByPool(_ context.Context, _ string, _ string, _ repo.Pagination) ([]*domain.AddressRecord, string, error) {
	return nil, "", nil
}

func (r *stubAddressPoolRepo) PopulateFreelistForPool(_ context.Context, poolID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freelistCalls = append(r.freelistCalls, poolID)
	return nil
}

// stubBindingRepo — fake AddressPoolBindingRepo.
type stubBindingRepo struct {
	mu       sync.Mutex
	netDef   map[string]string // network_id → pool_id
	addrOver map[string]string // address_id → pool_id
}

func newStubBindingRepo() *stubBindingRepo {
	return &stubBindingRepo{netDef: map[string]string{}, addrOver: map[string]string{}}
}

func (r *stubBindingRepo) SetNetworkDefault(_ context.Context, networkID, poolID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.netDef[networkID] = poolID
	return nil
}

func (r *stubBindingRepo) GetNetworkDefault(_ context.Context, networkID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.netDef[networkID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

func (r *stubBindingRepo) UnsetNetworkDefault(_ context.Context, networkID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.netDef, networkID)
	return nil
}

func (r *stubBindingRepo) SetAddressOverride(_ context.Context, addressID, poolID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addrOver[addressID] = poolID
	return nil
}

func (r *stubBindingRepo) GetAddressOverride(_ context.Context, addressID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.addrOver[addressID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

func (r *stubBindingRepo) UnsetAddressOverride(_ context.Context, addressID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.addrOver, addressID)
	return nil
}

// stubCloudSelRepo — fake CloudPoolSelectorRepo.
type stubCloudSelRepo struct {
	mu sync.Mutex
	m  map[string]*domain.CloudPoolSelector
}

func newStubCloudSelRepo() *stubCloudSelRepo {
	return &stubCloudSelRepo{m: map[string]*domain.CloudPoolSelector{}}
}

func (r *stubCloudSelRepo) Set(_ context.Context, cloudID string, sel map[string]string, setBy string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[cloudID] = &domain.CloudPoolSelector{
		CloudID: cloudID, Selector: sel, SetBy: setBy, SetAt: time.Now().UTC(),
	}
	return nil
}

func (r *stubCloudSelRepo) Get(_ context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.m[cloudID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (r *stubCloudSelRepo) Unset(_ context.Context, cloudID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, cloudID)
	return nil
}

// makeAddressPoolService — собирает AddressPoolService с тестовыми моками.
// Все 7 портов передаются; addressRepo и subnetRepo используются только на
// cascade-path (D-тесты — отдельный файл). Для B-тестов это просто заглушки.
func makeAddressPoolService(
	poolRepo *stubAddressPoolRepo,
	bindings *stubBindingRepo,
	cloudSel *stubCloudSelRepo,
) *AddressPoolService {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	nr := repomock.NewNetworkRepo()
	fc := &repomock.FolderClient{OK: true}
	zr := repomock.NewZoneRegistry("ru-central1-c", "ru-central1-a", "ru-central1-d")
	return NewAddressPoolService(poolRepo, bindings, cloudSel, ar, nr, sr, fc, zr)
}

// --------------------------------------------------------------------------
// B1: Create v4-only pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B1_Create_V4Only_OK(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	p, err := svc.Create(context.Background(), CreatePoolReq{
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
	require.Len(t, r.freelistCalls, 1)
	assert.Equal(t, p.ID, r.freelistCalls[0])
}

// --------------------------------------------------------------------------
// B2: Create v6-only pool — happy path
// --------------------------------------------------------------------------

func TestAddressPool_B2_Create_V6Only_OK(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	p, err := svc.Create(context.Background(), CreatePoolReq{
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
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	p, err := svc.Create(context.Background(), CreatePoolReq{
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
// B5 (был B4 в acceptance — теперь B5: оба пусты → InvalidArgument).
// (Сам acceptance имеет нумерацию B1..B13; здесь сохраняю те же ID что
// в задаче — B5 = both empty.)
// --------------------------------------------------------------------------

// B5: Create отвергается если v4_cidr_blocks и v6_cidr_blocks оба пустые.
func TestAddressPool_B5_Create_BothEmpty_InvalidArgument(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	_, err := svc.Create(context.Background(), CreatePoolReq{
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
// B6 (acceptance B5/B6): cross-family CIDR placement → InvalidArgument
// --------------------------------------------------------------------------

// B6a: IPv6 prefix в v4_cidr_blocks → InvalidArgument с verbatim текстом.
func TestAddressPool_B6_Create_V6InV4Slot_InvalidArgument(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	_, err := svc.Create(context.Background(), CreatePoolReq{
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

// B6b: IPv4 prefix в v6_cidr_blocks → InvalidArgument с verbatim текстом.
func TestAddressPool_B6_Create_V4InV6Slot_InvalidArgument(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	_, err := svc.Create(context.Background(), CreatePoolReq{
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
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	// Pre-create dual-stack pool.
	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "dual",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	updated, err := svc.Update(context.Background(), UpdatePoolReq{
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
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "dual",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	updated, err := svc.Update(context.Background(), UpdatePoolReq{
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
// B9: Update без обоих replace_v*=true → no-op (200, прежние CIDR в echo)
// --------------------------------------------------------------------------

func TestAddressPool_B9_Update_NoReplaceFlags_NoOpForCIDR(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "dual-noop",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	// Body с непустыми CIDR-полями, но без replace-флагов → body-array должен
	// быть проигнорирован.
	updated, err := svc.Update(context.Background(), UpdatePoolReq{
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
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "dual",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	_, err = svc.Update(context.Background(), UpdatePoolReq{
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
	got, _ := r.Get(context.Background(), created.ID)
	assert.NotEmpty(t, got.V4CIDRBlocks)
	assert.NotEmpty(t, got.V6CIDRBlocks)
}

// B10-symmetric: попытка очистить единственный непустой family (v4-only pool,
// replaceV4=true v4=[]) → InvalidArgument.
func TestAddressPool_B10_Update_ClearOnlyFamily_InvalidArgument(t *testing.T) {
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "v4-only",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"203.0.113.0/24"},
	})
	require.NoError(t, err)

	_, err = svc.Update(context.Background(), UpdatePoolReq{
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
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "dual-clear",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:ff::/64"},
	})
	require.NoError(t, err)

	updated, err := svc.Update(context.Background(), UpdatePoolReq{
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
	r := newStubAddressPoolRepo()
	svc := makeAddressPoolService(r, newStubBindingRepo(), newStubCloudSelRepo())

	created, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "dual-noop",
		Description:  "old desc",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"198.51.100.0/24"},
		V6CIDRBlocks: []string{"2001:db8:ff::/64"},
	})
	require.NoError(t, err)

	newDesc := "noop update probe"
	updated, err := svc.Update(context.Background(), UpdatePoolReq{
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

// B13: BindAsNetworkDefault на v6-only pool — Bind не валидирует family;
// фильтрация работает на resolve-этапе (см. D-тесты).
func TestAddressPool_B13_BindNetworkDefault_FamilyAgnostic(t *testing.T) {
	r := newStubAddressPoolRepo()
	br := newStubBindingRepo()
	cs := newStubCloudSelRepo()
	svc := makeAddressPoolService(r, br, cs)

	// Создаём v6-only pool и Network. Для тестового Network нам нужен
	// netRepo (через makeAddressPoolService уже передан) — заинжектим row через
	// AddressPoolService.netRepo напрямую невозможно (private field). Воркараунд:
	// предварительно создадим pool, потом insert network в svc.netRepo через
	// рефлексию-эквивалент — но проще создать Network отдельной фабрикой,
	// поэтому свяжем через специальный entry-point: BindAsNetworkDefault сам
	// зовёт netRepo.Get → не должно быть NotFound, поэтому добавим Network.
	pool, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "v6-bind",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: nil,
		V6CIDRBlocks: []string{"2001:db8::/64"},
	})
	require.NoError(t, err)

	// Insert network через netRepo (mock). Доступ — через wrapping helper:
	// makeAddressPoolService использует repomock.NewNetworkRepo()-инстанс, но не
	// возвращает его наружу. Re-write — пересоберём service с явным netRepo.
	nr := repomock.NewNetworkRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	_, err = nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: domain.RcNameVPC("net-v6-bind")})
	require.NoError(t, err)

	sr := repomock.NewSubnetRepo()
	ar := repomock.NewAddressRepo()
	zr := repomock.NewZoneRegistry("ru-central1-c")
	svc2 := NewAddressPoolService(r, br, cs, ar, nr, sr, &repomock.FolderClient{OK: true}, zr)

	// Bind — должно пройти НЕ смотря на то что pool v6-only, и Network может
	// быть v4-предназначен.
	err = svc2.BindAsNetworkDefault(context.Background(), netID, pool.ID)
	require.NoError(t, err, "Bind* should be family-agnostic")

	// Sanity: в bindings record появилась.
	got, gerr := br.GetNetworkDefault(context.Background(), netID)
	require.NoError(t, gerr)
	assert.Equal(t, pool.ID, got)
}

// B13-override: BindAsAddressOverride на pool не той family тоже не
// валидируется — symmetric к BindAsNetworkDefault.
func TestAddressPool_B13_BindAddressOverride_FamilyAgnostic(t *testing.T) {
	r := newStubAddressPoolRepo()
	br := newStubBindingRepo()
	cs := newStubCloudSelRepo()
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	nr := repomock.NewNetworkRepo()
	zr := repomock.NewZoneRegistry("ru-central1-c")
	svc := NewAddressPoolService(r, br, cs, ar, nr, sr, &repomock.FolderClient{OK: true}, zr)

	// v4-only pool.
	pool, err := svc.Create(context.Background(), CreatePoolReq{
		Name:         "v4-bind-override",
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		V4CIDRBlocks: []string{"203.0.113.0/24"},
	})
	require.NoError(t, err)

	// Address с external_ipv6 spec (запрос на v6-allocation, ещё без allocated IP).
	addrID := ids.NewID(ids.PrefixAddress)
	_, err = ar.Insert(context.Background(), &domain.Address{
		ID: addrID, FolderID: "f1",
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
		ExternalIpv6: &domain.ExternalIpv6Spec{ZoneID: "ru-central1-c"},
	})
	require.NoError(t, err)

	// Bind override — должно пройти, family-фильтр будет на resolve.
	err = svc.BindAsAddressOverride(context.Background(), addrID, pool.ID)
	require.NoError(t, err, "Override Bind should be family-agnostic")

	got, gerr := br.GetAddressOverride(context.Background(), addrID)
	require.NoError(t, gerr)
	assert.Equal(t, pool.ID, got)
}

// --------------------------------------------------------------------------
// Sanity: ErrPoolNotResolved sentinel доступен (используется в D-тестах).
// --------------------------------------------------------------------------

var _ = ErrPoolNotResolved
