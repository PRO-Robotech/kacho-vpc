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

// folderCloudIDTTL — TTL кеша folder→cloud_id. Привязка folder к cloud в YC
// неизменна (folder нельзя переместить в другой cloud), id не переиспользуются —
// поэтому TTL можно длинный; держим умеренный (10 мин) на случай дрейфа.
const folderCloudIDTTL = 10 * time.Minute

// FolderClient реализует service.FolderClient через gRPC к resource-manager
// с TTL-кешем для Exists и GetCloudID (оба — hot path: Exists в каждом
// Create/Move, GetCloudID в каждом external-Address allocate, IPAM cascade Step 3).
type FolderClient struct {
	cli rmv1.FolderServiceClient

	mu       sync.RWMutex
	exists   map[string]time.Time    // folderID → время до которого результат "true" валиден
	cloudIDs map[string]cloudIDEntry // folderID → {cloud_id, expiry}
}

type cloudIDEntry struct {
	cloudID string
	exp     time.Time
}

// NewFolderClient создаёт FolderClient. conn — обычно `clients.Build(...)`
// (см. builder.go), принимается как grpc.ClientConnInterface — что подходит и
// для corlib `ClientConn` (KAC-97), и для `*grpc.ClientConn`.
func NewFolderClient(conn grpc.ClientConnInterface) *FolderClient {
	return &FolderClient{
		cli:      rmv1.NewFolderServiceClient(conn),
		exists:   make(map[string]time.Time),
		cloudIDs: make(map[string]cloudIDEntry),
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
// (cloud-pool-selector lookup для external Address) — на каждый external-IP
// allocate. Положительный результат кешируется на folderCloudIDTTL: убирает
// gRPC RTT к resource-manager из hot-path аллокатора (без кеша RM —
// 1-pod сервис — становился потолком ~3K allocate/sec). Ошибки/пустой
// cloud_id не кешируются.
func (c *FolderClient) GetCloudID(ctx context.Context, folderID string) (string, error) {
	// Cache hit?
	c.mu.RLock()
	e, ok := c.cloudIDs[folderID]
	c.mu.RUnlock()
	if ok && time.Now().Before(e.exp) {
		return e.cloudID, nil
	}

	var cloudID string
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		f, rerr := c.cli.Get(ctx, &rmv1.GetFolderRequest{FolderId: folderID})
		if rerr != nil {
			return rerr
		}
		cloudID = f.GetCloudId()
		return nil
	})
	if err != nil {
		return "", err
	}
	if cloudID != "" {
		c.mu.Lock()
		c.cloudIDs[folderID] = cloudIDEntry{cloudID: cloudID, exp: time.Now().Add(folderCloudIDTTL)}
		c.mu.Unlock()
	}
	return cloudID, nil
}
