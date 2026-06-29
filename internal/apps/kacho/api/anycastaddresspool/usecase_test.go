// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// ---- fakes ----

// errProjectClient — ProjectClient, моделирующий недоступность iam (fail-closed).
type errProjectClient struct{}

func (errProjectClient) Exists(context.Context, string) (bool, error) {
	return false, errors.New("iam unavailable")
}

// fakeNetworkReader — узкий NetworkReader для attach (existence + project).
type fakeNetworkReader struct {
	nets map[string]*kachorepo.NetworkRecord
}

func (f *fakeNetworkReader) Get(_ context.Context, id string) (*kachorepo.NetworkRecord, error) {
	n, ok := f.nets[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return n, nil
}

// fakeFilter — per-object ListFilter: explicit allowed-set (или bypass / err).
type fakeFilter struct {
	allowed []string
	bypass  bool
	err     error
}

func (f *fakeFilter) ListAllowedIDs(context.Context, string, string, string) ([]string, bool, error) {
	return f.allowed, f.bypass, f.err
}

// ---- builders ----

func okProject() *repomock.ProjectClient { return &repomock.ProjectClient{OK: true} }

func createUC(kr *kachomock.Repository, pc ProjectClient, or *repomock.OpsRepo) *CreateAnycastAddressPoolUseCase {
	return NewCreateAnycastAddressPoolUseCase(kr, pc, or)
}

func poolFromOp(t *testing.T, op *operations.Operation) *vpcv1.AnycastAddressPool {
	t.Helper()
	require.NotNil(t, op.Response, "operation response must carry the pool")
	var p vpcv1.AnycastAddressPool
	require.NoError(t, op.Response.UnmarshalTo(&p))
	return &p
}

// createPool — helper: создать пул и дождаться Operation done. Возвращает proto.
func createPool(t *testing.T, kr *kachomock.Repository, or *repomock.OpsRepo, in CreateInput) *vpcv1.AnycastAddressPool {
	t.Helper()
	op, err := createUC(kr, okProject(), or).Execute(context.Background(), in)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error, "create should succeed")
	return poolFromOp(t, saved)
}

func within(t *testing.T, reserved, block string) bool {
	t.Helper()
	return netip.MustParsePrefix(reserved).Overlaps(netip.MustParsePrefix(block))
}

// =====================================================================
// Группа A — Create + валидация
// =====================================================================

// GWT-01: Create — platform-assigned IPv4-пул (happy).
func TestCreate_PlatformAssignedIPv4(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	p := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Name: "internal-vips",
		Scope: domain.AnycastScopeInternal, IPVersion: domain.IpVersionIPv4,
	})
	assert.True(t, len(p.Id) > 0 && p.Id[:3] == ids.PrefixAnycastPool)
	assert.Equal(t, vpcv1.AnycastAddressPool_INTERNAL, p.Scope)
	assert.Equal(t, vpcv1.Address_IPV4, p.IpVersion)
	assert.Equal(t, vpcv1.AnycastAddressPool_ACTIVE, p.Status)
	assert.False(t, p.IsDefault)
	require.NotEmpty(t, p.CidrBlocks)
	assert.True(t, within(t, reservedV4, p.CidrBlocks[0]), "assigned block must be within reserved /10")
}

// GWT-02: Create — BYO cidr_blocks из reserved (happy).
func TestCreate_BYOReserved(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	p := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	assert.Equal(t, []string{"100.64.12.0/22"}, p.CidrBlocks)
}

// GWT-03: Create — IPv6 provider-ULA пул (happy).
func TestCreate_PlatformAssignedIPv6(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	p := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal, IPVersion: domain.IpVersionIPv6,
	})
	assert.Equal(t, vpcv1.Address_IPV6, p.IpVersion)
	require.NotEmpty(t, p.CidrBlocks)
	assert.True(t, within(t, reservedV6, p.CidrBlocks[0]), "assigned v6 block must be within provider-ULA /48")
}

// GWT-04: Create — cidr_blocks вне reserved-диапазона (negative).
func TestCreate_OutOfReserved(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	_, err := createUC(kr, okProject(), or).Execute(context.Background(), CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"10.0.0.0/22"},
	})
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t,
		"Illegal argument cidr_blocks: anycast address pool CIDR must be within the reserved 100.64.0.0/10 range",
		status.Convert(err).Message())
}

