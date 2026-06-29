# k6 — нагрузочные тесты kacho-vpc

Pre-req:
- k6 v0.55+ (`/usr/local/bin/k6` или `~/.local/bin/k6`)
- kind-кластер с kacho-vpc запущен, port-forward `:18080`
- pre-seeded fixtures: Account/Project, Region `zone`, zones a/b/c/d,
  default AddressPool с CIDR /16+ на zone-a

## Запуск

```bash
# Single scenario
k6 run scripts/network-create-burst.js

# Все scenarios sequentially (CI-like)
./run-all.sh

# Specific environment
k6 run --env BASE_URL=http://localhost:18080 --env PROJECT_ID=b1gXXX scripts/network-create-burst.js

# Save results
k6 run --out json=results/network-create-burst.json scripts/network-create-burst.js
```

## Сценарии

| Файл | Назначение | Длительность | VU profile |
|---|---|---|---|
| `network-create-burst.js` | Burst Network Create | 5 min | ramp 0→50 |
| `subnet-create-burst.js` | Burst Subnet Create | 5 min | ramp 0→30 |
| `allocate-external-burst.js` | AllocateExternalIP capacity | 5 min | ramp 0→100 |
| `list-heavy.js` | List под нагрузкой read | 3 min | constant 50 |
| `mixed-read-write.js` | Production-like 60/30/10 | 10 min | constant 50 |
| `lro-completion.js` | Latency Create→done=true | 5 min | constant 20 |
| `breakpoint.js` | Linear ramp до crash | до failure | 0→1000 |
| `soak-24h.js` | Stability 24h | 24h | constant 30 |

## SLO targets (local KIND)

| Scenario | RPS sustained | p99 | Error rate |
|---|---|---|---|
| Network Create | ≥ 30 | < 1500ms | < 1% |
| Subnet Create | ≥ 20 | < 1000ms | < 1% |
| Address (ext) | ≥ 50 | < 600ms | < 0.5% |
| Get/List | ≥ 200 | < 100ms | < 0.1% |

## Files

```
tests/k6/
├── scripts/
│   ├── lib/
│   │   ├── client.js     — common HTTP + auth headers
│   │   ├── fixtures.js   — read env, helpers для names
│   │   ├── poll-op.js    — LRO polling
│   │   └── slo.js        — thresholds
│   ├── network-create-burst.js
│   ├── subnet-create-burst.js
│   ├── allocate-external-burst.js
│   ├── list-heavy.js
│   ├── mixed-read-write.js
│   ├── lro-completion.js
│   ├── breakpoint.js
│   └── soak-24h.js
├── environments/
│   └── local.json        — BASE_URL, PROJECT_ID, ZONE_ID
├── results/              — gitignored
├── run-all.sh            — run all in CI mode
└── README.md
```
