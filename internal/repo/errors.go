package repo

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// Sentinel-ошибки слоя repo. KAC-94 A.7 sub-PR 4/6: оригинал переехал в
// `internal/repo/helpers/errors.go` (общий пакет helpers для всех repo-impl).
// Legacy `*_repo.go` продолжают пользоваться этими именами (`repo.ErrNotFound`
// и т.п.); внутри они теперь — те же `var = helpers.ErrXxx` (single source
// of truth для `errors.Is`-семантики). `internal/service` тоже ре-экспортирует
// их через alias'ы — `errors.Is` работает прозрачно через все слои.
//
// После Sub-PR 6 (удаление 11 legacy *_repo.go) эти aliases уйдут вместе с
// пакетом `repo`; все callers — на `helpers.ErrXxx` напрямую (или через
// re-export в `internal/service`).

// ErrNotFound возвращается, когда ресурс не найден.
var ErrNotFound = helpers.ErrNotFound

// ErrAlreadyExists возвращается при нарушении UNIQUE constraint.
var ErrAlreadyExists = helpers.ErrAlreadyExists

// ErrInvalidArg возвращается при некорректных входных данных.
var ErrInvalidArg = helpers.ErrInvalidArg

// ErrFailedPrecondition возвращается, когда операция отклонена из-за состояния
// ресурса (например, попытка удалить Network с зависимыми Subnets — нарушение FK
// в Postgres SQLSTATE 23503). Маппится в gRPC FailedPrecondition.
var ErrFailedPrecondition = helpers.ErrFailedPrecondition

// ErrInternal — generic-ошибка для неклассифицированных DB-проблем. Маппится
// на gRPC Internal с фиксированным сообщением, чтобы не leak'ать pgx-текст.
var ErrInternal = helpers.ErrInternal

// ErrPoolNotResolved — ни один шаг IPAM cascade не дал результат.
var ErrPoolNotResolved = helpers.ErrPoolNotResolved

// ErrInvalidIPv4 — попытка allocate IP из не-IPv4 CIDR.
var ErrInvalidIPv4 = helpers.ErrInvalidIPv4

// ErrMacCollision — нарушение UNIQUE-constraint конкретно по mac_address при
// INSERT NIC.
var ErrMacCollision = helpers.ErrMacCollision

// ErrPoolExhausted — address_pool_free_ips пуст для запрошенного pool_id.
var ErrPoolExhausted = helpers.ErrPoolExhausted
