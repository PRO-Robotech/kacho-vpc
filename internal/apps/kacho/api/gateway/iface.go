// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package gateway — use-case-структура ресурса Gateway. Бизнес-логика
// CreateGatewayUseCase / UpdateGatewayUseCase / DeleteGatewayUseCase /
// GetGatewayUseCase / ListGatewaysUseCase / ListOperationsUseCase плюс тонкий
// gRPC-handler.
//
// Gateway use-case'ы работают через CQRS-Repository (Reader / Writer split).
// Каждый mutating use-case открывает TX явно (`u.repo.Writer(ctx)`), эмитит
// outbox через `w.Outbox().Emit(...)` в той же TX, затем Commit — атомарность
// DML + outbox гарантирована.
package gateway

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination — пере-используем единый value-объект `internal/repo` (type-alias).
type (
	Pagination    = repo.Pagination
	GatewayFilter = kacho.GatewayFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
//
// Use-case-слой открывает TX явно через `repo.Reader(ctx)` / `repo.Writer(ctx)` и
// видит разделение reader/writer в типе вызова — это держит сервис тонким и
// фиксирует точку транзакции.
type (
	Repo               = kacho.Repository
	Reader             = kacho.RepositoryReader
	Writer             = kacho.RepositoryWriter
	GatewayReaderIface = kacho.GatewayReaderIface
	GatewayWriterIface = kacho.GatewayWriterIface
	OutboxEmitter      = kacho.OutboxEmitter
)

// ProjectClient — то, что use-case'ам Gateway нужно от peer-сервиса
// kacho-iam: проверка существования project'а.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ListFilter — port per-object List-фильтра. Реализация —
// `authzfilter.AsPort(*authzfilter.FGAFilter)`. nil → unfiltered passthrough.
// bypass=false && len(allowedIDs)==0 → пустой List (no-leak).
type ListFilter interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}
