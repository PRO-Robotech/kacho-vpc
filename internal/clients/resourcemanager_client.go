package clients

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/retry"
	rmv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/resourcemanager/v1"
)

// folderExistsTTL — как долго кешируется положительный результат Exists.
// Folder existence стабильна (folder редко удаляется), но не вечна — поэтому TTL.
// NotFound НЕ кешируется: folder может быть создан в любой момент, и closed-world
// negative cache привёл бы к ложным "Folder not found".
const folderExistsTTL = 30 * time.Second

// FolderClient реализует service.FolderClient через gRPC к resource-manager
// с TTL-кешем для Exists (hot path в каждом Create/Move).
type FolderClient struct {
	cli rmv1.FolderServiceClient

	mu    sync.RWMutex
	exists map[string]time.Time // folderID → время до которого результат "true" валиден
}

// NewFolderClient создаёт FolderClient.
func NewFolderClient(conn *grpc.ClientConn) *FolderClient {
	return &FolderClient{
		cli:    rmv1.NewFolderServiceClient(conn),
		exists: make(map[string]time.Time),
	}
}

// Exists проверяет существование Folder. Положительный результат кешируется
// на folderExistsTTL — это убирает gRPC RTT к resource-manager из hot-path
// при burst-нагрузке (5000 Create/sec → 5000 gRPC calls/sec без кеша).
func (c *FolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	// Cache hit?
	c.mu.RLock()
	exp, ok := c.exists[folderID]
	c.mu.RUnlock()
	if ok && time.Now().Before(exp) {
		return true, nil
	}

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
	if err != nil {
		return false, err
	}
	if exists {
		c.mu.Lock()
		c.exists[folderID] = time.Now().Add(folderExistsTTL)
		c.mu.Unlock()
	}
	return exists, nil
}

// GetCloudID возвращает cloud_id для Folder. Используется в IPAM-cascade
// (cloud-pool-selector lookup для external Address). Возвращает ErrNotFound
// если folder не существует. Не кешируется — вызывается реже чем Exists.
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
