package repo

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6: реальная реализация — в `internal/repo/helpers/unique.go`.
// Тонкие unexported алиасы оставлены для legacy `*_repo.go` (только те, что
// реально используются — остальные удалены как unused; после Sub-PR 6 этот
// файл уйдёт целиком вместе с пакетом repo).

// isNICMacCollision — alias на helpers.IsNICMacCollision. Используется
// network_interface_repo.go для retry-on-collision MAC-allocation.
func isNICMacCollision(err error) bool { return helpers.IsNICMacCollision(err) }

// isFKViolation — alias на helpers.IsFKViolation. Используется
// security_group_repo.go / route_table_repo.go для FK-detection.
func isFKViolation(err error) bool { return helpers.IsFKViolation(err) }

// isExclusionViolation — alias на helpers.IsExclusionViolation. Используется
// subnet_repo.go для распознавания CIDR-overlap (SQLSTATE 23P01).
func isExclusionViolation(err error) bool { return helpers.IsExclusionViolation(err) }

// wrapPgErr — alias на helpers.WrapPgErr. Используется всеми *_repo.go для
// классификации pgx-ошибок в repo-sentinel'ы.
func wrapPgErr(err error, kind, id string) error { return helpers.WrapPgErr(err, kind, id) }
