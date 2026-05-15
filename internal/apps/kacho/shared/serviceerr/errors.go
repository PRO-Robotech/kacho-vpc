// Package serviceerr — sentinel-ошибки сервисного слоя и единый mapper
// repo-ошибок в gRPC status'ы.
//
// Wave 3 cleanup (KAC-94): перенесено из `internal/service/{errors,maperr}.go`
// в shared-пакет согласно skill evgeniy §1 A.3. Ошибки — тот же error-value,
// что и в `internal/ports`, так что `errors.Is(err, serviceerr.ErrNotFound)`
// работает идентично прежнему `service.ErrNotFound`.
package serviceerr

import "github.com/PRO-Robotech/kacho-vpc/internal/ports"

// Sentinel-ошибки живут в leaf-пакете `internal/ports` (см. TODO #12) — это
// позволяет общему test-helper'у `internal/ports/portmock` возвращать их без
// зависимости от service-слоя. Здесь — ре-экспорт через `var`-alias'ы.

// ErrNotFound возвращается, когда ресурс не найден.
var ErrNotFound = ports.ErrNotFound

// ErrAlreadyExists возвращается при нарушении UNIQUE constraint.
var ErrAlreadyExists = ports.ErrAlreadyExists

// ErrInvalidArg возвращается при некорректных входных данных.
var ErrInvalidArg = ports.ErrInvalidArg

// ErrFailedPrecondition возвращается, когда операция отклонена из-за состояния
// ресурса (например, попытка удалить Network с зависимыми Subnets — нарушение FK
// в Postgres SQLSTATE 23503). Маппится в gRPC FailedPrecondition (как у YC:
// "Network is not empty").
var ErrFailedPrecondition = ports.ErrFailedPrecondition

// ErrInternal — generic-ошибка для неклассифицированных DB-проблем. Маппится
// на gRPC Internal с фиксированным сообщением, чтобы не leak'ать pgx-текст.
var ErrInternal = ports.ErrInternal

// ErrPoolNotResolved — ни один шаг IPAM cascade не дал результат.
// Маппится в FailedPrecondition. Тестируется через errors.Is.
var ErrPoolNotResolved = ports.ErrPoolNotResolved

// ErrInvalidIPv4 — попытка allocate IP из не-IPv4 CIDR.
// Маппится в InvalidArgument.
var ErrInvalidIPv4 = ports.ErrInvalidIPv4

// ErrMacCollision — нарушение UNIQUE-constraint по network_interfaces.mac_address.
// Сигнал для NIC use-case'а CreateNetworkInterfaceUseCase сгенерировать новый
// MAC и повторить Insert.
var ErrMacCollision = ports.ErrMacCollision

// ErrPoolExhausted — address_pool_free_ips пуст (PG-native freelist allocator,
// миграция 0015). Маппится в FailedPrecondition. Repo также использует тот же
// error-value (через `repo.ErrPoolExhausted = ports.ErrPoolExhausted`), поэтому
// `errors.Is(err, serviceerr.ErrPoolExhausted)` сработает на ошибке из repo.
var ErrPoolExhausted = ports.ErrPoolExhausted
