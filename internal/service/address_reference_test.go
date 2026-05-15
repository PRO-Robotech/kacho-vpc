package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func seedAddrForRef(ar *mockAddressRepo) *domain.Address {
	a := &domain.Address{
		ID:           ids.NewID(ids.PrefixAddress),
		FolderID:     "f1",
		CreatedAt:    time.Now().UTC(),
		Name:         "ref-ip",
		Type:         domain.AddressTypeExternal,
		IpVersion:    domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{Address: "203.0.113.5", ZoneID: "ru-central1-a"},
	}
	ar.Seed(a)
	return a
}

func TestAddressService_SetAddressReference_OK(t *testing.T) {
	ar := newMockAddressRepo()
	svc := NewAddressService(ar, newMockSubnetRepo(), newMockFolderClient(true), newMockOpsRepo(), nil)
	a := seedAddrForRef(ar)

	ref, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID: a.ID, ReferrerType: "compute_instance", ReferrerID: "epdvm0000000000001", ReferrerName: "vm-1",
	})
	require.NoError(t, err)
	assert.Equal(t, a.ID, ref.AddressID)
	assert.Equal(t, "compute_instance", ref.ReferrerType)
	assert.Equal(t, "epdvm0000000000001", ref.ReferrerID)
	assert.Equal(t, "vm-1", ref.ReferrerName)

	got, _ := svc.Get(context.Background(), a.ID)
	assert.True(t, got.Used)

	// KAC-88: idempotent re-set с ТЕМ ЖЕ referrer — допустимо (CAS matches),
	// можно обновить referrer_name.
	ref, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID: a.ID, ReferrerType: "compute_instance", ReferrerID: "epdvm0000000000001", ReferrerName: "vm-1-renamed",
	})
	require.NoError(t, err)
	assert.Equal(t, "epdvm0000000000001", ref.ReferrerID)
	assert.Equal(t, "vm-1-renamed", ref.ReferrerName)

	// KAC-88: re-set с ДРУГИМ referrer → FailedPrecondition (CAS fail) —
	// parity с инцидентом KAC-52 (NIC-attach race), исправляет gap G1 из KAC-84.
	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{
		AddressID: a.ID, ReferrerType: "compute_instance", ReferrerID: "epdvm0000000000002",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code(),
		"set-reference к занятому address от чужого referrer → FailedPrecondition")
}

func TestAddressService_SetAddressReference_Validation(t *testing.T) {
	ar := newMockAddressRepo()
	svc := NewAddressService(ar, newMockSubnetRepo(), newMockFolderClient(true), newMockOpsRepo(), nil)
	a := seedAddrForRef(ar)

	// malformed address id
	_, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{AddressID: "garbage", ReferrerType: "compute_instance", ReferrerID: "x"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// missing referrer_type
	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{AddressID: a.ID, ReferrerID: "x"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// missing referrer_id
	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{AddressID: a.ID, ReferrerType: "compute_instance"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// non-existent address
	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{AddressID: ids.NewID(ids.PrefixAddress), ReferrerType: "compute_instance", ReferrerID: "x"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAddressService_GetAddressReference(t *testing.T) {
	ar := newMockAddressRepo()
	svc := NewAddressService(ar, newMockSubnetRepo(), newMockFolderClient(true), newMockOpsRepo(), nil)
	a := seedAddrForRef(ar)

	// no referrer yet → NotFound
	_, err := svc.GetAddressReference(context.Background(), a.ID)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())

	_, err = svc.SetAddressReference(context.Background(), SetAddressReferenceReq{AddressID: a.ID, ReferrerType: "compute_instance", ReferrerID: "epdvm0000000000001"})
	require.NoError(t, err)

	ref, err := svc.GetAddressReference(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, "epdvm0000000000001", ref.ReferrerID)
}

func TestAddressService_ClearAddressReference(t *testing.T) {
	ar := newMockAddressRepo()
	svc := NewAddressService(ar, newMockSubnetRepo(), newMockFolderClient(true), newMockOpsRepo(), nil)
	a := seedAddrForRef(ar)

	_, err := svc.SetAddressReference(context.Background(), SetAddressReferenceReq{AddressID: a.ID, ReferrerType: "compute_instance", ReferrerID: "epdvm0000000000001"})
	require.NoError(t, err)

	require.NoError(t, svc.ClearAddressReference(context.Background(), a.ID))
	got, _ := svc.Get(context.Background(), a.ID)
	assert.False(t, got.Used)
	_, err = svc.GetAddressReference(context.Background(), a.ID)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())

	// idempotent no-op
	require.NoError(t, svc.ClearAddressReference(context.Background(), a.ID))

	// non-existent address → NotFound
	err = svc.ClearAddressReference(context.Background(), ids.NewID(ids.PrefixAddress))
	st, _ = status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}
