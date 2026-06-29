// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package anycastaddresspool — use-case-слой tenant-facing ресурса
// AnycastAddressPool. Каждый use-case локализован рядом с handler'ом, repo-
// операции делегируются через CQRS-Repository (Reader / Writer split). Мутации
// (Create/Update/Delete/AttachNetwork/DetachNetwork) — async через corelib
// operations Worker; read (Get/List) — sync.
//
// Within-service инвариант непересечения CIDR-претензий сети держится на
// DB-уровне (network_cidr_claims + EXCLUDE gist) — claim'ы пишутся в той же
// writer-TX, что и pivot. Use-case только маппит SQL-sentinels на gRPC status.
package anycastaddresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Value-объекты — единые alias'ы (не копии): определены в leaf-пакете
// `kachorepo`, а `repo.*` сам — type-alias на них.
type (
	Pagination               = repo.Pagination
	AnycastAddressPoolFilter = repo.AnycastAddressPoolFilter
)

// Re-export CQRS-Repository типов — use-case работает с ними под коротким именем.
type (
	Repo   = kachorepo.Repository
	Reader = kachorepo.RepositoryReader
	Writer = kachorepo.RepositoryWriter
)

// NetworkReader — узкое чтение Network для attach: проверка существования сети и
// same-project-инварианта (пул и сеть одного проекта). Удовлетворяется
// `cqrsadapter.NetworkAdapter` (Get) — wired в composition-root.
type NetworkReader interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
}

// ProjectClient — peer-сервис kacho-iam: проверка существования project'а
// (fail-closed на request-path/worker'е).
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ListFilter — port per-object List-фильтра (FGA). Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)`. nil → unfiltered passthrough.
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}
