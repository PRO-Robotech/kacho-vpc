// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"time"

	"github.com/spf13/viper"
)

// RegisterDefaults устанавливает default-значения всех конфиг-ключей
// (defaults в одном месте, не в struct-tags).
//
// Карта «legacy ENV → новый ключ» (значения покрывают dev-стенд без values.yaml):
//
//	legacy env → new key
//	-------------------------------------------------------------------------
//	KACHO_VPC_DB_HOST=localhost                       → repository.postgres.url (compose)
//	KACHO_VPC_DB_PORT=5432
//	KACHO_VPC_DB_USER=vpc
//	KACHO_VPC_DB_NAME=kacho_vpc
//	KACHO_VPC_DB_SSLMODE=disable                      → repository.postgres.ssl-mode
//	KACHO_VPC_DB_MAX_CONNS=0                          → repository.postgres.max-conns
//	KACHO_VPC_GRPC_PORT=9090                          → api-server.endpoint=tcp://0.0.0.0:9090
//	KACHO_VPC_INTERNAL_PORT=9091                      → api-server.internal-endpoint=tcp://0.0.0.0:9091
//	KACHO_VPC_IAM_GRPC_ADDR=...                       → extapi.iam.endpoint
//	KACHO_VPC_IAM_TLS=false                           → extapi.iam.tls.enable
//	KACHO_VPC_IAM_DNS_LB=false                        → extapi.iam.dns-lb
//	KACHO_VPC_GEO_GRPC_ADDR=...                       → extapi.geo.endpoint
//	KACHO_VPC_GEO_TLS=false                           → extapi.geo.tls.enable
//	KACHO_VPC_DEFAULT_SG_INLINE=true                  → network.default-sg-inline
//	KACHO_VPC_PROJECT_CACHE_TTL=30s                    → network.project-cache.positive-ttl
//	KACHO_VPC_PROJECT_CACHE_NEGATIVE_TTL=5s            → network.project-cache.negative-ttl
//	KACHO_VPC_PROJECT_CACHE_SIZE=10000                 → network.project-cache.max-size
//	KACHO_VPC_AUTH_MODE=dev                           → authn.mode
//
// DB-пароль остается read-from-env (см. PostgresConfig.PasswordFromEnv).
func RegisterDefaults(v *viper.Viper) {
	// logger
	v.SetDefault("logger.level", "INFO")

	// api-server
	v.SetDefault("api-server.endpoint", "tcp://0.0.0.0:9090")
	v.SetDefault("api-server.internal-endpoint", "tcp://0.0.0.0:9091")
	v.SetDefault("api-server.graceful-shutdown", 10*time.Second)
	// request-timeout — server-side deadline на один RPC (защита от bounded-pool
	// exhaustion / deadline-less запросов, CWE-770). 0 → без границы.
	v.SetDefault("api-server.request-timeout", 30*time.Second)

	// metrics / healthcheck — cluster-internal diagnostic listener (/metrics +
	// /healthz + /readyz). endpoint=:9095 зеркалит kacho-iam; enable=false ИЛИ
	// пустой endpoint → listener не поднимается.
	v.SetDefault("metrics.enable", true)
	v.SetDefault("metrics.endpoint", ":9095")
	v.SetDefault("healthcheck.enable", true)

	// repository
	v.SetDefault("repository.type", "POSTGRES")
	// URL по умолчанию покрывает локальный goose / `make test` без values.yaml.
	// Пароль подставляется из ENV (см. password-from-env ниже).
	v.SetDefault("repository.postgres.url", "postgres://vpc@localhost:5432/kacho_vpc")
	// slave-url — опц. DSN read-replica. Пустая строка → Reader-TX идут на master
	// (fallback). Когда деплой добавит реплику — выставляется через
	// values.yaml / ENV KACHO_VPC_REPOSITORY__POSTGRES__SLAVE_URL.
	v.SetDefault("repository.postgres.slave-url", "")
	v.SetDefault("repository.postgres.max-conns", 0)
	// serving-only DB-тайм-ауты (в MigrateDSN не попадают): ограничивают время
	// одного запроса / ожидания блокировки, чтобы зависший запрос не держал
	// pooled-connection бесконечно (CWE-770/400). 0 → Postgres default (без лимита).
	v.SetDefault("repository.postgres.statement-timeout", 30*time.Second)
	v.SetDefault("repository.postgres.lock-timeout", 15*time.Second)
	v.SetDefault("repository.postgres.ssl-mode", "disable")
	v.SetDefault("repository.postgres.password-from-env", "KACHO_VPC_DB_PASSWORD")

	// authn — secure-by-default: production (anonymous → fail-closed). Локальный
	// режим без AuthN (anonymous как admin) включается явно: authn.mode=dev
	// (values-dev.yaml / KACHO_VPC_AUTH_MODE=dev) — только для dev-стенда и тестов.
	v.SetDefault("authn.mode", "production")
	// trusted-forwarder — fail-closed default. В production (non-strict) публичный
	// :9090 listener требует ЛИБО server-mTLS, ЛИБО явного trusted-forwarder=true
	// (оператор подтверждает, что listener стоит за аутентифицированным
	// forwarder'ом/mesh). Без одного из двух production-старт отвергается
	// (ValidateServerMTLS) — client-asserted principal по plaintext недопустим.
	v.SetDefault("authn.trusted-forwarder", false)

	// extapi
	// project-existence peer — kacho-iam (ProjectService.Get).
	v.SetDefault("extapi.def-dial-duration", 10*time.Second)
	v.SetDefault("extapi.iam.endpoint", "iam.kacho.svc.cluster.local:9090")
	v.SetDefault("extapi.iam.tls.enable", false)
	v.SetDefault("extapi.iam.dns-lb", false)
	// zone_id валидируется через kacho-geo (leaf-домен Geography), а не
	// kacho-compute. Dial-host = geo k8s Service `kacho-geo` на public :9090
	// listener (ZoneService.Get/List); host совпадает с server-cert SAN
	// (kacho-geo.* / kacho-geo-internal.*).
	v.SetDefault("extapi.geo.endpoint", "kacho-geo.kacho.svc.cluster.local:9090")
	v.SetDefault("extapi.geo.tls.enable", false)

	// authz. По умолчанию iam-endpoint пустой → interceptor не навешивается;
	// включается через values.yaml / ENV. В dev-стенде — values-dev.yaml
	// выставит iam-endpoint=kacho-iam.kacho.svc.cluster.local:9091.
	v.SetDefault("authz.iam-endpoint", "")
	v.SetDefault("authz.iam-tls.enable", false)
	v.SetDefault("authz.breakglass", false)
	v.SetDefault("authz.check-timeout", 2*time.Second)
	v.SetDefault("authz.deny-rate-limit-per-sec", 100.0)
	v.SetDefault("authz.cache-ttl", 5*time.Second)

	// per-object list-filter (FGA ListObjects-filtered List).
	// Default enabled=true: List<Resource> возвращает только доступные объекты
	// (read==enforce, no-leak). endpoint/mTLS — через values.yaml (deploy);
	// пустой authorize-endpoint → fallback на iam-endpoint. Anonymous/no-subject
	// → fail-closed (use-case passthrough только для system-principal).
	v.SetDefault("authz.list-filter.enabled", true)
	v.SetDefault("authz.list-filter.authorize-endpoint", "")
	v.SetDefault("authz.list-filter.authorize-tls.enable", false)
	v.SetDefault("authz.list-filter.timeout-ms", 500)
	v.SetDefault("authz.list-filter.cache-ttl", 5*time.Second)
	v.SetDefault("authz.list-filter.max-entries", 10000)
	v.SetDefault("authz.list-filter.max-results", 10000)
	v.SetDefault("authz.list-filter.model-id", "")
	v.SetDefault("authz.list-filter.fail-open", false)

	// iam — интеграция с kacho-iam. require — fail-closed boot-gate (default off:
	// dev/Create разрешён, только Warn). register-drainer-enabled — default-on
	// (owner-tuple publisher). Ранее оба читались os.LookupEnv в cmd/; теперь —
	// типизированные ключи со строгой bool-валидацией на decode.
	v.SetDefault("iam.require", false)
	v.SetDefault("iam.register-drainer-enabled", true)

	// network (VPC-domain)
	v.SetDefault("network.default-sg-inline", true)
	v.SetDefault("network.project-cache.positive-ttl", 30*time.Second)
	v.SetDefault("network.project-cache.negative-ttl", 5*time.Second)
	v.SetDefault("network.project-cache.max-size", 10000)
}
