// Package clients — iam_listobjects_client.go (KAC-127 Phase 4).
//
// Adapter поверх kacho-iam AuthorizeService.ListObjects, реализующий
// corelib/authz.ListObjectsClient port-interface (decoupling: corelib не
// импортирует kacho-proto stubs).
//
// Используется на read-path (List RPC) для FGA-filtered listing. См.
// docs/specs/sub-phase-3.4-iam-list-filtering-acceptance.md.
package clients

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/PRO-Robotech/kacho-corelib/auth"
	"github.com/PRO-Robotech/kacho-corelib/authz"
	"github.com/PRO-Robotech/kacho-corelib/retry"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// authzModelHeader — metadata key для pinned authorization_model_id.
// kacho-iam AuthorizeService читает это header'ом (вне ListObjectsRequest),
// чтобы pinned model_id применялся consistently и к Check/BatchCheck/etc.
const authzModelHeader = "x-kacho-authz-model-id"

// IAMListObjectsClient — adapter поверх iamv1.AuthorizeServiceClient,
// реализующий authz.ListObjectsClient.
//
// Wiring: `cmd/vpc/main.go` строит gRPC-conn до kacho-iam (тот же, что
// используется для Check), оборачивает в этот adapter, передаёт в
// authz.NewListObjectsService.
type IAMListObjectsClient struct {
	cli iamv1.AuthorizeServiceClient
}

// NewIAMListObjectsClient — конструктор.
//
// conn — обычно `clients.Build(ctx, ...)` (см. builder.go) на public-listener
// kacho-iam (:9090) — AuthorizeService публичный (не Internal).
func NewIAMListObjectsClient(conn grpc.ClientConnInterface) *IAMListObjectsClient {
	if conn == nil {
		return nil
	}
	return &IAMListObjectsClient{
		cli: iamv1.NewAuthorizeServiceClient(conn),
	}
}

// ListObjects реализует authz.ListObjectsClient.
//
//   - Передаёт `subject`/`resource_type`/`action`/`max_results`/`page_token`
//     прозрачно.
//   - AuthzModelID — через grpc-metadata `x-kacho-authz-model-id` (Phase 3
//     контракт): kacho-iam AuthorizeService использует его при ListObjects-вызове
//     в FGA-engine (consistent с Check semantics, acceptance D-12).
//   - retry.OnUnavailable — авто-retry на временные сетевые сбои.
//   - Любая `error` → возвращается caller'у, который wraps в ErrUnavailable
//     (см. authz.ListObjectsService.ListAllowedIDs).
//   - W1.4 (KAC-178 follow-up): outgoing ctx обёрнут `auth.PropagateOutgoing`,
//     чтобы iam-side `grpcsrv.UnaryPrincipalExtract` увидел реального caller'а,
//     а не SystemPrincipal() = user:bootstrap. Без этого wrap'а IAM
//     authzguard'ы видели "system:bootstrap" и отбивали ListObjects как
//     "authz_anonymous_mutation_denied" → vpc list-filter возвращал 403
//     "list-filter denied" для ВСЕХ user'ов независимо от их FGA-tuple'ов.
//     Зеркало `kacho-vpc/internal/apps/kacho/check/check_client.go` (W1.4 для Check)
//     и `kacho-compute/internal/check/check_client.go`.
func (c *IAMListObjectsClient) ListObjects(ctx context.Context, req authz.ListObjectsRequest) (authz.ListObjectsResponse, error) {
	if c == nil || c.cli == nil {
		return authz.ListObjectsResponse{}, fmt.Errorf("IAMListObjectsClient: client not initialized")
	}

	ctx = auth.PropagateOutgoing(ctx)
	if req.AuthzModelID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, authzModelHeader, req.AuthzModelID)
	}

	var resp authz.ListObjectsResponse
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		grpcReq := &iamv1.ListObjectsRequest{
			Subject:      req.Subject,
			ResourceType: req.ResourceType,
			Action:       req.Action,
			MaxResults:   int64(req.MaxResults),
			PageToken:    req.PageToken,
		}
		out, rerr := c.cli.ListObjects(ctx, grpcReq)
		if rerr != nil {
			return rerr
		}
		resp = authz.ListObjectsResponse{
			ResourceIDs:   out.GetResourceIds(),
			NextPageToken: out.GetNextPageToken(),
			Truncated:     out.GetTruncated(),
		}
		return nil
	})
	if err != nil {
		return authz.ListObjectsResponse{}, err
	}
	return resp, nil
}
