// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addressref

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// BYO ownership/family guard в SetAddressReference: consumer (напр. nlb) приносит
// «свой» Address и передаёт expect_project_id/expect_ip_version. vpc проверяет
// ownership/family server-side в рамках used_by-CAS; несовпадение → generic
// InvalidArgument "Illegal argument addressId" (анти-oracle: без подтверждения
// чужого ownership/существования). Пустые expect_* → проверка пропускается.

func seedGuardAddr(ar *repomock.AddressRepo, projectID string, family domain.IpVersion) *kachorepo.AddressRecord {
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		ProjectID: projectID,
		Type:      domain.AddressTypeInternal,
		IpVersion: family,
		Anycast:   &domain.AnycastSpec{NetworkID: ids.NewID(ids.PrefixNetwork), Address: "100.64.12.5"},
	}}
	ar.Seed(rec)
	return rec
}

// happy: совпадающие expect_project_id/expect_ip_version → bind проходит.
func TestSetAddressReference_Guard_OK(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedGuardAddr(ar, "prj-A", domain.IpVersionIPv4)

	ref, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID:       a.ID,
		ReferrerType:    "nlb_load_balancer",
		ReferrerID:      "lb000000000000001",
		ExpectProjectID: "prj-A",
		ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.NoError(t, err)
	assert.Equal(t, a.ID, ref.AddressID)
	got, _ := ar.Get(context.Background(), a.ID)
	assert.True(t, got.Used)
}

// ownership guard: чужой проект → generic InvalidArgument (анти-oracle).
func TestSetAddressReference_Guard_ForeignProject(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedGuardAddr(ar, "prj-B", domain.IpVersionIPv4) // чужой проект

	_, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID:       a.ID,
		ReferrerType:    "nlb_load_balancer",
		ReferrerID:      "lb000000000000001",
		ExpectProjectID: "prj-A",
		ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "Illegal argument addressId", st.Message())

	// used не выставлен — bind не состоялся.
	got, _ := ar.Get(context.Background(), a.ID)
	assert.False(t, got.Used)
}

// family guard: другое семейство → generic InvalidArgument.
func TestSetAddressReference_Guard_WrongFamily(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedGuardAddr(ar, "prj-A", domain.IpVersionIPv6) // v6, ожидается v4

	_, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID:       a.ID,
		ReferrerType:    "nlb_load_balancer",
		ReferrerID:      "lb000000000000001",
		ExpectProjectID: "prj-A",
		ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "Illegal argument addressId", st.Message())
}

// несуществующий адрес под guard'ом → generic InvalidArgument (без подтверждения
// несуществования — анти-oracle), НЕ NotFound.
func TestSetAddressReference_Guard_NotFoundIsGeneric(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)

	_, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID:       ids.NewID(ids.PrefixAddress),
		ReferrerType:    "nlb_load_balancer",
		ReferrerID:      "lb000000000000001",
		ExpectProjectID: "prj-A",
		ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "Illegal argument addressId", st.Message())
}

// back-compat: пустые expect_* → проверка пропускается, used_by-CAS как раньше
// (bind проходит для существующего адреса; несуществующий → NotFound).
func TestSetAddressReference_Guard_EmptyBackCompat(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedGuardAddr(ar, "prj-A", domain.IpVersionIPv4)

	ref, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID:    a.ID,
		ReferrerType: "compute_instance",
		ReferrerID:   "epdvm0000000000001",
	})
	require.NoError(t, err)
	assert.Equal(t, a.ID, ref.AddressID)
	got, _ := ar.Get(context.Background(), a.ID)
	assert.True(t, got.Used)

	// без guard'а несуществующий адрес → NotFound (не маскируется в generic).
	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID:    ids.NewID(ids.PrefixAddress),
		ReferrerType: "compute_instance",
		ReferrerID:   "epdvm0000000000001",
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// used_by-CAS под совпадающим guard'ом: второй referrer на занятый адрес →
// FailedPrecondition (финальный гейт против двойной аллокации).
func TestSetAddressReference_Guard_UsedByCAS(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedGuardAddr(ar, "prj-A", domain.IpVersionIPv4)

	_, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID: a.ID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		ExpectProjectID: "prj-A", ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.NoError(t, err)

	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID: a.ID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000002",
		ExpectProjectID: "prj-A", ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}
