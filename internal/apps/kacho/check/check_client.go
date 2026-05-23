package check

import (
	"context"
	"strings"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/auth"
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
//
// Когда IAM возвращает allowed=false с reason "no path" (нет FGA-tuple для
// объекта), Check возвращает authz.ErrNoPath — сигнал interceptor'у пропустить
// запрос к handler'у (который вернёт NOT_FOUND из DB) вместо 403.
//
// W1.4 (KAC-140): outgoing ctx обёрнут `auth.PropagateOutgoing`, чтобы iam-side
// `grpcsrv.UnaryPrincipalExtract` увидел реального caller'а, а не SystemPrincipal()
// = user:bootstrap. До W1.4 каждый per-RPC authz Check от vpc-check-interceptor
// → iam летел без MD → iam-обработчики, которые потом звали
// operations.PrincipalFromContext (audit, scope-filter, OPA-overlay) видели
// bootstrap независимо от реального caller'а. См.
// `docs/specs/sub-phase-W1.4-principal-propagation-acceptance.md`.
func (c *IAMCheckClient) Check(ctx context.Context, subjectID, relation, object string) (bool, error) {
	resp, err := c.cli.Check(auth.PropagateOutgoing(ctx), &iamv1.CheckRequest{
		SubjectId: subjectID,
		Relation:  relation,
		Object:    object,
	})
	if err != nil {
		return false, err
	}
	if !resp.GetAllowed() && strings.Contains(resp.GetReason(), "no path") {
		return false, authz.ErrNoPath
	}
	return resp.GetAllowed(), nil
}

// Compile-time check.
var _ authz.CheckClient = (*IAMCheckClient)(nil)
