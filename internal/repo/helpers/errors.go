// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package helpers — общие helper'ы repo-слоя kacho-vpc, экспортируемые для
// использования из CQRS pg-impl'ов в `internal/repo/kacho/pg/` (и адаптера
// `cqrsadapter`).
//
// Логика держится в одном месте, чтобы repo-impl'ы собирались без дублирования
// SQLSTATE-классификации, scan-логики и outbox-payload снимков.
//
// Содержимое:
//   - errors.go — sentinel-ошибки слоя repo (NotFound / AlreadyExists / ...).
//   - jsonb.go — marshal/unmarshal JSONB helpers.
//   - outbox.go — EmitVPC + DomainToMap для outbox-payload снимков.
//   - paging.go — Encode/DecodePageToken + InvalidPageTokenErr / InvalidFilterErr.
//   - unique.go — SQLSTATE-классификаторы + WrapPgErr.
//   - sql.go — JoinAnd / NullableStr / NormalizeMap / MarshalDhcp / MarshalStaticRoutes.
//   - scans.go — column-list-константы и scan-функции по 10 ресурсам.
//   - payloads.go — payload-функции для outbox-snapshots.
//   - nic.go — NIC-specific (NIStatusName / NIStatusFromName / OrEmptyStrSlice).
//   - sg.go — WrapSGErr / WrapGatewayErr (стабильный error-text per kind).
//   - freelist_sql.go — AllocateFromFreelistSQL (PG-native v4 freelist allocator).
package helpers

import "errors"

// ErrNotFound — ресурс не найден.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists — UNIQUE-constraint violation.
var ErrAlreadyExists = errors.New("already exists")

// ErrInvalidArg — некорректные входные данные.
var ErrInvalidArg = errors.New("invalid argument")

// ErrFailedPrecondition — FK violation и др. state-related ошибки.
// Маппится в gRPC FailedPrecondition (например, "network is not empty").
var ErrFailedPrecondition = errors.New("failed precondition")

// ErrInternal — generic-ошибка для неклассифицированных DB-проблем.
// Маппится на gRPC Internal с фиксированным сообщением (no leak).
var ErrInternal = errors.New("internal database error")

// ErrPoolNotResolved — ни один шаг IPAM cascade не дал результат.
var ErrPoolNotResolved = errors.New("no address pool resolved")

// ErrInvalidIPv4 — попытка allocate IP из не-IPv4 CIDR.
var ErrInvalidIPv4 = errors.New("not ipv4")

// ErrMacCollision — UNIQUE-violation именно по mac_address при INSERT NIC.
// Service-слой использует, чтобы отличить retry-able MAC-collision от
// duplicate-name (`ErrAlreadyExists`).
var ErrMacCollision = errors.New("network interface mac collision")

// ErrPoolExhausted — address_pool_free_ips пуст (PG-native freelist allocator).
// Service-слой маппит в gRPC FailedPrecondition.
var ErrPoolExhausted = errors.New("address pool exhausted")
