package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
)

func TestNetworkService_Get_NotFound(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), newMockOpsRepo(), nil)
	// well-formed-но-несуществующий id → NotFound (не InvalidArgument).
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixNetwork))
	require.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNetworkService_Create_ValidationError(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), newMockOpsRepo(), nil)

	// Пустой folder_id
	_, err := svc.Create(context.Background(), CreateNetworkReq{Name: "test"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Пустое name теперь допускается (YC permissive policy для VPC) — empty
	// name проходит sync-валидацию. Поэтому проверяем имя с invalid-pattern,
	// например начинающееся с цифры.
	_, err = svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "1bad"})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkService_Create_FolderNotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(false), or, nil)
	op, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "net1"})
	require.NoError(t, err) // Operation создаётся, ошибка внутри goroutine
	require.NotNil(t, op)
	awaitOpDone(t, or, op.ID)
}

func TestNetworkService_Create_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	svc := NewNetworkService(nr, nil, nil, nil, newMockFolderClient(true), or, nil)

	op, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "net1", Description: "desc"})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := awaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestNetworkService_List_Empty(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), newMockOpsRepo(), nil)
	nets, token, err := svc.List(context.Background(), NetworkFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nets)
	assert.Empty(t, token)
}

func TestNetworkService_Delete_ValidationError(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), newMockOpsRepo(), nil)
	_, err := svc.Delete(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestNetworkService_Delete_ResponseIsEmpty проверяет TODO #1 + #29:
// Operation.response для Delete должен быть google.protobuf.Empty, не Metadata.
// Защищает от регрессии (исторически кладали DeleteNetworkMetadata в response).
func TestNetworkService_Delete_ResponseIsEmpty(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	svc := NewNetworkService(nr, nil, nil, nil, newMockFolderClient(true), or, nil)

	createOp, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "del-resp-test"})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.ID)

	nets, _, _ := svc.List(context.Background(), NetworkFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, nets, 1)

	delOp, err := svc.Delete(context.Background(), nets[0].ID)
	require.NoError(t, err)
	saved := awaitOpDone(t, or, delOp.ID)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)

	var empty emptypb.Empty
	err = saved.Response.UnmarshalTo(&empty)
	require.NoError(t, err, "Delete response must be google.protobuf.Empty (proto-options contract)")
}

func TestNetworkService_Update_MaskApplication(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	folderClient := newMockFolderClient(true)
	svc := NewNetworkService(nr, nil, nil, nil, folderClient, or, nil)

	// Создаём сеть
	createOp, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "net1"})
	require.NoError(t, err)
	savedOp := awaitOpDone(t, or, createOp.ID)
	require.NotNil(t, savedOp.Metadata)

	// Находим созданную сеть через List
	nets, _, err := svc.List(context.Background(), NetworkFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nets, 1)
	netID := nets[0].ID

	// Update с маской
	updOp, err := svc.Update(context.Background(), UpdateNetworkReq{
		NetworkID:   netID,
		Name:        "net1-updated",
		Description: "new desc",
		UpdateMask:  []string{"name"},
	})
	require.NoError(t, err)
	savedUpdOp := awaitOpDone(t, or, updOp.ID)
	assert.True(t, savedUpdOp.Done)

	// Проверяем что только name обновилось (description не изменилась из-за маски)
	n, err := svc.Get(context.Background(), netID)
	require.NoError(t, err)
	assert.Equal(t, "net1-updated", n.Name)
	assert.Equal(t, "", n.Description) // маска не включала description
}
