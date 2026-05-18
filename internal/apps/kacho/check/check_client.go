package check

import (
	"context"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// IAMCheckClient — gRPC adapter, реализующий port `authz.CheckClient`
// поверх `kacho-iam.InternalIAMService.Check`.
//
// Decoupling: corelib/authz НЕ зависит от kacho-proto stubs (см. doc.go в
// kacho-corelib/authz). Adapter живёт здесь, в сервисе, как любой adapter.
type IAMCheckClient struct {
	cli iamv1.InternalIAMServiceClient
}

// NewIAMCheckClient создаёт adapter. conn — `*grpc.ClientConn`/`ClientConnInterface`
// к internal-port'у kacho-iam (обычно `kacho-iam.kacho.svc.cluster.local:9091`).
func NewIAMCheckClient(conn grpc.ClientConnInterface) *IAMCheckClient {
	return &IAMCheckClient{
		cli: iamv1.NewInternalIAMServiceClient(conn),
	}
}

// Check вызывает `InternalIAMService.Check`. Реализация port'а authz.CheckClient.
//
// Error semantics — см. authz.CheckClient:
//   - err = nil + allowed=true  → пропустить RPC
//   - err = nil + allowed=false → DENY
//   - err != nil                → Unavailable (interceptor fail-closed'нет)
func (c *IAMCheckClient) Check(ctx context.Context, subjectID, relation, object string) (bool, error) {
	resp, err := c.cli.Check(ctx, &iamv1.CheckRequest{
		SubjectId: subjectID,
		Relation:  relation,
		Object:    object,
	})
	if err != nil {
		return false, err
	}
	return resp.GetAllowed(), nil
}

// Compile-time check.
var _ authz.CheckClient = (*IAMCheckClient)(nil)
