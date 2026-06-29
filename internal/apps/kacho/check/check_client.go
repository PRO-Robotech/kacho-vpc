// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check

import (
	"context"
	"strings"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/auth"
	"github.com/PRO-Robotech/kacho-corelib/authz"
	iamv1 "github.com/PRO-Robotech/kacho-iam/proto/gen/go/kacho/cloud/iam/v1"
)

// ResourceExistenceProbe — порт проверки существования object-scoped ресурса в
// БД vpc по его FGA-объекту (`<type>:<id>`). Используется на deny-пути
// IAMCheckClient'а для existence-hiding: object-scoped deny на ОТСУТСТВУЮЩИЙ
// объект ведет себя как `ErrNoPath` (passthrough → handler отдаст verbatim
// NotFound 404, скрывая отсутствие owner-tuple), а на СУЩЕСТВУЮЩИЙ — остается
// PermissionDenied (verb-RBAC сохранен, мутация-handler недостижим).
//
// objectType — один из vpc-типов (`vpc_network`, `vpc_subnet`, …); неизвестный
// тип → ошибка (caller оставляет deny). Реализация — read-only `SELECT EXISTS`
// в repo-слое.
type ResourceExistenceProbe interface {
	ObjectExists(ctx context.Context, objectType, objectID string) (bool, error)
}

// vpcObjectTypes — object-scoped vpc-ресурсы, для которых применяется
// existence-hiding (Get/List/Update/Delete на самом ресурсе). `project`/`cluster`
// — collection/cluster-scoped, существование объекта там не скрывается.
var vpcObjectTypes = map[string]struct{}{
	objectTypeNetwork:          {},
	objectTypeSubnet:           {},
	objectTypeAddress:          {},
	objectTypeRouteTable:       {},
	objectTypeSecurityGroup:    {},
	objectTypeGateway:          {},
	objectTypeNetworkInterface: {},
}

// IAMCheckClient — gRPC adapter, реализующий port `authz.CheckClient`
// поверх `kacho-iam.InternalIAMService.Check`.
//
// corelib/authz намеренно не зависит от доменных proto-stubs, поэтому adapter
// живет здесь, в сервисе, как любой другой adapter.
//
// probe (опционально) — existence-probe для object-scoped deny: nil → прежнее
// поведение (только reason-substring "no path" → ErrNoPath).
type IAMCheckClient struct {
	cli   iamv1.InternalIAMServiceClient
	probe ResourceExistenceProbe
}

// NewIAMCheckClient создает adapter. conn — `*grpc.ClientConn`/`ClientConnInterface`
// к internal-port'у kacho-iam (обычно `kacho-iam.kacho.svc.cluster.local:9091`).
func NewIAMCheckClient(conn grpc.ClientConnInterface) *IAMCheckClient {
	return &IAMCheckClient{
		cli: iamv1.NewInternalIAMServiceClient(conn),
	}
}

// NewIAMCheckClientWithProbe — adapter с подключенным existence-probe (Decision 1
// existence-hiding). probe может быть nil (== NewIAMCheckClient).
func NewIAMCheckClientWithProbe(conn grpc.ClientConnInterface, probe ResourceExistenceProbe) *IAMCheckClient {
	return &IAMCheckClient{
		cli:   iamv1.NewInternalIAMServiceClient(conn),
		probe: probe,
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
		// Транспорт/Unavailable — interceptor отрабатывает fail-closed
		// PermissionDenied. НИКОГДА не existence-hide инфра-сбой (не 404).
		return false, err
	}
	if resp.GetAllowed() {
		return true, nil
	}
	// Deny. iam уже явно сигналит "no path" (unscopable resource) → passthrough.
	if strings.Contains(resp.GetReason(), "no path") {
		return false, authz.ErrNoPath
	}
	// Existence-hiding (Decision 1): object-scoped deny на vpc-ресурс. iam для
	// объекта без owner-tuple возвращает reason "lacks relation …" (не "no path"),
	// так что отличить «нет owner-tuple/нет объекта» от «объект есть, но нет
	// доступа» можно только сверкой с собственной БД vpc. Отсутствует → ведем
	// как ErrNoPath (passthrough → handler вернет verbatim NotFound 404, скрывая
	// отсутствие); существует → оставляем deny (PermissionDenied; verb-RBAC
	// сохранен, мутация-handler недостижим → no tamper). Probe-ошибка/неизвестный
	// тип → fail-closed (оставляем deny, без passthrough).
	if c.probe != nil {
		if objectType, objectID, ok := splitFGAObject(object); ok {
			if _, isVPCObj := vpcObjectTypes[objectType]; isVPCObj {
				exists, perr := c.probe.ObjectExists(ctx, objectType, objectID)
				if perr == nil {
					if !exists {
						// Объекта нет → passthrough (ErrNoPath): handler сам отдаст
						// verbatim NotFound из БД, скрывая отсутствие owner-tuple.
						return false, authz.ErrNoPath
					}
					// Объект есть, но caller не вправе видеть → existence-hiding:
					// interceptor блокирует handler и отдает NotFound (не 403),
					// чтобы «есть-но-не-твой» было неотличимо от «нет такого».
					return false, authz.ErrHideExistence
				}
				// Probe-ошибка → fail-closed (deny/403, без passthrough).
			}
		}
	}
	return false, nil
}

// splitFGAObject разбирает FGA-объект `<type>:<id>` на тип и id. ok=false, если
// формат не `type:id` (нет ровно одного разделителя или пустые части).
func splitFGAObject(object string) (objectType, objectID string, ok bool) {
	i := strings.IndexByte(object, ':')
	if i <= 0 || i == len(object)-1 {
		return "", "", false
	}
	return object[:i], object[i+1:], true
}

// Compile-time check.
var _ authz.CheckClient = (*IAMCheckClient)(nil)
