package helpers

import "github.com/PRO-Robotech/kacho-vpc/internal/domain"

// OrEmptyStrSlice: nil → empty slice (для JSONB-сериализации; иначе `null`
// вместо `[]` в БД-колонке). Используется в NIC-сценариях для
// security_group_ids/v4_address_ids/v6_address_ids.
func OrEmptyStrSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// NIStatusName — domain enum → DB column text (status TEXT в network_interfaces).
func NIStatusName(s domain.NetworkInterfaceStatus) string {
	switch s {
	case domain.NIStatusProvisioning:
		return domain.NIStatusStrProvisioning
	case domain.NIStatusActive:
		return domain.NIStatusStrActive
	case domain.NIStatusAvailable:
		return domain.NIStatusStrAvailable
	case domain.NIStatusFailed:
		return domain.NIStatusStrFailed
	case domain.NIStatusDeleting:
		return domain.NIStatusStrDeleting
	default:
		return domain.NIStatusStrUnspecified
	}
}

// NIStatusFromName — DB column text → domain enum.
func NIStatusFromName(s string) domain.NetworkInterfaceStatus {
	switch s {
	case domain.NIStatusStrProvisioning:
		return domain.NIStatusProvisioning
	case domain.NIStatusStrActive:
		return domain.NIStatusActive
	case domain.NIStatusStrAvailable:
		return domain.NIStatusAvailable
	case domain.NIStatusStrFailed:
		return domain.NIStatusFailed
	case domain.NIStatusStrDeleting:
		return domain.NIStatusDeleting
	default:
		return domain.NIStatusUnspecified
	}
}
