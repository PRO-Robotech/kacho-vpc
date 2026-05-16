// Package serviceerr — sentinel-ошибки сервисного слоя и единый mapper
// repo-ошибок в gRPC status'ы.
//
// Wave 3 cleanup (KAC-94): перенесено из `internal/service/{errors,maperr}.go`
// в shared-пакет согласно skill evgeniy §1 A.3. Ошибки — тот же error-value,
// что и в `internal/repo`, так что `errors.Is(err, serviceerr.ErrNotFound)`
// работает идентично прежнему `service.ErrNotFound`.
package serviceerr

import "github.com/PRO-Robotech/kacho-vpc/internal/repo"

// Sentinel-ошибки живут в leaf-пакете `internal/repo` (см. TODO #12) — это
// позволяет общему test-helper'у `internal/repo/repomock` возвращать их без
// зависимости от service-слоя. Здесь — ре-экспорт через `var`-alias'ы.

// ErrNotFound возвращается, когда ресурс не найден.
var ErrNotFound = repo.ErrNotFound

// ErrAlreadyExists возвращается при нарушении UNIQUE constraint.
var ErrAlreadyExists = repo.ErrAlreadyExists

// ErrInvalidArg возвращается при некорректных входных данных.
var ErrInvalidArg = repo.ErrInvalidArg

// ErrFailedPrecondition возвращается, когда операция отклонена из-за состояния
// ресурса (например, попытка удалить Network с зависимыми Subnets — нарушение FK
// в Postgres SQLSTATE 23503). Маппится в gRPC FailedPrecondition (как у YC:
// "Network is not empty").
var ErrFailedPrecondition = repo.ErrFailedPrecondition

// ErrInternal — generic-ошибка для неклассифицированных DB-проблем. Маппится
// на gRPC Internal с фиксированным сообщением, чтобы не leak'ать pgx-текст.
var ErrInternal = repo.ErrInternal

// ErrPoolNotResolved — ни один шаг IPAM cascade не дал результат.
// Маппится в FailedPrecondition. Тестируется через errors.Is.
var ErrPoolNotResolved = repo.ErrPoolNotResolved

// ErrInvalidIPv4 — попытка allocate IP из не-IPv4 CIDR.
// Маппится в InvalidArgument.
var ErrInvalidIPv4 = repo.ErrInvalidIPv4

// ErrMacCollision — нарушение UNIQUE-constraint по network_interfaces.mac_address.
// Сигнал для NIC use-case'а CreateNetworkInterfaceUseCase сгенерировать новый
// MAC и повторить Insert.
var ErrMacCollision = repo.ErrMacCollision

// ErrPoolExhausted — address_pool_free_ips пуст (PG-native freelist allocator,
// миграция 0015). Маппится в FailedPrecondition. Repo также использует тот же
// error-value (через `repo.ErrPoolExhausted = repo.ErrPoolExhausted`), поэтому
// `errors.Is(err, serviceerr.ErrPoolExhausted)` сработает на ошибке из repo.
var ErrPoolExhausted = repo.ErrPoolExhausted
