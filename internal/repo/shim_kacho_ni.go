package repo

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Shim для пакета `internal/repo/kacho/pg`: экспортирует NIC-specific helper'ы.
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): NIC переезжает на CQRS;
// CQRS-impl `kacho/pg/network_interface.go` живёт в отдельном пакете и не
// может видеть unexported-имена `repo` (`niCols`, `scanNI`, `isNICMacCollision`,
// `niStatusName`, `niStatusFromName`, `orEmptyStrSlice`, `networkInterfacePayload`).
//
// Экспортируем только нужный subset, остальное остаётся unexported (live-копия
// SQL-семантики — пока legacy NICRepo не удалён). Альтернатива (отдельный
// shared-helper-package) — крупный рефакторинг, выходит за scope replicate-фазы.

// NICCols — exported network_interfaces column list; используется
// kacho/pg/network_interface.go в SQL-запросах.
const NICCols = niCols

// ScanNIRec — exported alias of scanNI; возвращает *kacho.NetworkInterfaceRecord
// (= `repo.NetworkInterface` после Wave 5 replicate type-alias-смены).
func ScanNIRec(row Scannable) (*kachorepo.NetworkInterfaceRecord, error) {
	return scanNI(row)
}

// IsNICMacCollision — exported alias of isNICMacCollision; used by
// kacho/pg/network_interface.go для retry-on-collision MAC-allocation.
func IsNICMacCollision(err error) bool {
	return isNICMacCollision(err)
}

// NIStatusName — exported alias of niStatusName (domain enum → DB column text).
func NIStatusName(s domain.NetworkInterfaceStatus) string {
	return niStatusName(s)
}

// NIStatusFromName — exported alias of niStatusFromName (DB column text → domain enum).
func NIStatusFromName(s string) domain.NetworkInterfaceStatus {
	return niStatusFromName(s)
}

// OrEmptyStrSlice — exported alias of orEmptyStrSlice (nil-slice → empty slice
// для JSONB-сериализации; иначе `null` вместо `[]` в БД-колонке).
func OrEmptyStrSlice(s []string) []string {
	return orEmptyStrSlice(s)
}

// NetworkInterfacePayload — exported alias of networkInterfacePayload (snapshot
// для outbox-payload в use-case'е).
func NetworkInterfacePayload(rec *kachorepo.NetworkInterfaceRecord) map[string]any {
	return networkInterfacePayload(rec)
}
