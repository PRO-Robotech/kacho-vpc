// Package listauthz — project-level List authorization (KAC-240).
//
// History: KAC-127 Phase 4 introduced per-object FGA list-filtering — each List
// use-case resolved the FGA-allowed object-id set via ListObjects and filtered
// the (already project-scoped) repo rows by it. That coupled List visibility to
// per-object `view` tuples existing AT List time. Those tuples are emitted best-
// effort AFTER commit, outside the worker TX, and the corelib allow-set is cached
// ~5s — so a freshly-created resource was invisible until its tuple landed (a
// race), and permanently invisible if the tuple-write silently failed.
//
// KAC-240 fix (option 1 — project-scoped List): List is already project-bounded
// at the repo layer (every use-case calls repo.List(ctx, projectID, …), so all
// returned rows belong to exactly that one project). Therefore authorize at the
// PROJECT level: if the subject may `viewer` the project, return ALL the rows;
// otherwise return empty (fail-closed). This removes the dependency on per-object
// tuples existing at List time, fixing both the race and the silent-tuple-fail
// mode, WITHOUT weakening tenant isolation — the repo query remains the boundary.
//
// The project-view decision reuses the EXISTING per-RPC authz mechanism: a
// corelib `authz.CheckClient` (implemented by `check.IAMCheckClient` over
// `kacho-iam InternalIAMService.Check`) with relation "viewer" on object
// "project:<project_id>" — identical to the relation/object the per-RPC
// interceptor checks for the same List RPCs (see internal/apps/kacho/check).
package listauthz

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
)

// relationViewer / objectTypeProject — reuse the EXACT FGA relation + object type
// the per-RPC Check gate uses for List RPCs (internal/apps/kacho/check:
// relationViewer="viewer", objectTypeProject="project"). Do NOT invent a new
// relation name — the FGA model already grants `viewer on project` to anyone who
// may list the project's resources.
const (
	relationViewer    = "viewer"
	objectTypeProject = "project"
)

// MapListFilterErr — единая трансляция ошибок project-authz Check в gRPC status,
// который list-handler возвращает клиенту.
//
//   - authz.ErrPermissionDenied → codes.PermissionDenied (HTTP 403)
//     "у тебя нет прав на этот action" — легитимный denial, retry бесполезен.
//   - всё остальное (включая authz.ErrUnavailable, sentinel-free errors) →
//     codes.Unavailable (HTTP 503) с префиксом "list-filter unavailable:"
//     — infra сломана, retry имеет смысл.
//
// nil-err → nil (defensive).
func MapListFilterErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, authz.ErrPermissionDenied) {
		return status.Error(codes.PermissionDenied, "list-filter denied")
	}
	return status.Error(codes.Unavailable, "list-filter unavailable: "+err.Error())
}

// Port — generic интерфейс project-level List authorization. Используется во
// всех VPC list-use-case'ах (network/subnet/sg/rt/address/gateway/pe/ni). Каждый
// use-case вызывает ровно один метод — CanViewProject.
type Port interface {
	// CanViewProject возвращает (true, nil), если subject имеет relation
	// "viewer" на project:<projectID>. (false, nil) → fail-closed empty list.
	// err != nil → infra недоступна (fail-closed Unavailable у caller'а).
	CanViewProject(ctx context.Context, subjectID, projectID string) (bool, error)
}

// Adapter — реализация Port поверх corelib `authz.CheckClient`. Thread-safe
// (CheckClient + его кеш — thread-safe).
//
// nil-safe: если checker == nil — любой CanViewProject вернёт (false, ErrUnavailable)
// (fail-closed). Caller'ы должны прежде проверять (adapter == nil) → не выставлять
// use-case authz (dev / no-authz mode).
type Adapter struct {
	checker authz.CheckClient
}

// New собирает Adapter из CheckClient. checker == nil — допустимо (caller'ы
// должны прежде проверять nil → не выставлять use-case authz).
func New(checker authz.CheckClient) *Adapter {
	return &Adapter{checker: checker}
}

// CanViewProject — single Check на project:<projectID> с relation viewer.
func (a *Adapter) CanViewProject(ctx context.Context, subjectID, projectID string) (bool, error) {
	if a == nil || a.checker == nil {
		return false, authz.ErrUnavailable
	}
	object, err := authz.FormatObject(objectTypeProject, projectID)
	if err != nil {
		return false, err
	}
	return a.checker.Check(ctx, subjectID, relationViewer, object)
}

// AsPort — возвращает Adapter как Port (для type-safety wiring).
// Returns nil-Port если adapter == nil или adapter.checker == nil.
func AsPort(a *Adapter) Port {
	if a == nil || a.checker == nil {
		return nil
	}
	return a
}

// NewProjectChecker — convenience-конструктор: возвращает Port напрямую из
// CheckClient. checker == nil → nil-Port (use-case fallback на passthrough).
func NewProjectChecker(checker authz.CheckClient) Port {
	if checker == nil {
		return nil
	}
	return &Adapter{checker: checker}
}
