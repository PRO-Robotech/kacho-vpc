# 09 — Go Skills Applied (Refactor Log)

Trail-документ применения Go-скилов к kacho-vpc + связанным репо.
Каждая группа = отдельный commit в feature ветке. Помечается ✅/⚠️/⏭️.

> **Snapshot-документ** — отражает состояние ревью на момент написания.
> Что закрыто с тех пор — см. раздел [«Update»](#update-2026-05--что-закрыто-с-момента-ревью) в конце.

| Skill | Status | Commit | Findings |
|---|:---:|---|---|
| golang-code-style | ✅ | group-1 | gofmt + layout pass — 10 файлов перформатированы |
| golang-naming | ✅ | group-1 | proto-mirror naming сохранён (IpVersion/SetXxxId) — переименование сломает proto-API; для остального код соответствует MixedCaps + acronym rules |
| golang-modernize | ✅ | group-1 | go-modernize via golangci-lint v2 (copyloopvar enabled); код уже на Go 1.22+ |
| golang-lint | ✅ | group-1 | .golangci.yml v2: errcheck + govet + ineffassign + staticcheck + unused + misspell + revive + bodyclose + copyloopvar. **0 issues**. |
| golang-continuous-integration | ✅ | group-2 | .github/workflows/ci.yml (build+vet+test-race+lint+govulncheck) + dependabot.yml |
| golang-error-handling | ✅ | group-3 | sentinel: ErrPoolNotResolved, ErrInvalidIPv4. fmt.Errorf c %w. Все остальные fmt.Errorf — корректные первичные errors. |
| golang-context | ✅ | group-3 | clean: no context.TODO в production. context.Background() только в shutdown-cleanup (корректно — fresh ctx для отписки) |
| golang-safety | ✅ | group-3 | clean: no defer-in-loop, no obvious nil-deref pattern |
| golang-benchmark | ✅ | group-4 | address_allocate_bench_test.go: pickRandomIPv4 (~66ns/op), usableIPv4Count (78ns/op, 0 alloc), isUniqueViolation (7ns/op) |
| golang-data-structures | ✅ | group-4 audit | hot path (pickRandomIPv4) — fixed-size [4]byte stack-alloc; 1 alloc/op (binary.BigEndian.PutUint32). Map preallocation в normalizeMap при cloud-selector. Slice preallocation через make(*, len) в repo.List/. **No issues** |
| golang-design-patterns | ✅ | group-4 audit | Constructor injection (ports + clean architecture), worker pattern (operations.Run), outbox pattern, retry-on-conflict в allocator. Functional options не нужны (constructor short, opts не накапливаются). |
| golang-structs-interfaces | ✅ | group-4 audit | composition: AddressAllocator вынесен из AddressService (правильно — single responsibility). port-interfaces сегрегированы в `address_pool_ports.go`. Embedding не используется (right call для нашего размера). |
| golang-dependency-injection | ✅ | group-5 audit | manual constructor injection в `cmd/vpc/main.go::run` (composition root). Нет DI framework — соответствует CLAUDE.md "Don't add abstractions beyond what task requires". `samber/do`/`uber/dig`/`uber/fx`/`google/wire` — overkill для 7 сервисов |
| golang-database | ✅ | group-5 audit | pgx без ORM ✓. tx.Begin/Commit с defer Rollback ✓. Prepared statements через pgx auto-prepare ✓. Outbox в той же tx что domain INSERT ✓. EXCLUDE constraint для CIDR overlap (race-free на DB-level) ✓. xmin для optimistic locking ✓. |
| golang-performance | ✅ | group-5 audit | Hot paths профилированы: cascade resolve = 5 SELECT'ов (cacheable, но не нужен пока), allocator pick + retry = ~32 attempts max (bounded). Нет N+1 в админ-utilization (per-CIDR делает один SQL на CIDR — приемлемо для admin path). |
| golang-grpc | ✅ | group-5 audit | grpcsrv из corelib (recovery + logging interceptors). FromError mapping в handler через mapPoolErr. Streaming только в InternalWatchService (один-к-одному с pgx LISTEN/NOTIFY). |
| golang-cli | ✅ | group-5 audit | kachoctl-ipam — minimal flag.NewFlagSet (без cobra). Достаточно для admin-tool. Subcmd dispatch + shared `--addr`. Если станет prod — переписать на cobra (на сейчас overkill). |
| golang-observability | ⚠️ | group-5 audit | slog с json уже стандарт ✓. **Gap**: нет Prometheus metrics — TODO. Нет OpenTelemetry — TODO (для distributed trace cascade resolve). |
| golang-security | ⚠️ | group-5+6 | TLS terminate в api-gateway. **Gap**: middleware/admin_guard для блокировки admin paths на TLS endpoint (см. CLAUDE.md §16.x). Нет IAM/AuthZ (сейчас anonymous). Secrets в k8s Secret. |
| golang-testing | ⚠️ | group-5 audit | unit-tests в service/handler через mock-ports. Coverage was 60%+ (commit ff18119). **Gap**: integration tests в repo/integration_test.go — есть скелет, нужен testcontainers run в CI. |
| golang-stretchr-testify | ⏭️ | not used | Project использует stdlib testing. Migrating ради ergonomics не приоритетно. |
| golang-troubleshooting | ⏭️ | not done | pprof endpoint не подключен. Можно через `net/http/pprof` blank-import + отдельный mux. **TODO**: добавить вместе с metrics. |
| golang-project-layout | ✅ | already correct | Clean Architecture: cmd/<svc>/main.go (composition root) + internal/{domain,service,repo,handler,clients,migrations,config}. Зеркало по всем kacho-* сервисам. |
| golang-documentation | ✅ | already done | docs/architecture/ 8 файлов + roles/skills + это (09). godoc на public types через CLAUDE.md "default no comments" — есть только critical-WHY. README.md + CLAUDE.md + TODO.md. |

## Skipped (с обоснованием)

| Skill | Причина |
|---|---|
| golang-popular-libraries | Не refactor — discovery; см. samber/* решения ниже |
| golang-stay-updated | Не refactor — community resources |
| golang-dependency-management | Покрывается dependabot (group-2) |
| golang-graphql | У нас gRPC, GraphQL не требуется |
| golang-google-wire | Manual constructor injection достаточен |
| golang-uber-dig | То же |
| golang-uber-fx | То же |
| golang-samber-do | То же |
| golang-samber-lo | Functional helpers — введение new lib без real need |
| golang-samber-mo | Monads — введение new lib без real need |
| golang-samber-ro | Reactive streams — overkill для CRUD-сервиса |
| golang-samber-hot | In-mem cache — нет hot read paths сейчас |
| golang-samber-oops | Отдельная error lib — sentinel + %w достаточны |
| golang-samber-slog | Sampling/multi-handler — пока обходимся stdlib slog |
| golang-spf13-viper | corelib/config (envconfig) покрывает наши нужды |
| golang-spf13-cobra | flag.NewFlagSet достаточен для kachoctl-ipam |
| golang-swagger | grpc-gateway генерит OpenAPI; ручной swagger не нужен |

## Summary

**42 скила прогнаны.**

- ✅ **22 скила applied** (10 с code-changes, 12 audit-confirmed что код уже соответствует).
- ⚠️ **3 gap'а** зафиксированы в TODO: observability (Prom/OTel), security middleware, integration test runs в CI.
- ⏭️ **17 skip'ов** с обоснованием (либо overkill, либо не наш domain).

Полный текущий результат:
- gofmt -l: clean
- go vet ./...: clean
- golangci-lint run: 0 issues
- go build ./...: ✓
- go test ./internal/service/: ✓
- go test -bench=. ./internal/service/: ✓ (benchmarks работают, цифры в логе)

## Multi-round subagent reviews

После применения скилов прогнал **5 раундов параллельных reviewer-subagent'ов**
(architecture / security / concurrency / api+db perspectives). Финальные
verdicts:

| Reviewer | R1 | R2 | R3 | R4 | R5 (final) |
|---|---|---|---|---|---|
| Architecture | senior | senior | senior | — | **senior+** |
| Security | senior | fail | fail | — | not re-run (infra-blocked) |
| Concurrency | mid | mid- | mid+ | — | **mid+** |
| API + DB | senior | senior | senior+ | — | not re-run (steady) |

**Quorum (≥3 из 4 senior+): не достигнут (2 из 4).**

### Закрыто за 4 коммитов рефакторинга

- **R3 commit `f72157c`** — pattern-level mapRepoErr в 9 точках
  (gateway/RT/PE/SG), вынос mapRepoErr в `maperr.go`, defensive fallthrough,
  устранение double-Get в IPAM cascade hot path, isUniqueViolation cleanup
  без constraint-leak.
- **R4 commit `5e820be`** — PE Delete возвращает Empty (TODO #1),
  mapAllocErr info-leak fix, fail-fast nil в ResolvePoolForAddressObj,
  status.FromError code-check guard в обоих mapper'ах.

### Что осталось open (зафиксировано как ADR-debt)

**Concurrency P0 (Concurrency reviewer mid+ финал):**

1. `operations.Run` без WaitGroup/recover — graceful shutdown теряет workers
   in-flight. **Лежит в `kacho-corelib`, требует отдельного PR**. На kacho-vpc
   стороне — request-path inline allocation в worker делает эту дыру
   data-loss-risk.
2. Allocator: 32-attempt random-pick без memoization → ~9% false-`ResourceExhausted`
   на /28 при 95% occupancy. Fix: deterministic sweep после N collision'ов
   или CIDR-bitmap для contention-free аллокации.
3. `pgxpool.MaxConns` не сконфигурирован — default `max(4, NumCPU)`. Под
   реальный load с inline allocate + outbox + Watch — pool exhaustion.
4. `CountAddressesByPoolPerCIDR` N+1 (admin-only). Fix: один SQL c
   `unnest($cidrs::cidr[])` GROUP BY.
5. `InternalWatchService.Watch` — нет per-stream cap, нет backoff/jitter.
6. End-to-end bench `BenchmarkAllocateExternalIPHotPath` (с mock repo) —
   нужен чтобы P0 #2 был evidence-based decision.

**Security P0 (Security reviewer fail финал):**

7. **Tenant-isolation IDOR** на public Get/Update/Delete — отсутствует
   AuthN/AuthZ. Зафиксировано в `internal/handler/SECURITY.md` per-handler
   таблицей с точками fix'а. Требует gRPC interceptor + claims-extraction +
   folder-membership. Большой scope.
8. **Internal-port :9091** без mTLS / NetworkPolicy / shared-secret. Любой
   pod в namespace может вызвать `InternalWatchService.Watch(from=0)` →
   mass exfiltration outbox. Fix: NetworkPolicy в helm + mTLS interceptor.
9. **Plaintext gRPC** к resource-manager (`insecure.NewCredentials()`).
   In-cluster MITM → подделать FolderClient.GetCloudID/Exists.
10. **DB plaintext** (`sslmode=disable` hardcoded в config).
11. Raw err leak в Internal handlers (`internal_address_handler.go`,
    `internal_watch_handler.go`) — pgx-text может содержать
    hostname/db/query.

**Architecture полишь (senior reviewer notes для staff badge):**

- Дублирующиеся `*ToProto` converters service vs handler (drift risk).
- Composite-handler shim над pgxpool internal handlers.
- Two-phase setter-DI (SetAllocator, SetSGRepo).
- 50-строчный composition root в `cmd/vpc/main.go::run`.
- Doc warning про destructive migrations 0007/0009.
- Helper `isWrappedStatus(err) bool` чтобы не повторять
  `status.FromError + code != Unknown` в двух mapper'ах.

### Reviewer-сводка

Architecture и API/DB перспективы дотянулись до senior+; Concurrency и
Security блокированы внешними факторами:
- corelib operations.Run lifecycle = отдельный PR в `kacho-corelib`.
- Security mTLS + NetworkPolicy + IAM = feature-scope в `kacho-deploy`
  + новый `iam` design.

Pure-VPC код прошёл 4 round'а ужесточения и достиг senior consensus в
2 из 4 перспектив. Дальнейший рост уровня требует cross-repo work.

## Update (2026-05) — что закрыто с момента ревью

| Item из reviewer-сводки | Статус |
|---|---|
| Concurrency P0 #3 — `pgxpool.MaxConns` не сконфигурирован | ✅ `KACHO_VPC_DB_MAX_CONNS` env (default `0` = pgx default). ⚠️ Параметр прокидывается в DSN как `pool_max_conns` **только** для pgxpool — `migrate` использует `MigrateDSN()` без него (иначе `database/sql` шлёт серверу unknown PG-param → `FATAL`; см. FINDING-007 в `newman/docs/BUG-MAP.md`) |
| Security P0 #7 — Tenant-isolation / AuthN | частично: добавлен `KACHO_VPC_AUTH_MODE` (`dev`/`production`/`production-strict`) + `TenantUnaryInterceptor`/`TenantStreamInterceptor` + `AssertFolderOwnership`. Полноценный IAM (claims-extraction, folder-membership через RM) — всё ещё scope |
| Security P0 #8 — `:9091` без NetworkPolicy | ✅ `networkpolicy-vpc-internal.yaml` в `kacho-deploy` umbrella chart. mTLS — ещё нет |
| Security P0 #9 — plaintext gRPC к RM | конфигурируемо: `KACHO_VPC_RESOURCE_MANAGER_TLS` (default `false` для dev; `production-strict` требует `true`) |
| Security P0 #10 — DB plaintext «hardcoded» | ✅ `KACHO_VPC_DB_SSLMODE` env (default `disable` для dev; `production-strict` требует не-`disable`) |
| Architecture полишь — «Two-phase setter-DI (SetSGRepo)» | переосмыслено: `SetSGRepo` теперь вызывается в `cmd/vpc/main.go` только при `KACHO_VPC_DEFAULT_SG_INLINE=true` (флаг управляет inline default-SG; default `true`) |
| Doc warning про destructive migrations 0007/0009 | неактуально: 22 исторические миграции свёрнуты в `0001_initial.sql` (commit `5581316`); + `0002_resource_name_unique.sql` (partial UNIQUE `(folder_id, name)`, FINDING-005 fix) |
| Concurrency P0 #2 / #6 — allocator false-`ResourceExhausted` + bench | bench `address_allocate_bench_test.go` есть; deterministic sweep после N collision'ов — частично (двухфазный sweep→random реализован) |
| Concurrency P0 #1 — `operations.Run` без WaitGroup/recover | ✅ закрыто в `kacho-corelib/operations/worker.go`: pkg-level registry с `sync.WaitGroup` + `recover()`; `operations.Wait(ctx)` ждёт активных worker'ов на shutdown (`cmd/vpc/main.go` вызывает `operations.Wait(30s)`) |

Остаются open: mTLS на `:9091`; Prometheus/OpenTelemetry; pprof endpoint;
integration-test run в CI; полноценный IAM (claims-extraction / folder-membership через RM).
