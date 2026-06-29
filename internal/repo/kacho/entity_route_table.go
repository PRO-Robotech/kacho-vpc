// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// RouteTableRecord — repo-entity для RouteTable: domain.RouteTable плюс
// DB-managed CreatedAt (parity с NetworkRecord).
//
// Dependency rule — см. doc-комментарий пакета в `entity_network.go`.
type RouteTableRecord struct {
	domain.RouteTable
	CreatedAt time.Time
}
