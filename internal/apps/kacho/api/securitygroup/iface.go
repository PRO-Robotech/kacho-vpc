// Package securitygroup — use-case-структура ресурса SecurityGroup (skill evgeniy
// §2 B.1-B.4).
//
// Wave 3 (KAC-94): сюда переехал бывший монолитный `internal/service/security_group.go`
// (SecurityGroupService, 583 LoC) — fat-service со всеми методами в одном файле.
// Use-case'ы локализованы рядом с handler'ом (B.4 — локальность), repo-операции
// делегируются через **локальные** port-интерфейсы (ниже).
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): SG переехал на CQRS-
// Repository (Reader / Writer split) — parity с Network/Subnet/Address/RouteTable/
// PrivateEndpoint/NetworkInterface/Gateway. Каждый use-case открывает TX явно
// (`u.repo.Writer(ctx)` или `Reader(ctx)`), и outbox-emit лежит в той же tx
// writer'а — атомарность DML + outbox гарантирована (G.5). OCC через xmin для
// UpdateRules — в pg-impl (`pg/security_group.go`), use-case'ы только
// маппят SQL-sentinels на gRPC status.
//
// SG-специфика: помимо базового CRUD/Move есть split-endpoint **UpdateRules**
// (атомарно удалить deletion_rule_ids + добавить addition_rule_specs) и
// **UpdateRule** (mod description/labels единичного rule; response — parent SG
// для verbatim YC CLI 1.x compat).
//
// Default-SG creation остаётся inline в `internal/apps/kacho/api/network/`
// (CreateNetworkUseCase): здесь use-case'ы — обычный Create без авто-default.
package securitygroup

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, SecurityGroupFilter — пере-используем единые value-объекты
// (alias'ы, не копии). После Wave 5 D.1 — Pagination/SecurityGroupFilter живут
// в leaf-пакете `kachorepo`; legacy `repo.Pagination` сам — type-alias.
type (
	Pagination          = repo.Pagination
	SecurityGroupFilter = repo.SecurityGroupFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): parity с network/iface.go.
type (
	Repo                     = kachorepo.Repository
	Reader                   = kachorepo.RepositoryReader
	Writer                   = kachorepo.RepositoryWriter
	SecurityGroupReaderIface = kachorepo.SecurityGroupReaderIface
	SecurityGroupWriterIface = kachorepo.SecurityGroupWriterIface
	OutboxEmitter            = kachorepo.OutboxEmitter
)

// NetworkReader — узкое чтение Network для sync-precondition'а
// «Network существует» в Create-SG (если network_id задан).
type NetworkReader interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
}

// SecurityGroupReader — узкое чтение SecurityGroup для same-network-валидации
// SG-target-правил (KAC-243, §C). Резолвит `network_id`:
//   - редактируемой SG (для UpdateRules / UpdateRule — на Create network_id
//     приходит прямо из request'а);
//   - каждой target-SG, на которую ссылается SG-target-правило
//     (`oneof target = security_group_id`).
//
// Cross-network target (`target.NetworkID != self.NetworkID`) → InvalidArgument;
// несуществующая target-SG (`repo.ErrNotFound`) → InvalidArgument (D1). Проверка
// не TOCTOU-prone: network_id immutable (§B). Удовлетворяется
// `cqrsadapter.SecurityGroupAdapter` (Get) — wired в composition-root.
type SecurityGroupReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SecurityGroupRecord, error)
}

// ProjectClient — peer-сервис kacho-iam: проверка существования
// folder'а на request-path и в worker'е.
type ProjectClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
