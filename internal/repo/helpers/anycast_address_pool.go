// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// AnycastAddressPoolCols — список колонок anycast_address_pools в порядке,
// ожидаемом ScanAnycastAddressPool. cidr_blocks — денормализованный text[]-вид
// (источник истины блоков — child anycast_address_pool_cidrs).
const AnycastAddressPoolCols = `id, project_id, created_at, name, description, labels, scope, ip_version, cidr_blocks, is_default, status`

// ScanAnycastAddressPool — row-scanner для AnycastAddressPoolRecord. scope/
// ip_version/status хранятся как text-enum в БД и конвертируются в domain-enum.
func ScanAnycastAddressPool(row Scannable) (*kachorepo.AnycastAddressPoolRecord, error) {
	var rec kachorepo.AnycastAddressPoolRecord
	var labelsJSON []byte
	var name, description, scope, ipVersion, status string
	var blocks pgtype.Array[string]

	err := row.Scan(
		&rec.ID, &rec.ProjectID, &rec.CreatedAt, &name, &description, &labelsJSON,
		&scope, &ipVersion, &blocks, &rec.IsDefault, &status,
	)
	if err != nil {
		return nil, err
	}
	rec.Name = domain.RcNameVPC(name)
	rec.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "AnycastAddressPool.labels"); err != nil {
		return nil, err
	}
	rec.Labels = domain.LabelsFromMap(labels)
	if blocks.Valid {
		rec.CIDRBlocks = blocks.Elements
	}
	rec.Scope = AnycastScopeFromText(scope)
	rec.IPVersion = IPVersionFromText(ipVersion)
	rec.Status = AnycastStatusFromText(status)
	return &rec, nil
}

// Текстовые представления enum'ов anycast-пула в БД (CHECK-констрейнты миграции).
// Конвертеры держат domain-enum (числовой) и DB-text синхронными в одном месте.

// AnycastScopeToText — domain AnycastScope → DB-text. Неизвестное → "INTERNAL"
// (фаза 1; service-слой не пропускает иные значения до repo).
func AnycastScopeToText(s domain.AnycastScope) string {
	switch s {
	case domain.AnycastScopeExternal:
		return "EXTERNAL"
	default:
		return "INTERNAL"
	}
}

// AnycastScopeFromText — DB-text → domain AnycastScope.
func AnycastScopeFromText(s string) domain.AnycastScope {
	switch s {
	case "EXTERNAL":
		return domain.AnycastScopeExternal
	default:
		return domain.AnycastScopeInternal
	}
}

// IPVersionToText — domain IpVersion → DB-text (IPV4 / IPV6).
func IPVersionToText(v domain.IpVersion) string {
	if v == domain.IpVersionIPv6 {
		return "IPV6"
	}
	return "IPV4"
}

// IPVersionFromText — DB-text → domain IpVersion.
func IPVersionFromText(s string) domain.IpVersion {
	if s == "IPV6" {
		return domain.IpVersionIPv6
	}
	return domain.IpVersionIPv4
}

// AnycastStatusToText — domain AnycastPoolStatus → DB-text.
func AnycastStatusToText(s domain.AnycastPoolStatus) string {
	switch s {
	case domain.AnycastPoolStatusCreating:
		return "CREATING"
	case domain.AnycastPoolStatusDeleting:
		return "DELETING"
	default:
		return "ACTIVE"
	}
}

// AnycastStatusFromText — DB-text → domain AnycastPoolStatus.
func AnycastStatusFromText(s string) domain.AnycastPoolStatus {
	switch s {
	case "CREATING":
		return domain.AnycastPoolStatusCreating
	case "DELETING":
		return domain.AnycastPoolStatusDeleting
	default:
		return domain.AnycastPoolStatusActive
	}
}
