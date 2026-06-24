// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkInterfaceRecord — repo-entity для NetworkInterface:
// domain.NetworkInterface плюс DB-managed CreatedAt (см. doc-комментарий на
// NetworkRecord в entity_network.go).
//
// NIC — самый сложный ресурс домена: attach-race protection (атомарный CAS),
// MAC-allocation с UNIQUE-constraint, v4/v6 cardinality CHECK, ON DELETE
// RESTRICT каскад, used_by мирроринг. Repo-leaf не «знает» про эти инварианты
// семантически — они держатся на DB-уровне; repo-entity несет только
// row-snapshot.
type NetworkInterfaceRecord struct {
	domain.NetworkInterface
	CreatedAt time.Time
}
