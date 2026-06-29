// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressRecord — repo-entity для Address: domain.Address плюс DB-managed
// CreatedAt, который живет в repo-проекции, а не в domain.Address (parity с
// kacho.NetworkRecord).
//
// Service-/use-case-слой получает *AddressRecord из репозитория (port
// `AddressRepo` в `internal/apps/kacho/api/address`) и пробрасывает в DTO/
// handler. Через proto клиенту уходит CreatedAt из этой структуры (truncate
// до секунд, см. `dto/toproto/time.go`).
type AddressRecord struct {
	domain.Address
	CreatedAt time.Time
}
