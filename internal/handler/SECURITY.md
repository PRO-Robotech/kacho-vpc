# Security — handler layer

Снимок состояния AuthZ / AuthN / transport-security на уровне handler'ов: что
**уже сделано** и что осталось.

## Сообщить об уязвимости

Не открывайте публичный issue на security-проблему. Сообщайте приватно через
GitHub Security Advisories репозитория (`Security` → `Report a vulnerability`).
Опишите затронутую версию, шаги воспроизведения и предполагаемое влияние; мы
подтвердим получение и согласуем сроки раскрытия после выпуска фикса.

## Инварианты authN / authZ

Правила одинаковы для public (external TLS) и internal (:9091) листенеров —
неаутентифицированных и неавторизованных запросов нет ни на одном порту:

- **Транспорт / AuthN** — service→service через mTLS (verified client-cert),
  user→edge через TLS + validated JWT. Plaintext/insecure-gRPC в проде запрещен.
- **AuthZ** — каждый RPC проходит per-RPC authz-Check; read-RPC гейтятся
  viewer-tier, мутации — admin-tier. Internal-периметр не считается доверенным
  (defense-in-depth против lateral movement).
- **Internal-vs-external** — `Internal*` методы не публикуются на external TLS
  endpoint, только на cluster-internal listener.
- **Без leak'а инфра-данных** — публичная поверхность отдает лишь tenant-facing
  «намерение + результат»; placement/underlay/wiring — только через `Internal*`.

## Что сделано

### Tenant isolation (project ownership) на public-handler'ах

Каждый public RPC, читающий/мутирующий конкретный ресурс, проверяет, что
`resource.project_id` принадлежит caller'у. Tenant-context извлекается
interceptor'ом (`internal/handler/tenant_interceptor.go`), проверка —
`AssertProjectOwnership` в handler'ах (address/network/subnet/route_table/
security_group/gateway). Cross-tenant `Get` и `Get`
несуществующего ресурса дают одинаковый `404` (info-leak prevention; см.
`Address.GetByValue`).

`KACHO_VPC_AUTH_MODE` (`internal/config/config.go`):
- `dev` — anonymous-mode, callers без AuthN-headers пропускаются как admin
  (только для локальных фикстур; в развернутом стенде/проде недопустимо).
- `production` — **fail-closed**: запрос без не-пустого TenantCtx → `PermissionDenied`
  (защита от misconfigured prod-deploy, где IAM-sidecar/reverse-proxy забыт).
- `production-strict` — то же + дополнительно требует `ResourceManagerTLS=true` && `DBSSLMode != disable`.

### Internal-port (:9091) — оборона

`:9091` (`Internal*` RPC) защищен несколькими слоями:
1. **NetworkPolicy** (helm) — ingress на `:9091` только от api-gateway и admin-tooling pod'ов.
2. **admin-only interceptor** — `Internal*` методы требуют admin-claim.
3. **production-mode fail-closed** — без валидного context'а отказ.

`Internal*` методы **не регистрируются** на external TLS endpoint
(`api.kacho.local:443`, advertised для внешних клиентов) — только на
cluster-internal listener api-gateway.

### Без raw-pgx-leak в Internal handlers

Все `Internal*` handler'ы маппят ошибки через `internalMapErr`
(`internal/handler/internal_maperr.go`; обертки `mapPoolErr`/`mapGeoErr`/`mapAllocErr`) —
sentinel'ы классифицируются, raw `pgErr` → generic `Internal` без
hostname/db/query-fragment в тексте. Прямых `status.Errorf(codes.Internal, "...: %v", err)`
в `internal_*_handler.go` не осталось.

### Transport-security — env-gated

- `KACHO_VPC_DB_SSLMODE` (default `disable` для dev; в production helm-values — `verify-full`) — `internal/config/config.go`.
- `KACHO_VPC_RESOURCE_MANAGER_TLS` (default `false`; true в production) — TLS-credentials
  для gRPC-клиента к resource-manager (`cmd/vpc/main.go::dialResourceManager`).
- `production-strict`-mode проверяет, что оба включены (иначе старт падает).

## Что осталось (зависит от интеграции с `kacho-iam`)

- **Реальный AuthN (JWT-validating interceptor)** — сейчас claims приходят от
  upstream-proxy без валидации токена и без реальной проверки членства в
  project/cloud через resource-manager. Контракт `TenantFromCtx` /
  `AssertProjectOwnership` спроектирован так, чтобы interceptor можно было
  заменить без правок handler'ов.
- **`OperationService.Get(operation_id)` без project-ownership-check** —
  единственный public RPC без проверки. Требует `project_id` на таблице `operations`
  (она в `kacho-corelib`, shared) либо резолва через `metadata.resource_id` →
  ресурс → project.
- **mTLS на `:9091`** — слой поверх NetworkPolicy + admin-check + prod-mode;
  требует cert-management (issuer/ротация/mount).
- **`InternalWatchService.Watch` без project-filter** — раздает весь outbox
  (admin-tooling это и нужно, но при ослаблении изоляции `:9091` — exfil-вектор);
  опциональный required `project_filter` либо отдельный admin-only RPC.
</content>
</invoke>
