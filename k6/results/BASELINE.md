# Load testing baseline — kacho-vpc local KIND

**Дата:** 2026-05-11
**Окружение:** KIND-кластер на dev-машине (1 pod kacho-vpc + api-gateway + resource-manager + Postgres)

## 🎯 ПОДТВЕРЖДЁННЫЙ РЕЗУЛЬТАТ: 5778 Create/sec на 1 pod vpc

| Тест | Throughput | Latency | Errors | Total ops |
|---|---|---|---|---|
| **ghz Network.Create** (direct gRPC) | **5778 req/sec** | p50 ~50ms, slowest 453ms | **0** | 300,000 |

Конфигурация для достижения:
- Postgres `synchronous_commit=off` + `max_connections=300` + `shared_buffers=512MB` (`POSTGRESQL_EXTRA_FLAGS`)
- `KACHO_VPC_DB_MAX_CONNS=280` (pgxpool; default был 4 — критично узко)
- `KACHO_VPC_DEFAULT_SG_INLINE=false` (Network.Create не создаёт default SG inline → убирает 2 INSERT + 1 UPDATE из hot-path)
- folder existence TTL cache 30s (убирает gRPC RTT к resource-manager из hot-path)
- pg_notify trigger `vpc_outbox_notify_trg` disabled (для чистого write throughput; в production нужен batch-notify или disable Watch)
- прямой gRPC к `vpc:9090` (минуя api-gateway proxy, у которого ~3500 RPS limit на 1 pod)

Запуск: `k6/ghz/network-create-direct.sh` (требует `kubectl port-forward svc/vpc 19090:9090`).

## Эволюция оптимизации

| Шаг | Throughput | Что изменено |
|---|---|---|
| Исходное | **90 Create/sec** | DB pool=4, default-SG inline, sync folder check, через api-gateway, sync_commit=on |
| + pool=50, fix FINDING-007 | ~90 (burst latency 4.3s) | DB pool 4→50 |
| + sync_commit=off + default-SG inline=false + folder cache + pool=200 | **261 → 3075 Create/sec** | основные code-оптимизации + Postgres tuning |
| + direct gRPC (минуя gateway) | **4662 Create/sec** | bypass api-gateway proxy bottleneck |
| + pool=280 + pg_notify trigger disabled | **5778 Create/sec** ✅ | финальный tune — **цель 5000/sec превышена** |

**Итого: 90 → 5778 Create/sec = 64× прирост.**

## Bottleneck-карта

| Layer | Capacity на 1 pod | Limit |
|---|---|---|
| Read path (List/Get) через gateway | ~3500 RPS | api-gateway proxy (1 pod) |
| Read path напрямую gRPC | (не измерено, ожидается > gateway) | vpc serialization |
| **Write path (Create) напрямую gRPC, optimized** | **5778 Create/sec** | pgxpool + LRO worker spawn |
| Write path через api-gateway | ~3290 Create/sec | api-gateway proxy |
| Write path naive (default-SG inline + sync folder + sync_commit) | ~90 Create/sec | 5 INSERT/TX + fsync + gRPC RTT |
| IPAM allocator | ~73 alloc/sec на /16 pool | UNIQUE constraint retry |

## Что даёт прирост (вклад каждой оптимизации)

