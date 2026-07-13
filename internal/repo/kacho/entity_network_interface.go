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

// AttachNICParams — self-describing payload NIC↔Instance attach (compute-initiated).
// vpc валидирует СВОИ строки network_interfaces + subnets против этих значений и
// НИКОГДА не зовёт compute (ацикличность, ретроспектива KAC-266).
//
// Index — слот в инстансе: >=0 → явный слот; <0 → авто-назначение первого свободного
// (repo вычисляет в той же TX; concurrency держит partial UNIQUE + retry в service).
type AttachNICParams struct {
	NICID          string
	InstanceID     string
	InstanceName   string
	InstanceZoneID string
	ProjectID      string
	Index          int32
}

// AutoIndex — сентинел «слот не задан» для AttachNICParams.Index (авто-назначение).
const AutoIndex int32 = -1

// NetworkInterfaceAttachment — одна NIC-привязка, обогащённая instance-local слотом
// (Index — его нет на публичном NetworkInterface message) и денормализованным
// зеркалом адресации NIC. Питает batched ListByInstance → compute-side зеркало
// Instance.network_interfaces[] (source of truth = kacho-vpc NetworkInterface).
type NetworkInterfaceAttachment struct {
	NICID            string
	InstanceID       string
	Index            int32
	SubnetID         string
	PrimaryV4Address string
	PrimaryV6Address string
	SecurityGroupIDs []string
	MAC              string
}
