# 09 — Принципы Go-стиля и их применение

Свод инженерных принципов, которым следует код kacho-vpc, и того, как они выражены
в репозитории. Это не история разработки, а описание текущего состояния и обоснований.

## Code style и инструменты

| Принцип | Состояние в репо |
|---|---|
| Форматирование | `gofmt` — clean; единый layout пакетов по Clean Architecture |
| Naming | MixedCaps + acronym rules; proto-mirror naming (`IpVersion`, `SetXxxId`) сохранен — переименование сломало бы proto-API |
| Современный Go | код на Go 1.22+; `copyloopvar` включен в линтере |
| Линтинг | `.golangci.yml` v2: errcheck + govet + ineffassign + staticcheck + unused + misspell + revive + bodyclose + copyloopvar — 0 issues |
| CI | `.github/workflows/ci.yml` (build + vet + test-race + lint + govulncheck) + dependabot |

## Error handling и context

- Sentinel-ошибки (`ErrPoolNotResolved`, `ErrInvalidIPv4`, и пр.) + оборачивание через
  `fmt.Errorf(... %w ...)`. Наружу из репо/сервиса не утекает pgx-текст — INTERNAL
  отдается с фиксированным сообщением.
- `context` чистый: нет `context.TODO` в production-path; `context.Background` только в
  shutdown-cleanup (fresh ctx для отписки — корректно).
- Безопасность: нет defer-in-loop, нет очевидных nil-deref паттернов.

## Структуры, интерфейсы, DI

- Constructor injection (порты + Clean Architecture); композиционный корень —
  единственное место wiring (`cmd/vpc/main.go::run`). DI-фреймворк не используется —
  для сервиса такого размера это лишняя абстракция.
- `AddressAllocator` вынесен из `AddressService` (single responsibility); порт-интерфейсы
  сегрегированы в `address_pool_ports.go`. Embedding не используется намеренно.
- Паттерны: worker (`operations.Run`), transactional outbox, retry-on-conflict в
  allocator. Functional options не нужны (конструкторы короткие, опции не накапливаются).

## База данных

- pgx без ORM. `tx.Begin/Commit` с `defer Rollback`. Prepared statements через pgx
  auto-prepare. Outbox-запись — в той же tx, что и доменный INSERT.
- `EXCLUDE`-constraint для CIDR overlap (race-free на DB-уровне); `xmin` для optimistic
  locking; `FOR UPDATE SKIP LOCKED` для freelist-аллокации.

## Производительность

- Hot paths профилированы: cascade resolve — несколько SELECT'ов (cacheable, но кэш пока
  не нужен); allocator pick + retry — bounded по числу попыток.
- Микробенчмарки: `internal/repo/address_pool_freelist_bench_test.go`
  (`pickRandomIPv4`, `usableIPv4Count`, `isUniqueViolation`).
- Hot path `pickRandomIPv4` — fixed-size `[4]byte` stack-alloc; map/slice
  preallocation через `make(*, len)` в `repo.List`.

## gRPC и observability

- `grpcsrv` из corelib (recovery + logging interceptors). `FromError`-маппинг в handler.
  Все RPC unary (read — sync, мутации — async через `Operation`); server-streaming RPC нет —
  изменения наблюдаются через polling `List` / `OperationService.Get`.
- `slog` (json) — стандарт логирования.

## Тестирование

- unit-тесты сервис/handler через mock-порты; integration через testcontainers (CRUD,
  EXCLUDE/FK/UNIQUE, CAS/OCC/SKIP-LOCKED races, outbox); e2e через api-gateway (newman).

## Принятые архитектурные решения и известные ограничения

Перечень осознанно выбранных компромиссов и того, что закрыто на DB-/код-уровне:

- **AuthN/AuthZ.** Per-RPC authz-gate через `InternalIAMService.Check`; режим
  поведения управляется `KACHO_VPC_AUTH_MODE`. Полноценная интеграция IAM
  (claims-extraction, project-membership) приходит с интеграцией IAM.
- **Internal listener `:9091`.** Защищен NetworkPolicy (umbrella chart). mTLS — план.
- **Connection pool.** `KACHO_VPC_DB_MAX_CONNS` прокидывается в DSN только для pgxpool;
  `migrate` использует отдельный `MigrateDSN` без этого параметра (иначе `database/sql`
  шлет серверу неизвестный PG-параметр → `FATAL`).
- **TLS к kacho-iam / БД.** Конфигурируемо: `KACHO_VPC_IAM_TLS`, `KACHO_VPC_DB_SSLMODE`
  (для dev — `disable`, в строгом production-режиме требуется не-`disable`).
- **Default SG.** `SetSGRepo` вызывается в композиционном корне только при
  `KACHO_VPC_DEFAULT_SG_INLINE=true` (флаг управляет inline default-SG).
- **Graceful shutdown воркеров.** `operations.Run` использует pkg-level registry с
  `sync.WaitGroup` + `recover`; `operations.Wait(ctx)` ждет активных воркеров на shutdown
  (`cmd/vpc/main.go` вызывает `operations.Wait(30s)`).
- **IPv6 allocator.** Sparse counter-based (материализованный freelist на /64 нереален —
  18 квинтиллионов адресов); двухфазный sweep → random для contention.

### Остаются открытыми (observability / security)

- Prometheus metrics и OpenTelemetry (distributed trace для cascade resolve).
- pprof endpoint.
- mTLS на `:9091`.

Эти пункты ведутся как GitHub Issues с метками `observability-gap` / `enhancement`.
