package config

import (
	"fmt"

	corecfg "github.com/PRO-Robotech/kacho-corelib/config"
)

// Config — конфигурация kacho-vpc.
type Config struct {
	DBHost     string `envconfig:"KACHO_VPC_DB_HOST" default:"localhost"`
	DBPort     string `envconfig:"KACHO_VPC_DB_PORT" default:"5432"`
	DBUser     string `envconfig:"KACHO_VPC_DB_USER" default:"vpc"`
	DBPassword string `envconfig:"KACHO_VPC_DB_PASSWORD" required:"true"`
	DBName     string `envconfig:"KACHO_VPC_DB_NAME" default:"kacho_vpc"`
	// DBSSLMode — sslmode для DSN. По умолчанию `disable` для dev-стенда;
	// production обязан выставить `verify-full` (security P0 closure).
	DBSSLMode string `envconfig:"KACHO_VPC_DB_SSLMODE" default:"disable"`
	// DBMaxConns — лимит pgx pool. По умолчанию 0 = pgx default
	// (max(4, NumCPU)), что слишком мало для service с inline-allocate +
	// outbox-write + Watch streams. Production: ≥20 на pod.
	DBMaxConns int `envconfig:"KACHO_VPC_DB_MAX_CONNS" default:"0"`

	GrpcPort string `envconfig:"KACHO_VPC_GRPC_PORT" default:"9090"`

	// InternalGrpcPort — порт для cluster-internal RPC (InternalWatchService).
	// НЕ выставляется через api-gateway. Используется kacho-vpc-controllers
	// для подписки на стрим событий из vpc_outbox.
	InternalGrpcPort string `envconfig:"KACHO_VPC_INTERNAL_PORT" default:"9091"`

	// WatchMaxStreams — максимум одновременных Watch streams. Каждый держит
	// dedicated pgx.Conn под LISTEN — при отсутствии лимита buggy/looping
	// клиент исчерпает Postgres max_connections (concurrency P0 #5).
	WatchMaxStreams int `envconfig:"KACHO_VPC_WATCH_MAX_STREAMS" default:"32"`

	ResourceManagerGRPCAddr string `envconfig:"KACHO_VPC_RESOURCE_MANAGER_GRPC_ADDR" default:"resource-manager.kacho.svc.cluster.local:9090"`
	// ResourceManagerTLS — использовать TLS для cross-service gRPC к
	// resource-manager (security P0 closure: иначе in-cluster MITM может
	// подделать FolderClient.GetCloudID/Exists).
	ResourceManagerTLS bool `envconfig:"KACHO_VPC_RESOURCE_MANAGER_TLS" default:"false"`

	// ComputeGRPCAddr — публичный gRPC kacho-compute. kacho-compute — owner
	// Geography (Region/Zone), эпик KAC-15: VPC валидирует zone_id вызовом
	// compute.v1.ZoneService.Get (см. workspace CLAUDE.md §«Кросс-доменные ссылки»).
	ComputeGRPCAddr string `envconfig:"KACHO_VPC_COMPUTE_GRPC_ADDR" default:"compute.kacho.svc.cluster.local:9090"`
	// ComputeTLS — использовать TLS для cross-service gRPC к kacho-compute.
	ComputeTLS bool `envconfig:"KACHO_VPC_COMPUTE_TLS" default:"false"`

	// DefaultSGInline — создавать ли default SecurityGroup inline при Network.Create.
	//   true (default) — Network.doCreate синхронно создаёт default SG в той же
	//                    операции (workaround после упразднения kacho-vpc-controllers).
	//   false          — Network.Create НЕ создаёт SG (verbatim YC: SG создаётся
	//                    отдельным reconciler'ом). Убирает 2 INSERT + 1 UPDATE из
	//                    hot-path → +30-40% write-throughput. Для load-тестов и
	//                    deployment'ов с внешним SG-reconciler'ом.
	DefaultSGInline bool `envconfig:"KACHO_VPC_DEFAULT_SG_INLINE" default:"true"`

	// AuthMode — управление fail-closed гейтом перед IAM merge.
	//   `dev` (default) — anonymous-mode разрешён, interceptor пропускает
	//                     callers без AuthN-headers как admin (backward-compat).
	//   `production`    — fail-closed: каждый запрос обязан иметь не-пустой
	//                     TenantCtx (Actor + (Admin или FolderIDs)). Anonymous
	//                     → PermissionDenied. Защита от misconfigured prod-deploy
	//                     где IAM sidecar/reverse-proxy auth забыт — иначе
	//                     anonymous = root по всему сервису (security M5).
	//   `production-strict` — то же + дополнительно требует
	//                         ResourceManagerTLS=true && DBSSLMode!=disable.
	AuthMode string `envconfig:"KACHO_VPC_AUTH_MODE" default:"dev"`
}

// baseDSN возвращает стандартный postgres DSN без pgxpool-специфичных
// параметров — пригоден и для pgxpool, и для database/sql.Open("pgx").
func (c Config) baseDSN() string {
	mode := c.DBSSLMode
	if mode == "" {
		mode = "disable"
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, mode,
	)
}

// DSN — connection string для pgxpool (поддерживает pool_max_conns).
// НЕ использовать для database/sql.Open("pgx") — там pool_max_conns
// передаётся серверу как unknown PG-параметр → FATAL (см. FINDING-007).
func (c Config) DSN() string {
	dsn := c.baseDSN()
	if c.DBMaxConns > 0 {
		dsn += fmt.Sprintf("&pool_max_conns=%d", c.DBMaxConns)
	}
	return dsn
}

// MigrateDSN — connection string для goose/database/sql (без pgxpool-параметров).
func (c Config) MigrateDSN() string {
	return c.baseDSN()
}

// Load загружает конфигурацию из переменных окружения.
func Load() (Config, error) {
	var c Config
	err := corecfg.Load(&c)
	return c, err
}
