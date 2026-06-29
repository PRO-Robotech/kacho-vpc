// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressPoolRecord — repo-entity для AddressPool; единый Record-pattern с
// kacho.NetworkRecord / kacho.AddressRecord / kacho.SubnetRecord для всех
// ресурсов VPC.
//
// CreatedAt/ModifiedAt — DB-managed timestamps; они живут в Record, а НЕ в
// domain.AddressPool (паритет с SubnetRecord). Проставляются repo-слоем (pg
// Insert/Update) и читаются обратно через RETURNING; source of truth — БД.
type AddressPoolRecord struct {
	domain.AddressPool
	CreatedAt  time.Time
	ModifiedAt time.Time
}
