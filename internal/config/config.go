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
}

// DSN возвращает PostgreSQL DSN строку с настраиваемым sslmode.
func (c Config) DSN() string {
	mode := c.DBSSLMode
	if mode == "" {
		mode = "disable"
	}
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, mode,
	)
	if c.DBMaxConns > 0 {
		dsn += fmt.Sprintf("&pool_max_conns=%d", c.DBMaxConns)
	}
	return dsn
}

// Load загружает конфигурацию из переменных окружения.
func Load() (Config, error) {
	var c Config
	err := corecfg.Load(&c)
	return c, err
}
