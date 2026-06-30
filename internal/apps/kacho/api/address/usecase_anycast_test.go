// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// Тесты anycast-аллокации через AddressService.Create (scope #4, GWT-27..35).
// Use-case работает через kachomock (in-memory CQRS): сеть/пул/attach сидятся
// fixture-методами; глобально-уникальный expression-индекс
// addresses_anycast_host_uniq (anycast->>'address') моделируется mock'ом
// SetAnycast (дубль host → ErrAlreadyExists → exhausted).

func seedAnycastNet(kr *kachomock.Repository, id string) {
	kr.SeedNetwork(&kachorepo.NetworkRecord{Network: domain.Network{
		ID: id, ProjectID: "f1", Name: domain.RcNameVPC("net"),
	}})
}

func seedAnycastPoolRec(kr *kachomock.Repository, id, projectID string, blocks []string, isDefault bool) {
	kr.SeedAnycastPool(&kachorepo.AnycastAddressPoolRecord{
		AnycastAddressPool: domain.AnycastAddressPool{
			ID:         id,
			ProjectID:  projectID,
			Scope:      domain.AnycastScopeInternal,
			IPVersion:  domain.IpVersionIPv4,
			CIDRBlocks: blocks,
			IsDefault:  isDefault,
			Status:     domain.AnycastPoolStatusActive,
		},
	})
}

func anycastCreateUC(kr *kachomock.Repository, or *repomock.OpsRepo) *CreateAddressUseCase {
	return NewCreateAddressUseCase(kr, repomock.NewSubnetRepo(), &repomock.ProjectClient{OK: true}, or, nil)
}

// GWT-27 / GWT-35 — auto-alloc из платформенного is_default-пула (без явного attach).
func TestAnycast_AutoAlloc_FromDefault(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	seedAnycastNet(kr, netID)
	seedAnycastPoolRec(kr, ids.NewID(ids.PrefixAnycastPool), "kacho-system", []string{"100.64.0.0/16"}, true)
	uc := anycastCreateUC(kr, or)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:   "f1",
		AnycastSpec: &AnycastAddrSpec{NetworkID: netID, IpVersion: domain.IpVersionIPv4},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error, "auto-alloc из is_default должен пройти без error")

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	require.NotNil(t, addrs[0].Anycast)
	assert.Equal(t, netID, addrs[0].Anycast.NetworkID)
	assert.True(t, addrs[0].Used, "anycast-Address активен сразу (used=true)")
	host, perr := netip.ParseAddr(addrs[0].Anycast.Address)
	require.NoError(t, perr)
	assert.True(t, netip.MustParsePrefix("100.64.0.0/16").Contains(host), "host внутри CIDR пула")
}

// GWT-28 — auto-alloc из явно приаттаченного пула.
func TestAnycast_AutoAlloc_FromAttachedPool(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	poolID := ids.NewID(ids.PrefixAnycastPool)
	seedAnycastNet(kr, netID)
	seedAnycastPoolRec(kr, poolID, "f1", []string{"100.64.12.0/22"}, false)
	kr.SeedAnycastAttachment(poolID, netID)
	uc := anycastCreateUC(kr, or)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:   "f1",
		AnycastSpec: &AnycastAddrSpec{NetworkID: netID, IpVersion: domain.IpVersionIPv4, AnycastPoolID: poolID},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	require.NotNil(t, addrs[0].Anycast)
	assert.Equal(t, poolID, addrs[0].Anycast.AnycastPoolID)
	host, perr := netip.ParseAddr(addrs[0].Anycast.Address)
	require.NoError(t, perr)
	assert.True(t, netip.MustParsePrefix("100.64.12.0/22").Contains(host))
}

