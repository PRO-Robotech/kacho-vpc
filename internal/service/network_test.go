package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNetworkService_Get_NotFound(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, newMockOpsRepo())
	_, err := svc.Get(context.Background(), "nonexistent")
	require.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNetworkService_Create_ValidationError(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, newMockOpsRepo())

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
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: false}, newMockOpsRepo())
	op, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "net1"})
	require.NoError(t, err) // Operation создаётся, ошибка внутри goroutine
	require.NotNil(t, op)
	// Ждём завершения goroutine
	time.Sleep(50 * time.Millisecond)
}

func TestNetworkService_Create_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	svc := NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "net1", Description: "desc"})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)
	assert.False(t, op.Done) // async — ещё не done

	// Ждём async goroutine
	time.Sleep(100 * time.Millisecond)

	// Проверяем что Operation завершена успешно
	saved, err := or.Get(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestNetworkService_List_Empty(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, newMockOpsRepo())
	nets, token, err := svc.List(context.Background(), NetworkFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nets)
	assert.Empty(t, token)
}

func TestNetworkService_Delete_ValidationError(t *testing.T) {
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, newMockOpsRepo())
	_, err := svc.Delete(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkService_Update_MaskApplication(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	folderClient := &mockFolderClient{exists: true}
	svc := NewNetworkService(nr, nil, nil, nil, folderClient, or)

	// Создаём сеть
	createOp, err := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "net1"})
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Получаем id из metadata
	savedOp, err := or.Get(context.Background(), createOp.ID)
	require.NoError(t, err)
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
	time.Sleep(100 * time.Millisecond)

	savedUpdOp, err := or.Get(context.Background(), updOp.ID)
	require.NoError(t, err)
	assert.True(t, savedUpdOp.Done)

	// Проверяем что только name обновилось (description не изменилась из-за маски)
	n, err := svc.Get(context.Background(), netID)
	require.NoError(t, err)
	assert.Equal(t, "net1-updated", n.Name)
	assert.Equal(t, "", n.Description) // маска не включала description
}