// GWT-05: Create — malformed cidr_blocks element (negative).
func TestCreate_MalformedCIDR(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	_, err := createUC(kr, okProject(), or).Execute(context.Background(), CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"not-a-cidr"},
	})
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t, "Illegal argument cidr_blocks", status.Convert(err).Message())
}

// GWT-06: Create — scope=EXTERNAL вне фазы 1 (negative).
func TestCreate_ExternalScopeRejected(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	_, err := createUC(kr, okProject(), or).Execute(context.Background(), CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeExternal, IPVersion: domain.IpVersionIPv4,
	})
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t, "Illegal argument scope: only INTERNAL is supported", status.Convert(err).Message())
}

// GWT-07: Create — ip_version не совпадает с family блока (negative).
func TestCreate_FamilyMismatch(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	_, err := createUC(kr, okProject(), or).Execute(context.Background(), CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv6, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t, "Illegal argument cidr_blocks: does not match ip_version", status.Convert(err).Message())
}

// GWT-08: Create — iam недоступен → fail-closed Unavailable; пул не создан.
func TestCreate_IAMUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	op, err := createUC(kr, errProjectClient{}, or).Execute(context.Background(), CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal, IPVersion: domain.IpVersionIPv4,
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.Unavailable), saved.Error.Code)
	// Пул не создан.
	assert.Empty(t, kr.AnycastPools())
}

// GWT-09: Get — несуществующий пул (negative).
func TestGet_NotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	id := "aap00000000000000000"
	_, err := NewGetAnycastAddressPoolUseCase(kr, nil).Execute(context.Background(), "", id)
	requireCode(t, err, codes.NotFound)
	assert.Equal(t, "Anycast address pool "+id+" not found", status.Convert(err).Message())
}

// GWT-10: Get — malformed id → sync InvalidArgument первым стейтментом.
func TestGet_MalformedID(t *testing.T) {
	kr := kachomock.NewRepository()
	_, err := NewGetAnycastAddressPoolUseCase(kr, nil).Execute(context.Background(), "", "НЕ-id")
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t, "invalid anycast address pool id 'НЕ-id'", status.Convert(err).Message())
}