// GWT-29 — пул не доступен в сети (не приаттачен, не is_default).
func TestAnycast_AutoAlloc_PoolNotAvailable(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	poolID := ids.NewID(ids.PrefixAnycastPool)
	seedAnycastNet(kr, netID)
	seedAnycastPoolRec(kr, poolID, "f1", []string{"100.64.12.0/22"}, false)
	// НЕ приаттачен к netID.
	uc := anycastCreateUC(kr, or)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:   "f1",
		AnycastSpec: &AnycastAddrSpec{NetworkID: netID, IpVersion: domain.IpVersionIPv4, AnycastPoolID: poolID},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
	assert.Equal(t, "anycast address pool is not available in network", saved.Error.Message)
}

// GWT-30 — пул исчерпан → generic-ошибка (анти-oracle: без exhausted/capacity).
func TestAnycast_AutoAlloc_Exhausted_Generic(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	poolID := ids.NewID(ids.PrefixAnycastPool)
	seedAnycastNet(kr, netID)
	seedAnycastPoolRec(kr, poolID, "f1", []string{"100.64.200.0/32"}, false)
	kr.SeedAnycastAttachment(poolID, netID)
	// Единственный host /32 уже выдан — занимаем 100.64.200.0.
	kr.SeedAddress(&kachorepo.AddressRecord{Address: domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		ProjectID: "f1",
		Anycast:   &domain.AnycastSpec{NetworkID: netID, Address: "100.64.200.0", AnycastPoolID: poolID},
	}})
	uc := anycastCreateUC(kr, or)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:   "f1",
		AnycastSpec: &AnycastAddrSpec{NetworkID: netID, IpVersion: domain.IpVersionIPv4, AnycastPoolID: poolID},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
	assert.Equal(t, "could not allocate anycast address", saved.Error.Message)
	assert.NotContains(t, saved.Error.Message, "exhausted")
	assert.NotContains(t, saved.Error.Message, "capacity")
	assert.NotContains(t, saved.Error.Message, "free")
}

// Malformed network id → sync InvalidArgument первым стейтментом (до Operation).
func TestAnycast_MalformedNetworkID_Sync(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := anycastCreateUC(kr, or)

	_, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:   "f1",
		AnycastSpec: &AnycastAddrSpec{NetworkID: "not-a-net", IpVersion: domain.IpVersionIPv4},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// Сеть отсутствует → async NotFound "Network <id> not found".
func TestAnycast_NetworkNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	// сеть НЕ сидится; default-пул есть.
	seedAnycastPoolRec(kr, ids.NewID(ids.PrefixAnycastPool), "kacho-system", []string{"100.64.0.0/16"}, true)
	uc := anycastCreateUC(kr, or)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:   "f1",
		AnycastSpec: &AnycastAddrSpec{NetworkID: netID, IpVersion: domain.IpVersionIPv4},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.NotFound), saved.Error.Code)
}

// Handler dispatch: anycast_address_spec proto → use-case → anycast-Address.
func TestAnycast_Handler_Create(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: true}
	netID := ids.NewID(ids.PrefixNetwork)
	poolID := ids.NewID(ids.PrefixAnycastPool)
	seedAnycastNet(kr, netID)
	seedAnycastPoolRec(kr, poolID, "f1", []string{"100.64.12.0/22"}, false)
	kr.SeedAnycastAttachment(poolID, netID)
	h := makeHandler(t, kr, sr, or, fc)

	op, err := h.Create(context.Background(), &vpcv1.CreateAddressRequest{
		ProjectId: "f1",
		AddressSpec: &vpcv1.CreateAddressRequest_AnycastAddressSpec{
			AnycastAddressSpec: &vpcv1.AnycastAddressSpec{
				NetworkId:     netID,
				IpVersion:     vpcv1.Address_IPV4,
				AnycastPoolId: poolID,
			},
		},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.Nil(t, saved.Error)

	resp, _ := h.List(context.Background(), &vpcv1.ListAddressesRequest{ProjectId: "f1"})
	require.Len(t, resp.Addresses, 1)
	anycast := resp.Addresses[0].GetAnycastAddress()
	require.NotNil(t, anycast, "вывод Address должен нести anycast_address")
	assert.Equal(t, netID, anycast.GetNetworkId())
	assert.Equal(t, poolID, anycast.GetAnycastPoolId())
}
