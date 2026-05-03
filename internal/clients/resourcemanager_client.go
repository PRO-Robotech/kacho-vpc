package clients

import (
	"context"

	"google.golang.org/grpc"

	rmv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/resourcemanager/v1"
)

// FolderClient реализует service.FolderClient через gRPC к resource-manager.
type FolderClient struct {
	cli rmv1.FolderInternalServiceClient
}

// NewFolderClient создаёт FolderClient.
func NewFolderClient(conn *grpc.ClientConn) *FolderClient {
	return &FolderClient{cli: rmv1.NewFolderInternalServiceClient(conn)}
}

// Exists проверяет существование Folder в resource-manager.
func (c *FolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	resp, err := c.cli.Exists(ctx, &rmv1.ExistsRequest{Uid: folderID})
	if err != nil {
		return false, err
	}
	return resp.GetExists(), nil
}
