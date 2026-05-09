package clients

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/retry"
	rmv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/resourcemanager/v1"
)

// FolderClient реализует service.FolderClient через gRPC к resource-manager.
type FolderClient struct {
	cli rmv1.FolderServiceClient
}

// NewFolderClient создаёт FolderClient.
func NewFolderClient(conn *grpc.ClientConn) *FolderClient {
	return &FolderClient{cli: rmv1.NewFolderServiceClient(conn)}
}

// Exists проверяет существование Folder.
func (c *FolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	var exists bool
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		_, rerr := c.cli.Get(ctx, &rmv1.GetFolderRequest{FolderId: folderID})
		if rerr != nil {
			st, ok := status.FromError(rerr)
			if ok && st.Code() == codes.NotFound {
				exists = false
				return nil
			}
			return rerr
		}
		exists = true
		return nil
	})
	return exists, err
}

// GetCloudID возвращает cloud_id для Folder. Используется в IPAM-cascade
// (cloud-pool-selector lookup для external Address). Возвращает ErrNotFound
// если folder не существует.
func (c *FolderClient) GetCloudID(ctx context.Context, folderID string) (string, error) {
	var cloudID string
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		f, rerr := c.cli.Get(ctx, &rmv1.GetFolderRequest{FolderId: folderID})
		if rerr != nil {
			return rerr
		}
		cloudID = f.GetCloudId()
		return nil
	})
	return cloudID, err
}
