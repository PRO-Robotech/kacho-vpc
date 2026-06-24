// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
// corelib/authz намеренно не зависит от kacho-proto stubs, поэтому adapter
// живет здесь, в сервисе, как любой другой adapter.
type IAMCheckClient struct {
	cli iamv1.InternalIAMServiceClient
}

// NewIAMCheckClient создает adapter. conn — `*grpc.ClientConn`/`ClientConnInterface`
// к internal-port'у kacho-iam (обычно `kacho-iam.kacho.svc.cluster.local:9091`).
func NewIAMCheckClient(conn grpc.ClientConnInterface) *IAMCheckClient {
	return &IAMCheckClient{
		cli: iamv1.NewInternalIAMServiceClient(conn),
	}
}

// Check вызывает `InternalIAMService.Check`. Реализация port'а authz.CheckClient.
//
// Семантика ошибок — см. authz.CheckClient:
//   - err = nil + allowed=true  → пропустить RPC
//   - err = nil + allowed=false → DENY
//   - err != nil                → Unavailable (interceptor отрабатывает fail-closed)
//
// Когда IAM возвращает allowed=false с reason "no path" (нет FGA-tuple для
// объекта), Check возвращает authz.ErrNoPath — сигнал interceptor'у пропустить
// запрос к handler'у (который вернет NOT_FOUND из DB) вместо 403.
//
// Outgoing ctx оборачивается `auth.PropagateOutgoing`, чтобы на стороне iam
// `grpcsrv.UnaryPrincipalExtract` увидел реального caller'а, а не SystemPrincipal()
// = user:bootstrap. Без этого per-RPC Check уходил бы в iam без MD, и iam-обработчики,
// зовущие operations.PrincipalFromContext (audit, scope-filter, OPA-overlay), видели бы
// bootstrap независимо от реального caller'а.
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
