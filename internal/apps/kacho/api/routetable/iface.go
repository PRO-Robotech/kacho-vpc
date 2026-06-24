// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package routetable — use-case-структура ресурса RouteTable: бизнес-логика
// Create/Update/Delete/Get/List/ListOperations плюс тонкий gRPC-handler.
//
// Каждый use-case работает через CQRS-Repository и открывает TX явно
// (`u.repo.Writer(ctx)` либо `Reader(ctx)`); outbox-emit лежит в той же writer-TX
// — атомарность DML + outbox гарантирована.
//
// Auto-association: DB-уровневые PL/pgSQL триггеры AFTER INSERT ON route_tables
// привязывают Subnet'ы с `route_table_id IS NULL` и эмитят `Subnet.UPDATED` с
// маркером `auto_association: true`. CQRS-Insert просто делает INSERT — триггер
// срабатывает в БД, дополнительные outbox-события пишет БД, use-case ими не управляет.
package routetable

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, RouteTableFilter — пере-используем единые value-объекты `internal/repo`.
type (
	Pagination       = repo.Pagination
	RouteTableFilter = repo.RouteTableFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
type (
	Repo                  = kacho.Repository
	Reader                = kacho.RepositoryReader
	Writer                = kacho.RepositoryWriter
	RouteTableReaderIface = kacho.RouteTableReaderIface
	RouteTableWriterIface = kacho.RouteTableWriterIface
	OutboxEmitter         = kacho.OutboxEmitter
)

// ProjectClient — то, что use-case'ам RouteTable нужно от peer-сервиса
// kacho-iam.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ListFilter — port per-object List-фильтра. Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)`. nil → unfiltered passthrough.
// bypass=false && len(allowedIDs)==0 → пустой List (no-leak).
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}
