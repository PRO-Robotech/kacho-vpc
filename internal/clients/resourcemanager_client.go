package clients

import (
	"context"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/retry"
	rmv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/resourcemanager/v1"
)

// FolderClient реализует service.FolderClient через gRPC к resource-manager.
//
// Retry-policy: на codes.Unavailable (downstream-rolling-restart, network glitch)
// — exponential backoff 100ms..5s, max-elapsed 30s. См. kacho-corelib/retry.Defaults.
type FolderClient struct {
	cli rmv1.FolderInternalServiceClient
}

// NewFolderClient создаёт FolderClient.
func NewFolderClient(conn *grpc.ClientConn) *FolderClient {
	return &FolderClient{cli: rmv1.NewFolderInternalServiceClient(conn)}
}

// Exists проверяет существование Folder в resource-manager.
// При codes.Unavailable выполняется retry с exponential backoff.
func (c *FolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	var resp *rmv1.ExistsResponse
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		var rerr error
		resp, rerr = c.cli.Exists(ctx, &rmv1.ExistsRequest{Uid: folderID})
		return rerr
	})
	if err != nil {
		return false, err
	}
	return resp.GetExists(), nil
}
