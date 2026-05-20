package clients

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-corelib/retry"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// withPrincipalMD propagates the caller's principal onto the outgoing gRPC
// metadata (KAC-127 Bug-2).
//
// kacho-iam's public ProjectService.Get carries a tenant scope-filter: it
// returns NOT_FOUND unless the caller is the owning Account's owner, and
// skips straight to NOT_FOUND for an anonymous caller. kacho-vpc validates a
// Network's project via ProjectService.Get on the Network.Create hot path —
// that call runs inside the Operation worker, whose ctx (via operations
// baggage) still carries the original request Principal, but vpc never
// forwarded it onto the outgoing gRPC metadata. The peer therefore saw an
// anonymous/system call, returned NOT_FOUND, and Network.Create failed its
// project-exists check — silently (operations.Run masks worker errors) — so
// no Network row and no `vpc_network:<id>#project` FGA tuple were ever
// produced, leaving every per-resource Check `no path`.
//
// Propagating `x-kacho-principal-*` lets the peer resolve the real caller
// (the project's account-owner on the seeded fixtures) so the scope-filter
// passes. A ctx without a principal is forwarded unchanged.
func withPrincipalMD(ctx context.Context) context.Context {
	p := operations.PrincipalFromContext(ctx)
	if p.ID == "" || p.Type == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx,
		grpcsrv.MDKeyPrincipalType, p.Type,
		grpcsrv.MDKeyPrincipalID, p.ID,
		grpcsrv.MDKeyPrincipalDisplay, p.DisplayName,
	)
}

// KAC-106 (E1): peer-call switched from kacho-resource-manager.FolderService.Get
// to kacho-iam.ProjectService.Get. File-name retained for git-history continuity;
// the type name is now ProjectClient.

// projectExistsTTL — TTL положительного результата Exists.
const projectExistsTTL = 30 * time.Second

// projectAccountIDTTL — TTL кеша project→account_id (Account = parent-scope в IAM,
// analog "cloud_id" из RM-эпохи).
const projectAccountIDTTL = 10 * time.Minute

// ProjectClient реализует service.ProjectClient через gRPC к kacho-iam.
// Exists / GetCloudIDFromProject — hot path: Exists в каждом Create/Move,
// GetCloudIDFromProject в IPAM cascade Step 3.
type ProjectClient struct {
	cli iamv1.ProjectServiceClient

	mu         sync.RWMutex
	exists     map[string]time.Time      // projectID → время до которого результат "true" валиден
	accountIDs map[string]accountIDEntry // projectID → {account_id, expiry}
}

type accountIDEntry struct {
	accountID string
	exp       time.Time
}

// NewProjectClient создаёт ProjectClient. conn — обычно `clients.Build(...)`
// (см. builder.go), принимается как grpc.ClientConnInterface — что подходит и
// для corlib `ClientConn` (KAC-97), и для `*grpc.ClientConn`.
func NewProjectClient(conn grpc.ClientConnInterface) *ProjectClient {
	return &ProjectClient{
		cli:        iamv1.NewProjectServiceClient(conn),
		exists:     make(map[string]time.Time),
		accountIDs: make(map[string]accountIDEntry),
	}
}

// Exists проверяет существование Project через kacho-iam.ProjectService.Get.
// Положительный результат кешируется на projectExistsTTL — убирает gRPC RTT
// к kacho-iam из hot-path при burst-нагрузке. NotFound НЕ кешируется как
// negative (свеже-созданный project быстро становится виден).
func (c *ProjectClient) Exists(ctx context.Context, projectID string) (bool, error) {
	// Cache hit?
	c.mu.RLock()
	exp, ok := c.exists[projectID]
	c.mu.RUnlock()
	if ok && time.Now().Before(exp) {
		return true, nil
	}

	var exists bool
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		_, rerr := c.cli.Get(withPrincipalMD(ctx), &iamv1.GetProjectRequest{ProjectId: projectID})
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
		c.exists[projectID] = time.Now().Add(projectExistsTTL)
		c.mu.Unlock()
	}
	return exists, nil
}

// GetCloudIDFromProject возвращает parent-scope id для Project — account_id в
// IAM-модели (исторически "cloud_id" из RM-эпохи). Используется в IPAM cascade
// Step 3 (cloud-pool-selector lookup) на каждый external-IP allocate.
// Положительный результат кешируется; ошибки/пустой account_id не кешируются.
func (c *ProjectClient) GetCloudIDFromProject(ctx context.Context, projectID string) (string, error) {
	// Cache hit?
	c.mu.RLock()
	e, ok := c.accountIDs[projectID]
	c.mu.RUnlock()
	if ok && time.Now().Before(e.exp) {
		return e.accountID, nil
	}

	var accountID string
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		p, rerr := c.cli.Get(withPrincipalMD(ctx), &iamv1.GetProjectRequest{ProjectId: projectID})
		if rerr != nil {
			return rerr
		}
		accountID = p.GetAccountId()
		return nil
	})
	if err != nil {
		return "", err
	}
	if accountID != "" {
		c.mu.Lock()
		c.accountIDs[projectID] = accountIDEntry{accountID: accountID, exp: time.Now().Add(projectAccountIDTTL)}
		c.mu.Unlock()
	}
	return accountID, nil
}
