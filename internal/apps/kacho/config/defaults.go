package config

import (
	"time"

	"github.com/spf13/viper"
)

// RegisterDefaults устанавливает default-значения всех конфиг-ключей
// (skill evgeniy §8 J.3 — defaults в одном месте, не в struct-tags).
//
// Значения совпадают с прежними envconfig-defaults из internal/config/config.go,
// чтобы переход на viper не менял рантайм-поведение dev-стенда:
//
//	prev → new key
//	-------------------------------------------------------------------------
//	KACHO_VPC_DB_HOST=localhost                       → repository.postgres.url (compose)
//	KACHO_VPC_DB_PORT=5432
//	KACHO_VPC_DB_USER=vpc
//	KACHO_VPC_DB_NAME=kacho_vpc
//	KACHO_VPC_DB_SSLMODE=disable                      → repository.postgres.ssl-mode
//	KACHO_VPC_DB_MAX_CONNS=0                          → repository.postgres.max-conns
//	KACHO_VPC_GRPC_PORT=9090                          → api-server.endpoint=tcp://0.0.0.0:9090
//	KACHO_VPC_INTERNAL_PORT=9091                      → api-server.internal-endpoint=tcp://0.0.0.0:9091
//	KACHO_VPC_WATCH_MAX_STREAMS=32                    → watch.max-streams
//	KACHO_VPC_RESOURCE_MANAGER_GRPC_ADDR=...          → extapi.resource-manager.endpoint
//	KACHO_VPC_RESOURCE_MANAGER_TLS=false              → extapi.resource-manager.tls.enable
//	KACHO_VPC_RESOURCE_MANAGER_DNS_LB=false           → extapi.resource-manager.dns-lb
//	KACHO_VPC_COMPUTE_GRPC_ADDR=...                   → extapi.compute.endpoint
//	KACHO_VPC_COMPUTE_TLS=false                       → extapi.compute.tls.enable
//	KACHO_VPC_DEFAULT_SG_INLINE=true                  → network.default-sg-inline
//	KACHO_VPC_FOLDER_CACHE_TTL=30s                    → network.folder-cache.positive-ttl
//	KACHO_VPC_FOLDER_CACHE_NEGATIVE_TTL=5s            → network.folder-cache.negative-ttl
//	KACHO_VPC_FOLDER_CACHE_SIZE=10000                 → network.folder-cache.max-size
//	KACHO_VPC_AUTH_MODE=dev                           → authn.mode
//
// DB-пароль остаётся read-from-env (см. PostgresConfig.PasswordFromEnv).
func RegisterDefaults(v *viper.Viper) {
	// logger
	v.SetDefault("logger.level", "INFO")

	// api-server
	v.SetDefault("api-server.endpoint", "tcp://0.0.0.0:9090")
	v.SetDefault("api-server.internal-endpoint", "tcp://0.0.0.0:9091")
	v.SetDefault("api-server.graceful-shutdown", 10*time.Second)

	// metrics / healthcheck (placeholders)
	v.SetDefault("metrics.enable", true)
	v.SetDefault("healthcheck.enable", true)

	// repository
	v.SetDefault("repository.type", "POSTGRES")
	// URL по умолчанию покрывает локальный goose / `make test` без values.yaml.
	// Пароль подставляется из ENV (см. password-from-env ниже).
	v.SetDefault("repository.postgres.url", "postgres://vpc@localhost:5432/kacho_vpc")
	// slave-url — опц. DSN read-replica (skill evgeniy §6 G.4). Пустая строка →
	// Reader-TX идут на master (fallback). Когда деплой добавит реплику —
	// выставляется через values.yaml / ENV KACHO_VPC_REPOSITORY__POSTGRES__SLAVE_URL.
	v.SetDefault("repository.postgres.slave-url", "")
	v.SetDefault("repository.postgres.max-conns", 0)
	v.SetDefault("repository.postgres.ssl-mode", "disable")
	v.SetDefault("repository.postgres.password-from-env", "KACHO_VPC_DB_PASSWORD")

	// authn
	v.SetDefault("authn.mode", "dev")

	// extapi
	v.SetDefault("extapi.def-dial-duration", 10*time.Second)
	v.SetDefault("extapi.resource-manager.endpoint", "resource-manager.kacho.svc.cluster.local:9090")
	v.SetDefault("extapi.resource-manager.tls.enable", false)
	v.SetDefault("extapi.resource-manager.dns-lb", false)
	v.SetDefault("extapi.compute.endpoint", "compute.kacho.svc.cluster.local:9090")
	v.SetDefault("extapi.compute.tls.enable", false)

	// authz (E3 / KAC-108). По умолчанию iam-endpoint пустой → interceptor
	// не навешивается; включается через values.yaml / ENV. В dev-стенде —
	// values-dev.yaml выставит iam-endpoint=kacho-iam.kacho.svc.cluster.local:9091.
	v.SetDefault("authz.iam-endpoint", "")
	v.SetDefault("authz.iam-tls.enable", false)
	v.SetDefault("authz.breakglass", false)
	v.SetDefault("authz.check-timeout", 2*time.Second)
	v.SetDefault("authz.deny-rate-limit-per-sec", 100.0)
	v.SetDefault("authz.cache-ttl", 5*time.Second)

	// watch
	v.SetDefault("watch.max-streams", 32)

	// network (VPC-domain)
	v.SetDefault("network.default-sg-inline", true)
	v.SetDefault("network.folder-cache.positive-ttl", 30*time.Second)
	v.SetDefault("network.folder-cache.negative-ttl", 5*time.Second)
	v.SetDefault("network.folder-cache.max-size", 10000)
}
