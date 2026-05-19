// Package listauthz — KAC-127 Phase 4 wiring helper.
//
// Превращает один общий `*authz.ListObjectsService` из corelib (cache+FGA-client)
// в N узких port-интерфейсов (`network.ListAuthorizer`, `subnet.ListAuthorizer`, …),
// каждый из которых выставляется в свой use-case в composition root.
//
// Цель — caller use-case'ы НЕ знают про generic ListAllowedIDs API, они
// видят только ListAllowedIDs(ctx, subject, objectType, action, scope) → []string.
// objectType+action — VPC-specific константы в каждом use-case.
package listauthz

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/authz"
)

// Port — generic интерфейс для FGA list-filter. Используется во всех VPC
// list-use-case'ах (network/subnet/sg/rt/address/gateway/pe/ni). Adapter
// реализует его поверх corelib authz.ListObjectsService.
type Port interface {
	ListAllowedIDs(ctx context.Context, subjectID, objectType, action, scopeHint string) ([]string, error)
}

// Adapter — generic adapter поверх *authz.ListObjectsService, удовлетворяющий
// port-интерфейс `ListAllowedIDs(ctx, subject, objectType, action, scope) → []string`
// (общая сигнатура для всех VPC use-case'ов: network/subnet/sg/rt/address/...).
//
// nil-safe: если svc == nil — все вызовы вернут (nil, ErrUnavailable) сразу
// (caller'ы обрабатывают это как fail-closed, см. acceptance §5.4).
type Adapter struct {
	svc *authz.ListObjectsService
}

// New собирает Adapter. svc == nil — допустимо (LIST_FILTER_ENABLED=false).
// Caller'ы должны прежде проверять (adapter == nil) → не выставлять use-case authz.
// Если caller всё-же передал nil-adapter с не-nil-svc, adapter работает как
// transparent pass-through.
func New(svc *authz.ListObjectsService) *Adapter {
	return &Adapter{svc: svc}
}

// ListAllowedIDs — fan-out на corelib service. opts.ScopeHint используется
// для cache-key separation (разные projects → разные cache entries).
func (a *Adapter) ListAllowedIDs(ctx context.Context, subjectID, objectType, action, scopeHint string) ([]string, error) {
	if a == nil || a.svc == nil {
		return nil, authz.ErrUnavailable
	}
	return a.svc.ListAllowedIDs(ctx, subjectID, objectType, action, authz.ListAllowedIDsOptions{
		ScopeHint: scopeHint,
	})
}

// AsPort — возвращает Adapter как Port (для type-safety wiring).
// Returns nil-Port если adapter == nil или adapter.svc == nil.
func AsPort(a *Adapter) Port {
	if a == nil || a.svc == nil {
		return nil
	}
	return a
}

// SubjectFromCtxPrincipal — KAC-127 Phase 4: общий helper extract FGA subject
// из ctx-Principal. Используется во ВСЕХ public List handlers VPC. Empty
// subject (system / no auth) → use-case fallback на legacy unfiltered.
//
// Импортируется через type-alias: каждый handler-пакет определяет локальный
// `subjectFromCtx := listauthz.SubjectFromCtx`.
func SubjectFromCtxPrincipal(p struct {
	Type string
	ID   string
}) string {
	if p.Type == "" || p.ID == "" || p.Type == "system" {
		return ""
	}
	return p.Type + ":" + p.ID
}