// GWT-11: Update — mutable поля через update_mask (happy).
func TestUpdate_MutableFields(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Name: "orig", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	op, err := NewUpdateAnycastAddressPoolUseCase(kr, or).Execute(context.Background(), UpdateInput{
		ID: created.Id, UpdateMask: []string{"name", "labels"},
		Name: "renamed", Labels: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	p := poolFromOp(t, saved)
	assert.Equal(t, "renamed", p.Name)
	assert.Equal(t, map[string]string{"env": "prod"}, p.Labels)
	// Immutable неизменны.
	assert.Equal(t, []string{"100.64.12.0/22"}, p.CidrBlocks)
	assert.Equal(t, vpcv1.AnycastAddressPool_INTERNAL, p.Scope)
	assert.Equal(t, vpcv1.Address_IPV4, p.IpVersion)
	assert.False(t, p.IsDefault)
}

// GWT-12: Update — immutable/unknown поле в update_mask (negative).
func TestUpdate_ImmutableAndUnknownMask(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewUpdateAnycastAddressPoolUseCase(kr, or)
	for _, f := range []string{"cidr_blocks", "scope", "ip_version", "is_default"} {
		_, err := uc.Execute(context.Background(), UpdateInput{ID: "aap00000000000000001", UpdateMask: []string{f}})
		requireCode(t, err, codes.InvalidArgument)
		assert.Equal(t, f+" is immutable after AnycastAddressPool.Create", status.Convert(err).Message())
	}
	// unknown field → InvalidArgument.
	_, err := uc.Execute(context.Background(), UpdateInput{ID: "aap00000000000000001", UpdateMask: []string{"bogus"}})
	requireCode(t, err, codes.InvalidArgument)
}

// GWT-13: List — только свои пулы; cursor-пагинация (happy + authz).
func TestList_OwnOnlyWithPagination(t *testing.T) {
	kr := kachomock.NewRepository()
	base := time.Now().UTC()
	seedPool(kr, "aap00000000000000a01", "prj-A", base)
	seedPool(kr, "aap00000000000000a02", "prj-A", base.Add(time.Second))
	seedPool(kr, "aap00000000000000b03", "prj-B", base)
	seedPool(kr, "aap00000000000000def", "kacho-system", base) // is_default

	uc := NewListAnycastAddressPoolsUseCase(kr, nil)
	page1, next, err := uc.Execute(context.Background(), "", AnycastAddressPoolFilter{ProjectID: "prj-A"}, Pagination{PageSize: 1})
	require.NoError(t, err)
	require.Len(t, page1, 1)
	assert.Equal(t, "aap00000000000000a01", page1[0].ID)
	require.NotEmpty(t, next)

	page2, _, err := uc.Execute(context.Background(), "", AnycastAddressPoolFilter{ProjectID: "prj-A"}, Pagination{PageSize: 1, PageToken: next})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "aap00000000000000a02", page2[0].ID)

	// Чужой prj-B и admin-owned is_default не видны в выборке prj-A.
	all, _, err := uc.Execute(context.Background(), "", AnycastAddressPoolFilter{ProjectID: "prj-A"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	for _, p := range all {
		assert.Equal(t, "prj-A", p.ProjectID)
	}
}

// GWT-14: List — garbage page_token (negative).
func TestList_GarbageToken(t *testing.T) {
	kr := kachomock.NewRepository()
	_, _, err := NewListAnycastAddressPoolsUseCase(kr, nil).Execute(context.Background(), "",
		AnycastAddressPoolFilter{ProjectID: "prj-A"}, Pagination{PageToken: "$$garbage$$"})
	requireCode(t, err, codes.InvalidArgument)
}

// GWT-15: Delete — пустой пул (happy).
func TestDelete_Empty(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal, IPVersion: domain.IpVersionIPv4,
	})
	op, err := NewDeleteAnycastAddressPoolUseCase(kr, or).Execute(context.Background(), created.Id)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	_, gerr := NewGetAnycastAddressPoolUseCase(kr, nil).Execute(context.Background(), "", created.Id)
	requireCode(t, gerr, codes.NotFound)
}

// GWT-16: Delete — пул не пуст (negative).
func TestDelete_NotEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	kr.SeedAnycastAttachment(created.Id, "net-1")
	op, err := NewDeleteAnycastAddressPoolUseCase(kr, or).Execute(context.Background(), created.Id)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
	assert.Equal(t, "anycast address pool is not empty", saved.Error.Message)
}

// =====================================================================
// Группа B — attach / detach
// =====================================================================

func nets(projectByID map[string]string) *fakeNetworkReader {
	m := make(map[string]*kachorepo.NetworkRecord, len(projectByID))
	for id, pid := range projectByID {
		m[id] = &kachorepo.NetworkRecord{Network: domain.Network{ID: id, ProjectID: pid}}
	}
	return &fakeNetworkReader{nets: m}
}

// GWT-17/18: attachNetwork — happy + идемпотентность.
func TestAttach_HappyAndIdempotent(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	uc := NewAttachNetworkUseCase(kr, nets(map[string]string{"net00000000000000001": "prj-A"}), or)

	op, err := uc.Execute(context.Background(), created.Id, "net00000000000000001")
	require.NoError(t, err)
	require.Nil(t, repomock.AwaitOpDone(t, or, op.ID).Error)
	assert.Equal(t, []string{"net00000000000000001"}, kr.AnycastAttachments(created.Id))

	// Повторный attach — no-op, без дублей.
	op2, err := uc.Execute(context.Background(), created.Id, "net00000000000000001")
	require.NoError(t, err)
	require.Nil(t, repomock.AwaitOpDone(t, or, op2.ID).Error)
	assert.Equal(t, []string{"net00000000000000001"}, kr.AnycastAttachments(created.Id))
}

// GWT-19: attachNetwork — пул/сеть отсутствуют, malformed id (negative).
func TestAttach_MissingAndMalformed(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	uc := NewAttachNetworkUseCase(kr, nets(map[string]string{"net00000000000000001": "prj-A"}), or)

	// отсутствующий пул → NotFound.
	_, err := uc.Execute(context.Background(), "aap00000000000000999", "net00000000000000001")
	requireCode(t, err, codes.NotFound)

	// отсутствующая сеть → NotFound "Network ... not found".
	_, err = uc.Execute(context.Background(), created.Id, "net00000000000000ABS")
	requireCode(t, err, codes.NotFound)

	// malformed pool id → sync InvalidArgument первым стейтментом.
	_, err = uc.Execute(context.Background(), "not-an-id", "net00000000000000001")
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t, "invalid anycast address pool id 'not-an-id'", status.Convert(err).Message())

	// malformed network id → InvalidArgument.
	_, err = uc.Execute(context.Background(), created.Id, "bad-net")
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t, "invalid network id 'bad-net'", status.Convert(err).Message())
}

