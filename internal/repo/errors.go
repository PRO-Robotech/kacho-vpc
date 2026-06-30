// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// Sentinel-ошибки слоя repo. Источник истины — `internal/repo/helpers/errors.go`
// (общий пакет helpers для всех repo-impl); здесь они переэкспортированы как
// `var = helpers.ErrXxx`, чтобы `errors.Is`-семантика была единой. `internal/service`
// тоже ре-экспортирует их через alias'ы — `errors.Is` работает прозрачно через все слои.

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

// ErrGuardMismatch — BYO ownership/family-guard SetReferenceGuarded не совпал
// (чужой проект/семейство либо адрес не существует). Service маппит в generic
// InvalidArgument "Illegal argument addressId" (анти-oracle).
var ErrGuardMismatch = helpers.ErrGuardMismatch
