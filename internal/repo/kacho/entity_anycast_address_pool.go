// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AnycastAddressPoolRecord — repo-entity для AnycastAddressPool; единый
// Record-pattern с прочими ресурсами VPC. CreatedAt — DB-managed timestamp,
// живёт в Record (не в domain), проставляется repo-слоем и читается через
// RETURNING; source of truth — БД.
type AnycastAddressPoolRecord struct {
	domain.AnycastAddressPool
	CreatedAt time.Time
}