// GWT-20: attachNetwork — пул и сеть в разных проектах (negative).
func TestAttach_CrossProject(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	uc := NewAttachNetworkUseCase(kr, nets(map[string]string{"net00000000000000002": "prj-B"}), or)
	_, err := uc.Execute(context.Background(), created.Id, "net00000000000000002")
	requireCode(t, err, codes.InvalidArgument)
	assert.Equal(t,
		"Illegal argument networkId: network and anycast address pool must belong to the same project",
		status.Convert(err).Message())
}

// GWT-25: detachNetwork — снимает claim (happy).
func TestDetach_Happy(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	kr.SeedAnycastAttachment(created.Id, "net00000000000000001")
	op, err := NewDetachNetworkUseCase(kr, or).Execute(context.Background(), created.Id, "net00000000000000001")
	require.NoError(t, err)
	require.Nil(t, repomock.AwaitOpDone(t, or, op.ID).Error)
	assert.Empty(t, kr.AnycastAttachments(created.Id))
}

// GWT-26: detachNetwork — есть живые аллокации; идемпотентность (negative + edge).
func TestDetach_LiveAllocationsAndIdempotent(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	kr.SeedAnycastAttachment(created.Id, "net00000000000000001")
	kr.SeedAnycastAllocation(created.Id, "net00000000000000001", 1)

	op, err := NewDetachNetworkUseCase(kr, or).Execute(context.Background(), created.Id, "net00000000000000001")
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
	assert.Equal(t, "anycast address pool has allocated addresses in network", saved.Error.Message)

	// detach непривязанного пула → no-op (done без error).
	op2, err := NewDetachNetworkUseCase(kr, or).Execute(context.Background(), created.Id, "net00000000000000777")
	require.NoError(t, err)
	require.Nil(t, repomock.AwaitOpDone(t, or, op2.ID).Error)
}

// =====================================================================
// Группа D — AuthZ existence-hiding
// =====================================================================

// GWT-36/37: Get/Update/Delete/attach чужого или is_default пула → NotFound
// (existence-hiding deny→404). Все мутации проходят через тот же get-гейт.
func TestExistenceHiding_ForeignPoolDeny404(t *testing.T) {
	kr := kachomock.NewRepository()
	base := time.Now().UTC()
	seedPool(kr, "aap00000000000000a01", "prj-A", base) // свой
	seedPool(kr, "aap00000000000000b03", "prj-B", base) // чужой
	seedPool(kr, "aap00000000000000def", "kacho-system", base)

	// subject видит только свой пул.
	filter := &fakeFilter{allowed: []string{"aap00000000000000a01"}}
	getUC := NewGetAnycastAddressPoolUseCase(kr, filter)

	// свой пул виден.
	_, err := getUC.Execute(context.Background(), "user:alice", "aap00000000000000a01")
	require.NoError(t, err)

	// чужой и admin-owned (is_default) → NotFound (deny→404, не PermissionDenied).
	for _, id := range []string{"aap00000000000000b03", "aap00000000000000def"} {
		_, err := getUC.Execute(context.Background(), "user:alice", id)
		requireCode(t, err, codes.NotFound)
		assert.Equal(t, "Anycast address pool "+id+" not found", status.Convert(err).Message())
	}
}

// ---- helpers ----

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %v", err)
	assert.Equal(t, code, st.Code())
}

func seedPool(kr *kachomock.Repository, id, projectID string, createdAt time.Time) {
	kr.SeedAnycastPool(&kachorepo.AnycastAddressPoolRecord{
		AnycastAddressPool: domain.AnycastAddressPool{
			ID: id, ProjectID: projectID, Scope: domain.AnycastScopeInternal,
			IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
			Status: domain.AnycastPoolStatusActive,
		},
		CreatedAt: createdAt,
	})
}
