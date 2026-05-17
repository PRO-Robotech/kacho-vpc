# Security — handler layer (current state)

> Снимок состояния AuthZ / AuthN / transport-security на уровне handler'ов.
> Здесь — что **уже сделано** (чтобы не «исправлять» исправленное) и что
> осталось. Открытые задачи — GitHub Issues `PRO-Robotech/kacho-vpc` с меткой
> `blocked:kacho-iam` (там же причины отложенности).

## Что сделано

### Tenant isolation (folder ownership) на public-handler'ах

Каждый public RPC, читающий/мутирующий конкретный ресурс, проверяет, что
`resource.project_id` принадлежит caller'у. Tenant-context извлекается
interceptor'ом (`internal/handler/tenant_interceptor.go`), проверка —
`AssertFolderOwnership` в handler'ах (address/network/subnet/route_table/
security_group/gateway/private_endpoint). Cross-tenant `Get` и `Get`
несуществующего ресурса дают одинаковый `404` (info-leak prevention; см.
`Address.GetByValue`).

`KACHO_VPC_AUTH_MODE` (`internal/config/config.go`):
- `dev` (default) — anonymous-mode, callers без AuthN-headers пропускаются как admin (backward-compat dev-стенд).
- `production` — **fail-closed**: запрос без не-пустого TenantCtx → `PermissionDenied`
  (защита от misconfigured prod-deploy, где IAM-sidecar/reverse-proxy забыт).
- `production-strict` — то же + дополнительно требует `ResourceManagerTLS=true` && `DBSSLMode != disable`.

### Internal-port (:9091) — оборона

`:9091` (`Internal*` RPC) защищён тремя слоями:
1. **NetworkPolicy** (`kacho-deploy` helm) — ingress на `:9091` только от api-gateway и admin-tooling pod'ов.
2. **admin-only interceptor** — `Internal*` методы требуют admin-claim.
3. **production-mode fail-closed** — без валидного context'а отказ.

`Internal*` методы **не регистрируются** на external TLS endpoint
(`api.kacho.local:443`, advertised для `yc` CLI) — только на cluster-internal
listener api-gateway. См. workspace `CLAUDE.md` §запрет 6.

### Без raw-pgx-leak в Internal handlers

Все `Internal*` handler'ы маппят ошибки через `internalMapErr`
(`internal/handler/internal_maperr.go`; обёртки `mapPoolErr`/`mapGeoErr`/`mapAllocErr`) —
sentinel'ы классифицируются, raw `pgErr` → generic `Internal` без
hostname/db/query-fragment в тексте. Прямых `status.Errorf(codes.Internal, "...: %v", err)`
в `internal_*_handler.go` не осталось.

### Transport-security — env-gated

- `KACHO_VPC_DB_SSLMODE` (default `disable` для dev; в production helm-values — `verify-full`) — `internal/config/config.go`.
- `KACHO_VPC_RESOURCE_MANAGER_TLS` (default `false`; true в production) — TLS-credentials
  для gRPC-клиента к resource-manager (`cmd/vpc/main.go::dialResourceManager`).
- `production-strict`-mode проверяет, что оба включены (иначе старт падает).

## Что осталось (всё — `kacho-iam` design phase; GitHub Issues, метка `blocked:kacho-iam`)

- **Реальный AuthN (JWT-validating interceptor)** — сейчас claims приходят от
  upstream-proxy без валидации токена и без реальной проверки членства в
  folder/cloud через resource-manager. Контракт `TenantFromCtx` /
  `AssertFolderOwnership` спроектирован так, чтобы interceptor можно было
  заменить без правок handler'ов. (`kacho-iam` ещё не реализован.)
- **`OperationService.Get(operation_id)` без folder-ownership-check** —
  единственный public RPC без проверки. Требует `project_id` на таблице `operations`
  (она в `kacho-corelib`, shared) либо резолва через `metadata.resource_id` →
  ресурс → folder; делается в IAM-фазе.
- **mTLS на `:9091`** — 4-й слой поверх NetworkPolicy + admin-check + prod-mode;
  требует cert-management (issuer/ротация/mount) — `kacho-deploy`-scope + IAM/mesh-дизайн.
- **`InternalWatchService.Watch` без folder-filter** — раздаёт весь outbox
  (admin-tooling это и нужно, но при ослаблении изоляции `:9091` — exfil-вектор);
  опциональный required `folder_filter` либо отдельный admin-only RPC — в IAM-фазе.
