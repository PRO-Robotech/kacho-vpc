package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

func TestOperationHandler_Get_InvalidArg(t *testing.T) {
	or := newMockOpsRepo()
	h := NewOperationHandler(or)

	_, err := h.Get(context.Background(), &operationpb.GetOperationRequest{OperationId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestOperationHandler_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	h := NewOperationHandler(or)

	_, err := h.Get(context.Background(), &operationpb.GetOperationRequest{OperationId: ids.NewUID()})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestOperationHandler_Get_OK(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
	h_net := NewNetworkHandler(networkSvc)

	op, err := h_net.Create(context.Background(), &vpcv1.CreateNetworkRequest{FolderId: "f1", Name: "net1"})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	h := NewOperationHandler(or)
	gotten, err := h.Get(context.Background(), &operationpb.GetOperationRequest{OperationId: op.Id})
	require.NoError(t, err)
	assert.Equal(t, op.Id, gotten.Id)
	assert.True(t, gotten.Done)
}

func TestOperationHandler_Cancel_InvalidArg(t *testing.T) {
	or := newMockOpsRepo()
	h := NewOperationHandler(or)

	_, err := h.Cancel(context.Background(), &operationpb.CancelOperationRequest{OperationId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestOperationHandler_Cancel_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	h := NewOperationHandler(or)

	_, err := h.Cancel(context.Background(), &operationpb.CancelOperationRequest{OperationId: ids.NewUID()})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}
