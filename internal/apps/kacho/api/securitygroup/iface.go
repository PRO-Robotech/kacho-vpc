// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package securitygroup — use-case-слой ресурса SecurityGroup: каждый use-case
// локализован рядом с handler'ом, repo-операции делегируются через локальные
// port-интерфейсы (ниже).
//
// Repository — CQRS (Reader / Writer split), parity с прочими ресурсами VPC.
// Каждый use-case явно открывает TX (`u.repo.Writer(ctx)` или `Reader(ctx)`), и
// outbox-emit лежит в той же writer-TX — атомарность DML + outbox гарантирована.
// OCC через xmin для UpdateRules живет в pg-impl (`pg/security_group.go`),
// use-case'ы только маппят SQL-sentinels на gRPC status.
//
// SG-специфика: помимо базового CRUD есть split-endpoint UpdateRules (атомарно
// удалить deletion_rule_ids + добавить addition_rule_specs) и UpdateRule (правка
// description/labels единичного rule; response — parent SG для совместимости с
// CLI).
//
// Default-SG создается inline в CreateNetworkUseCase (`api/network/`); здесь
// use-case'ы — обычный Create без авто-default.
package securitygroup

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, SecurityGroupFilter — единые value-объекты (alias'ы, не копии):
// определены в leaf-пакете `kachorepo`, а `repo.Pagination` сам — type-alias на них.
type (
	Pagination          = repo.Pagination
	SecurityGroupFilter = repo.SecurityGroupFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
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
// SG-target-правил. Резолвит `network_id`:
//   - редактируемой SG (для UpdateRules / UpdateRule — на Create network_id
//     приходит прямо из request'а);
//   - каждой target-SG, на которую ссылается SG-target-правило
//     (`oneof target = security_group_id`).
//
// Cross-network target (`target.NetworkID != self.NetworkID`) → InvalidArgument;
// несуществующая target-SG (`repo.ErrNotFound`) → InvalidArgument. Проверка
// не TOCTOU-prone: network_id immutable. Удовлетворяется
// `cqrsadapter.SecurityGroupAdapter` (Get) — wired в composition-root.
type SecurityGroupReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SecurityGroupRecord, error)
}

// ProjectClient — peer-сервис kacho-iam: проверка существования
// project'а на request-path и в worker'е.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ListFilter — port per-object List-фильтра. Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)`. nil → unfiltered passthrough.
// bypass=false && len(allowedIDs)==0 → пустой List (no-leak).
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}
