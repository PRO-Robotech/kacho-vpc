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
//
// Использует FolderService.Get() из нового flat API (sub-phase 1.0).
// Retry-policy: на codes.Unavailable — exponential backoff 100ms..5s, max-elapsed 30s.
type FolderClient struct {
	cli rmv1.FolderServiceClient
}

// NewFolderClient создаёт FolderClient.
func NewFolderClient(conn *grpc.ClientConn) *FolderClient {
	return &FolderClient{cli: rmv1.NewFolderServiceClient(conn)}
}

// Exists проверяет существование Folder в resource-manager.
//
// При codes.Unavailable выполняется retry с exponential backoff.
// При codes.NotFound возвращает (false, nil) — папки нет.
// При прочих ошибках — (false, err).
func (c *FolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	var found bool
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		_, ferr := c.cli.Get(ctx, &rmv1.GetFolderRequest{FolderId: folderID})
		if ferr != nil {
			if st, ok := status.FromError(ferr); ok && st.Code() == codes.NotFound {
				found = false
				return nil
			}
			return ferr
		}
		found = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}
