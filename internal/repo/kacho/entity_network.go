// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package kacho — repo-leaf entities (per-resource DTO между domain и SQL-схемой
// kacho_vpc). Здесь живут структуры типа `<X>Record`, представляющие
// «row из таблицы + DB-managed поля» (`CreatedAt`, в будущем — `UpdatedAt`,
// `Generation`).
//
// Зачем отдельный пакет: `CreatedAt` — DB-managed, не часть domain-сущности,
// поэтому `<X>Record = domain.X + CreatedAt` живет рядом с repo-имплементацией,
// а не в domain (domain — чистый, без знания про SQL/DB). Все Record-структуры
// собраны в одном leaf-пакете `internal/repo/kacho/`, без под-папок
// per-resource, — это минимизирует число packages под Clean Architecture.
//
// Dependency rule:
//
//	dto/type2pb → repo/kacho → domain
//	apps/kacho/api/<res>/{ports,handler,helpers,...} → repo/kacho → domain
//	repo (pgxpool implementations) → repo/kacho → domain
//	cmd/vpc/main.go → repo/kacho (через repo-implementations)
//
// Импорт: только stdlib `time` + `internal/domain`. Никаких pgx/grpc/proto.
package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkRecord — repo-entity для Network: domain.Network плюс DB-managed
// CreatedAt. Service-слой получает *NetworkRecord из репозитория (port
// `NetworkRepo` в `internal/service` / `internal/apps/kacho/api/network`) и
// пробрасывает в DTO/handler. Через proto клиенту уходит CreatedAt из этой
// структуры (truncate до секунд, см. `dto/type2pb/time.go`).
type NetworkRecord struct {
	domain.Network
	CreatedAt time.Time
}
