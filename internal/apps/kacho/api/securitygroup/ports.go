// Package securitygroup — use-case-структура ресурса SecurityGroup (skill evgeniy
// §2 B.1-B.4).
//
// Wave 3 (KAC-94): сюда переехал бывший монолитный `internal/service/security_group.go`
// (SecurityGroupService, 583 LoC) — fat-service со всеми методами в одном файле.
// Use-case'ы локализованы рядом с handler'ом (B.4 — локальность), repo-операции
// делегируются через **локальные** port-интерфейсы (ниже).
//
// SG-специфика: помимо базового CRUD/Move есть split-endpoint **UpdateRules**
// (атомарно удалить deletion_rule_ids + добавить addition_rule_specs; OCC через
// xmin живёт в repo) и **UpdateRule** (mod description/labels единичного rule;
// response — parent SG для verbatim YC CLI 1.x compat).
//
// Default-SG creation остаётся inline в `internal/apps/kacho/api/network/`
// (CreateNetworkUseCase): здесь use-case'ы — обычный Create без авто-default.
//
// Локальные интерфейсы (а не type-alias на `internal/repo.SecurityGroupRepoIface`) —
// сознательный выбор по skill §6 G.2-G.3: каждый use-case-пакет описывает только
// то, что РЕАЛЬНО использует. Адаптерами выступают существующие
// `internal/repo/security_group_repo.go` и `internal/repo/repomock` — они уже
// реализуют `internal/repo.SecurityGroupRepoIface`, который ⊇ локальному интерфейсу,
// поэтому Go-типизация работает без shim'ов.
package securitygroup

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/repo`
// (alias'ы, не копии).
type (
	Pagination          = repo.Pagination
	SecurityGroupFilter = repo.SecurityGroupFilter
)

// SecurityGroupRepo — то, что use-case'ам SG нужно от репозитория.
//
// Все методы возвращают `*kachorepo.SecurityGroupRecord` (skill evgeniy §4 D.1 /
// §7 H.1 — repo-entity несёт DB-managed CreatedAt). Insert/Update принимают
// `*domain.SecurityGroup` (без CreatedAt).
//
// UpdateRules / UpdateRule — SG-специфические split-endpoint'ы. OCC через
// `xmin::text` живёт в repo (см. `security_group_repo.go`).
type SecurityGroupRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.SecurityGroupRecord, error)
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*kachorepo.SecurityGroupRecord, string, error)
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*kachorepo.SecurityGroupRecord, error)
	Update(ctx context.Context, sg *domain.SecurityGroup) (*kachorepo.SecurityGroupRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*kachorepo.SecurityGroupRecord, error)
	// UpdateRules атомарно заменяет набор правил SG: удаляет правила с
	// id ∈ deleteIDs и добавляет правила из add (с auto-id если пусто).
	// Возвращает обновлённый SG с актуальным списком правил.
	UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*kachorepo.SecurityGroupRecord, error)
	// UpdateRule обновляет description/labels единичного правила в SG.
	UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*kachorepo.SecurityGroupRecord, error)
}

// NetworkReader — узкое чтение Network для sync-precondition'а
// «Network существует» в Create-SG (если network_id задан).
type NetworkReader interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
}

// FolderClient — peer-сервис kacho-resource-manager: проверка существования
// folder'а на request-path и в worker'е.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
