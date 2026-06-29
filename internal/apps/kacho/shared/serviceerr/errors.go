// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package serviceerr — sentinel-ошибки сервисного слоя и единый mapper
// repo-ошибок в gRPC status'ы. Sentinel'ы — те же error-value, что и в
// `internal/repo`, поэтому `errors.Is(err, serviceerr.ErrNotFound)` срабатывает
// на ошибке из repo.
package serviceerr

import "github.com/PRO-Robotech/kacho-vpc/internal/repo"

// Sentinel-ошибки живут в leaf-пакете `internal/repo` — так общий test-helper
// `internal/repo/repomock` может возвращать их без зависимости от service-слоя.
// Здесь — ре-экспорт через `var`-alias'ы.

// ErrNotFound возвращается, когда ресурс не найден.
var ErrNotFound = repo.ErrNotFound

// ErrAlreadyExists возвращается при нарушении UNIQUE constraint.
var ErrAlreadyExists = repo.ErrAlreadyExists

// ErrInvalidArg возвращается при некорректных входных данных.
var ErrInvalidArg = repo.ErrInvalidArg

// ErrFailedPrecondition возвращается, когда операция отклонена из-за состояния
// ресурса (например, попытка удалить Network с зависимыми Subnets — нарушение FK
// в Postgres SQLSTATE 23503). Маппится в gRPC FailedPrecondition
// ("network is not empty").
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

// ErrPoolExhausted — таблица address_pool_free_ips пуста (PG-native freelist
// allocator). Маппится в FailedPrecondition; тот же error-value использует repo,
// поэтому `errors.Is(err, serviceerr.ErrPoolExhausted)` сработает на ошибке из repo.
var ErrPoolExhausted = repo.ErrPoolExhausted
