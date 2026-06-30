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

// Тесты BYO-привязки anycast-Address (SetAnycastReference, scope #4, GWT-32..34):
// guard ownership/family (анти-oracle) + used_by-CAS.

func seedAnycastAddr(ar *repomock.AddressRepo, projectID string, family domain.IpVersion) *kachorepo.AddressRecord {
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

// GWT-32 — BYO happy: свободный anycast-Address своего проекта/семейства → bind.
func TestSetAnycastReference_OK(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedAnycastAddr(ar, "prj-A", domain.IpVersionIPv4)

	ref, err := svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
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

// GWT-33 — ownership guard: чужой проект → generic InvalidArgument (анти-oracle).
func TestSetAnycastReference_ForeignProject(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedAnycastAddr(ar, "prj-B", domain.IpVersionIPv4) // чужой проект

	_, err := svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
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

// GWT-33 — family guard: другое семейство → generic InvalidArgument.
func TestSetAnycastReference_WrongFamily(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedAnycastAddr(ar, "prj-A", domain.IpVersionIPv6) // v6, а ожидается v4

	_, err := svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
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

// GWT-33 — не-anycast адрес → generic InvalidArgument (BYO только для anycast).
func TestSetAnycastReference_NotAnycast(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:           ids.NewID(ids.PrefixAddress),
		ProjectID:    "prj-A",
		IpVersion:    domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{Address: "203.0.113.5"},
	}}
	ar.Seed(rec)

	_, err := svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
		AddressID: rec.ID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		ExpectProjectID: "prj-A", ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "Illegal argument addressId", st.Message())
}

// GWT-33 — несуществующий адрес → generic InvalidArgument (без подтверждения).
func TestSetAnycastReference_NotFoundIsGeneric(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)

	_, err := svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
		AddressID: ids.NewID(ids.PrefixAddress), ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		ExpectProjectID: "prj-A", ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "Illegal argument addressId", st.Message())
}

// GWT-34 — used_by-CAS: второй referrer на занятый адрес → "address is already in use".
func TestSetAnycastReference_UsedByCAS(t *testing.T) {
	ar := repomock.NewAddressRepo()
	svc := NewService(ar)
	a := seedAnycastAddr(ar, "prj-A", domain.IpVersionIPv4)

	_, err := svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
		AddressID: a.ID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		ExpectProjectID: "prj-A", ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.NoError(t, err)

	_, err = svc.SetAnycastReference(context.Background(), SetAnycastReferenceReq{
		AddressID: a.ID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000002",
		ExpectProjectID: "prj-A", ExpectIPVersion: domain.IpVersionIPv4,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t, "address is already in use", st.Message())
}
