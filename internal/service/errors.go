package service

import "github.com/PRO-Robotech/kacho-vpc/internal/ports"

// Sentinel-ошибки живут в leaf-пакете `internal/ports` (см. TODO #12) — это
// позволяет общему test-helper'у `internal/ports/portmock` возвращать их без
// зависимости от `internal/service`. Здесь — ре-экспорт через `var`-alias'ы:
// это те же error-value, поэтому `errors.Is(err, service.ErrNotFound)` работает
// идентично прежнему поведению.

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
// Сигнал для NetworkInterfaceService.doCreate сгенерировать новый MAC и повторить.
var ErrMacCollision = ports.ErrMacCollision

// ErrPoolExhausted — address_pool_free_ips пуст (PG-native freelist allocator,
// миграция 0015). Маппится в FailedPrecondition. Repo также использует тот же
// error-value (через `repo.ErrPoolExhausted = ports.ErrPoolExhausted`), поэтому
// `errors.Is(err, service.ErrPoolExhausted)` сработает на ошибке из repo.
var ErrPoolExhausted = ports.ErrPoolExhausted