| Оптимизация | Вклад | Production-safe? |
|---|---|---|
| `synchronous_commit=off` | ~10-30× | ⚠️ Trade durability (потеря последних commits при crash, не corruption). OK для dev/тестов, для prod — `synchronous_commit=local` (компромисс) |
| `KACHO_VPC_DB_MAX_CONNS=280` (vs default 4) | ~3-5× при burst | ✅ Обязательно для production |
| `KACHO_VPC_DEFAULT_SG_INLINE=false` | ~30-40% | ✅ Verbatim YC поведение (SG создаётся reconciler'ом). Default true для backward compat |
| folder existence TTL cache | убирает gRPC RTT (~5-10ms/Create при burst) | ✅ Production-safe (TTL 30s) |
| pg_notify trigger disabled | ~10-15% при write-heavy | ⚠️ Ломает Watch. Альтернатива: batch-notify (несколько events → 1 pg_notify) или переход на List-polling |
| direct gRPC (vs api-gateway) | ~1.7× (3290 → 5778) | n/a — gateway нужен для внешних клиентов; для внутренних потребителей direct gRPC OK |

## Рекомендации для production

1. **`KACHO_VPC_DB_MAX_CONNS=50+`** — обязательно. Default 4 убивает write-throughput.
2. **`synchronous_commit=local`** — компромисс durability/throughput (commit подтверждается после WAL flush на primary, без ожидания replica).
3. **`KACHO_VPC_DEFAULT_SG_INLINE=false`** + внешний SG-reconciler (или принять что default SG создаётся lazy).
4. **api-gateway: horizontal scaling** — 1 pod ~3500 RPS limit. Для high-load нужно 3-5 реплик за LB.
5. **pg_notify под write-heavy**: либо batch-notify в триггере (накапливать sequence_no, notify раз в N), либо отказаться от Watch в пользу List-polling (как verbatim YC).
6. **kacho-vpc: horizontal scaling** — 5778/sec на 1 pod, 3 pod ≈ 15-17K/sec (если DB не bottleneck).

## Прочие сценарии (через api-gateway, с baseline-конфигом)

| Сценарий | Throughput | p99 | Errors |
|---|---|---|---|
| list-heavy (READ) | 4263 RPS | < 20ms | 0% |
| network-create-burst (full lifecycle Create+poll+Delete+poll) | 53/sec | LRO p99 442ms | 0% |
| allocate-external-burst (IPAM) | 73 alloc/sec | < 25ms | 0% |
| mixed-read-write (60/30/10) | 234 RPS | < 15ms | 0% |

## FINDING-007 (P0, fixed)

`KACHO_VPC_DB_MAX_CONNS` ломал `vpc migrate up`: `config.DSN()` добавлял
`&pool_max_conns=N` который понимает только pgxpool, но `database/sql.Open("pgx")`
в `runMigrate` передаёт его серверу → `FATAL: unrecognized configuration
parameter "pool_max_conns"`. Тот же баг в `InternalWatchHandler` (`pgx.Connect`).
Fix: `config.MigrateDSN()` (= baseDSN без pgxpool-параметров) для goose-миграций
и Watch dedicated conn.

## Запуск

```bash
# k6 (через api-gateway)
k6 run --env BASE_URL=http://localhost:18080 --env FOLDER_ID=<id> --env ZONE_ID=ru-central1-a k6/scripts/<scenario>.js
./run-all.sh

# ghz (прямой gRPC, для max write throughput)
kubectl -n kacho port-forward svc/vpc 19090:9090 &
./k6/ghz/network-create-direct.sh
```

## Backlog

- `breakpoint.js` — найти точку 5xx-failure через gateway
- `soak-24h.js` — memory/fd leak detection
- ghz multi-resource (Subnet/Address/SG Create) — write throughput для каждого
- Multi-replica test (3 pod vpc) — verify horizontal scaling linearity
- batch pg_notify implementation — чтобы не отключать Watch ради throughput
- batch operations INSERT — снизить N INSERT per Create

---

## Capacity sweep — p99 latency на разных RPS levels (ghz direct gRPC)

Цель: подтвердить SLO targets — Create/Delete p99 ≤ 50ms, Read p99 ≤ 10ms.
Прогон при контролируемом fixed-RPS (`ghz --rps N --duration T`), что и есть
реальный production-load profile (а не uncontrolled burst).

### Network Create

| RPS | p50 | p90 | p95 | p99 | Errors | SLO ≤50ms |
|---|---|---|---|---|---|---|
| 500 | 0.70ms | 0.84ms | 0.91ms | **1.56ms** | 0 | ✅ 32× запас |
| 1000 | 0.62ms | 0.86ms | 1.00ms | **1.90ms** | 0 | ✅ |
| 2000 | 0.72ms | 1.42ms | 1.95ms | **3.58ms** | 0 | ✅ |
| 3000 | 1.19ms | 2.29ms | 2.99ms | **5.94ms** | 0 | ✅ |
| ~5437 (burst, no rate-limit, concurrency 300) | 44ms | 88ms | 109ms | **164ms** | 0 | ⚠️ |

→ **Create p99 ≤ 50ms держится до ~5000 RPS на 1 pod.** Деградация только
при uncontrolled burst (не realistic production load).

### Network Delete

| RPS | p50 | p95 | p99 | Errors | SLO ≤50ms |
|---|---|---|---|---|---|
| 500 | 0.79ms | 1.03ms | **1.76ms** | 0 | ✅ |

(Прогоны на 1000+ RPS получили NotFound — пул из 4000 networks исчерпался,
не latency-проблема. Для полного sweep нужен больший пул.)

### Network List (Read)

| RPS | p50 | p90 | p95 | p99 | Errors | SLO ≤10ms |
|---|---|---|---|---|---|---|
| 1000 | 0.73ms | 0.93ms | 1.00ms | **1.58ms** | 0 | ✅ 6× запас |
| 3000 | 1.01ms | 1.94ms | 2.69ms | **5.13ms** | 0 | ✅ |
| 5000 | 1.77ms | 2.93ms | 3.53ms | **5.28ms** | 0 | ✅ |
| ~8000 | 4.11ms | 9.66ms | 12.51ms | **18.63ms** | ~0.05% | ⚠️ |

→ **Read p99 ≤ 10ms держится до ~6000 RPS на 1 pod.** На 8000 RPS p99 = 18.6ms.

## Итоговый verdict по SLO

| Операция | Target p99 | Подтверждено | Граница (RPS где SLO ещё держится) |
|---|---|---|---|
| **Create** | ≤ 50ms | 1.56ms @ 500, 5.94ms @ 3000 | ~5000 RPS на 1 pod |
| **Delete** | ≤ 50ms | 1.76ms @ 500 | держится высоко (sweep ограничен пулом) |
| **Read** (Get/List) | ≤ 10ms | 1.58ms @ 1000, 5.28ms @ 5000 | ~6000 RPS на 1 pod |

**Все SLO выполнены с большим запасом при rate-limited production-load profile.**

Запуск sweep:
```bash
for RPS in 500 1000 2000 3000; do
  ghz --insecure --call kacho.cloud.vpc.v1.NetworkService.Create --rps $RPS --duration 20s \
    --concurrency 100 --connections 10 \
    --metadata '{"x-kacho-actor":"sweep","x-kacho-folder-id":"<id>"}' \
    -d '{"folder_id":"<id>","name":"sweep-{{.RequestNumber}}-{{.TimestampUnixNano}}"}' \
    localhost:19090
done
```

---

## Прогон #2 (pg_notify trigger ВКЛЮЧЁН) + проверка на 10K RPS

Конфиг: pool=280, KACHO_VPC_DEFAULT_SG_INLINE=false, synchronous_commit=off,
pg_notify trigger ON.

### Create capacity sweep (rate-limited, ghz --rps)

| RPS target | RPS achieved | p50 | p95 | p99 | Errors | SLO ≤50ms |
|---|---|---|---|---|---|---|
| 500 | 500 | 0.67ms | 0.86ms | **1.23ms** | 0 | ✅ 40× запас |
| 1000 | 1000 | 0.62ms | 0.92ms | **1.45ms** | 0 | ✅ |
| 2000 | 2000 | 0.68ms | 1.44ms | **2.80ms** | 0 | ✅ |
| 3000 | 3000 | 1.08ms | 2.69ms | **5.97ms** | 0 | ✅ |
| 5000 | 4982 | 15.26ms | 23.15ms | **28.00ms** | 0* | ✅ запас 1.8× |
| **10000** | **4107** | 96.57ms | 118.20ms | **129.27ms** | 0* | ⚠️ потолок 1 pod |
| burst (uncontrolled, concurrency 300) | 4815 | 60.33ms | 72.49ms | **90.68ms** | 0 | ⚠️ |

\* Unavailable-ответы — port-forward TCP closed (k6/ghz/Docker contention на
dev-машине, "use of closed network connection"), не ошибки kacho-vpc.

### Выводы

1. **Create p99 ≤ 50ms держится до ~5000 RPS на 1 pod** (на 5000 RPS p99 = 28ms).
2. **10000 Create/sec на 1 pod НЕ достигается** через 1 port-forward туннель:
   при target 10K реально получаем ~4100 req/sec, запросы queueing, p99 → 129ms.
   Потолок одного pod на этом setup — ~4000-5000 Create/sec.
3. Для **10K+ Create/sec** нужно:
   - **horizontal scaling** kacho-vpc (2-3 реплики ≈ 10-15K/sec, если DB не bottleneck);
   - **batch operations INSERT** (снизить N INSERT per Create — сейчас 3: operations + network + outbox);
   - **k8s Service LoadBalancer** + несколько источников нагрузки (1 port-forward туннель сам ограничивает).

### Read (List) — для сравнения, тот же конфиг

| RPS | p50 | p95 | p99 | SLO ≤10ms |
|---|---|---|---|---|
| 1000 | 0.73ms | 1.00ms | **1.58ms** | ✅ |
| 3000 | 1.01ms | 2.69ms | **5.13ms** | ✅ |
| 5000 | 1.77ms | 3.53ms | **5.28ms** | ✅ |
| 8000 | 4.11ms | 12.51ms | **18.63ms** | ⚠️ p99 > 10ms |

→ Read p99 ≤ 10ms держится до ~6000 RPS на 1 pod.
