// Package helpers — общие helper'ы repo-слоя kacho-vpc, экспортируемые для
// использования из всех repo-impl'ов (legacy `*_repo.go` в `internal/repo/` и
// CQRS-impl в `internal/repo/kacho/pg/`).
//
// Wave 5 finale (KAC-94 A.7 sub-PR 4/6): вынесены сюда из `internal/repo/`
// (где раньше были unexported + re-exported через `shim_kacho.go`), чтобы:
//
//  - убрать дублирование (shim_kacho был ~280 строк ручных camelCase→PascalCase
//    re-export'ов);
//  - дать CQRS-impl `internal/repo/kacho/pg/` чистый import-path без зависимости
//    от `internal/repo` (тот пакет временный — будет удалён в Sub-PR 6 вместе с
//    11 legacy `*_repo.go`).
//
// Legacy `*_repo.go` продолжают использовать unexported алиасы внутри `internal/repo`
// (см. `internal/repo/errors.go` и др.), которые делегируют сюда — это позволяет
// собрать оба пакета (legacy + CQRS-pg) без duplication логики.
//
// Содержимое:
//
//  - `errors.go`     — sentinel-ошибки слоя repo (NotFound / AlreadyExists / ...);
//  - `jsonb.go`      — marshal/unmarshal JSONB helpers;
//  - `outbox.go`     — emitVPC + domainToMap для outbox-payload снимков;
//  - `paging.go`     — encode/decodePageToken + invalidPageTokenErr / invalidFilterErr;
//  - `unique.go`     — SQLSTATE-классификаторы (23505/23503/23514/23P01/22P02)
//                      + wrapPgErr (главный mapper PG-ошибок в sentinel'ы);
//  - `sql.go`        — joinAnd / nullableStr / normalizeMap;
//  - `scans/cols.go` — column-list-константы и scan-функции по 10 ресурсам;
//  - `payloads.go`   — payload-функции для outbox-snapshots.
package helpers

import "errors"

// ErrNotFound — ресурс не найден.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists — UNIQUE-constraint violation.
var ErrAlreadyExists = errors.New("already exists")

// ErrInvalidArg — некорректные входные данные.
var ErrInvalidArg = errors.New("invalid argument")

// ErrFailedPrecondition — FK violation и др. state-related ошибки.
// Маппится в gRPC FailedPrecondition (verbatim YC: "Network is not empty").
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

// ErrPoolExhausted — address_pool_free_ips пуст (PG-native freelist allocator,
// миграция 0015). Service-слой маппит в gRPC FailedPrecondition.
var ErrPoolExhausted = errors.New("address pool exhausted")
