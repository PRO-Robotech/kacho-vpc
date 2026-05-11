package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

func makeAddressService() (*svc.AddressService, *mockOpsRepo) {
	ar := portmock.NewAddressRepo()
	sr := portmock.NewSubnetRepo()
	or := newMockOpsRepo()
	return svc.NewAddressService(ar, sr, newMockFolderClient(true), or, nil), or
}

func TestAddressHandler_Get_InvalidArg(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressHandler_Get_NotFound(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: ids.NewUID()})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAddressHandler_List_Empty(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	resp, err := h.List(context.Background(), &vpcv1.ListAddressesRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Addresses)
}

func TestAddressHandler_Create_External_OK(t *testing.T) {
	addrSvc, or := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	op, err := h.Create(context.Background(), &vpcv1.CreateAddressRequest{
		FolderId: "f1",
		Name:     "my-ip",
		AddressSpec: &vpcv1.CreateAddressRequest_ExternalIpv4AddressSpec{
			ExternalIpv4AddressSpec: &vpcv1.ExternalIpv4AddressSpec{
				Address: "203.0.113.20",
				ZoneId:  "ru-central1-a",
			},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.Id)

	saved := awaitOpDone(t, or, op.Id)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestAddressHandler_Delete_InvalidArg(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	_, err := h.Delete(context.Background(), &vpcv1.DeleteAddressRequest{AddressId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
