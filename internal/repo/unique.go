package repo

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6: реальная реализация — в `internal/repo/helpers/unique.go`.
// Тонкие unexported алиасы оставлены для legacy `*_repo.go`, которые будут
// удалены в Sub-PR 6 (после чего этот файл удаляется вместе с пакетом repo).

// isUniqueViolation — alias на helpers.IsUniqueViolation.
func isUniqueViolation(err error) bool { return helpers.IsUniqueViolation(err) }

// nicMacUniqueConstraint — имя UNIQUE-индекса на network_interfaces.mac_address.
const nicMacUniqueConstraint = helpers.NICMacUniqueConstraint

// isNICMacCollision — alias на helpers.IsNICMacCollision.
func isNICMacCollision(err error) bool { return helpers.IsNICMacCollision(err) }

// isFKViolation — alias на helpers.IsFKViolation.
func isFKViolation(err error) bool { return helpers.IsFKViolation(err) }

// isExclusionViolation — alias на helpers.IsExclusionViolation.
func isExclusionViolation(err error) bool { return helpers.IsExclusionViolation(err) }

// isCheckViolation — alias на helpers.IsCheckViolation.
func isCheckViolation(err error) bool { return helpers.IsCheckViolation(err) }

// ycKindText — alias на helpers.YCKindText.
func ycKindText(kind string) string { return helpers.YCKindText(kind) }

// isInvalidUUID — alias на helpers.IsInvalidUUID.
func isInvalidUUID(err error) bool { return helpers.IsInvalidUUID(err) }

// wrapPgErr — alias на helpers.WrapPgErr.
func wrapPgErr(err error, kind, id string) error { return helpers.WrapPgErr(err, kind, id) }
