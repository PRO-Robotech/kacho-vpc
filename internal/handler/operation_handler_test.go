package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
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
	// Wave 3a pilot (KAC-94): NetworkHandler удалён из этого пакета — тест на
	// happy-path OperationService.Get больше не использует Network create-flow;
	// вместо этого вручную кладём заранее завершённый Operation в opsRepo и
	// проверяем, что OperationHandler.Get отдаёт его обратно.
	or := newMockOpsRepo()
	op, err := operations.New(
		ids.PrefixOperationVPC,
		"unit-test op",
		&vpcv1.CreateNetworkMetadata{NetworkId: "test-net"},
	)
	require.NoError(t, err)
	require.NoError(t, or.Create(context.Background(), op))
	require.NoError(t, or.MarkDone(context.Background(), op.ID, nil))

	h := NewOperationHandler(or)
	gotten, err := h.Get(context.Background(), &operationpb.GetOperationRequest{OperationId: op.ID})
	require.NoError(t, err)
	assert.Equal(t, op.ID, gotten.Id)
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
