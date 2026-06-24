// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// GatewayRecord живет в repo-leaf, а не в domain-сущности: `CreatedAt` —
// DB-managed, поэтому физически держится в repo-проекции, тогда как domain
// описывает только «намерение» / CRUD-payload.
//
// Импорт-граф: `internal/repo/kacho` импортирует `internal/domain` (parity с
// `entity_network.go`); никаких pgx/grpc/proto в этом пакете — он остается
// «leaf-оберткой» над domain.

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GatewayRecord — repo-entity для Gateway: domain.Gateway плюс DB-managed
// CreatedAt. Service-/use-case-слой получает *GatewayRecord из репозитория
// (port `GatewayRepo` в `internal/apps/kacho/api/gateway`) и пробрасывает в
// DTO/handler. Через proto клиенту уходит CreatedAt из этой структуры
// (truncate до секунд, см. `dto/toproto/time.go`).
type GatewayRecord struct {
	domain.Gateway
	CreatedAt time.Time
}
