package repo

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6 — переведено на тонкие aliases на `internal/repo/helpers`.
// NIC-specific shim'ы для `internal/repo/kacho/pg/network_interface.go`.

// NICCols — alias на helpers.NICCols.
const NICCols = helpers.NICCols

// ScanNIRec — alias на helpers.ScanNI.
func ScanNIRec(row Scannable) (*kachorepo.NetworkInterfaceRecord, error) {
	return helpers.ScanNI(row)
}

// IsNICMacCollision — alias на helpers.IsNICMacCollision.
func IsNICMacCollision(err error) bool {
	return helpers.IsNICMacCollision(err)
}

// NIStatusName — alias на helpers.NIStatusName.
func NIStatusName(s domain.NetworkInterfaceStatus) string {
	return helpers.NIStatusName(s)
}

// NIStatusFromName — alias на helpers.NIStatusFromName.
func NIStatusFromName(s string) domain.NetworkInterfaceStatus {
	return helpers.NIStatusFromName(s)
}

// OrEmptyStrSlice — alias на helpers.OrEmptyStrSlice.
func OrEmptyStrSlice(s []string) []string {
	return helpers.OrEmptyStrSlice(s)
}

// NetworkInterfacePayload — alias на helpers.NetworkInterfacePayload.
func NetworkInterfacePayload(rec *kachorepo.NetworkInterfaceRecord) map[string]any {
	return helpers.NetworkInterfacePayload(rec)
}
