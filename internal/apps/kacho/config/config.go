package config

import (
	"fmt"
	"time"
)

// Config — корневая структура конфигурации kacho-vpc (skill evgeniy §8 J.1).
//
// Иерархия (YAML):
//
//	logger:        { level }
//	api-server:    { endpoint, internal-endpoint, graceful-shutdown }
//	metrics:       { enable }
//	healthcheck:   { enable }
//	repository:    { type, postgres }
//	authn:         { mode, tls }
//	extapi:        { def-dial-duration, resource-manager, compute }
//	watch:         { max-streams }
//	network:       { default-sg-inline, folder-cache }
//
// Все секции — `mapstructure`-теги (viper по умолчанию использует mapstructure
// для Unmarshal). Default'ы — в defaults.go (правило J.3).
type Config struct {
	Logger      LoggerConfig      `mapstructure:"logger"`
	APIServer   APIServerConfig   `mapstructure:"api-server"`
	Metrics     MetricsConfig     `mapstructure:"metrics"`
	Healthcheck HealthcheckConfig `mapstructure:"healthcheck"`
	Repository  RepositoryConfig  `mapstructure:"repository"`
	AuthN       AuthNConfig       `mapstructure:"authn"`
	ExtAPI      ExtAPIConfig      `mapstructure:"extapi"`
	Watch       WatchConfig       `mapstructure:"watch"`
	Network     NetworkConfig     `mapstructure:"network"`
}

// LoggerConfig — секция logger.
type LoggerConfig struct {
	// Level — один из FATAL|ERROR|WARN|INFO|DEBUG.
	Level string `mapstructure:"level"`
}

// APIServerConfig — секция api-server.
//
// Endpoint / InternalEndpoint поддерживают два формата:
//   - `tcp://0.0.0.0:9090` (полный URL-стиль, рекомендуется);
//   - `9090` (legacy: голый порт; работает для backward-compat
//     с старыми values.yaml, см. listenAddress в load.go).
type APIServerConfig struct {
	Endpoint         string        `mapstructure:"endpoint"`
	InternalEndpoint string        `mapstructure:"internal-endpoint"`
	GracefulShutdown time.Duration `mapstructure:"graceful-shutdown"`
}

// MetricsConfig — секция metrics (placeholder под будущий /metrics endpoint).
type MetricsConfig struct {
	Enable bool `mapstructure:"enable"`
}

// HealthcheckConfig — секция healthcheck (placeholder под /healthz).
type HealthcheckConfig struct {
	Enable bool `mapstructure:"enable"`
}

// RepositoryConfig — секция repository. Сейчас single-backend (Postgres);
// `Type` зарезервирован под мульти-БД, как в skill evgeniy §9 K.3 (migrator
// должен уметь postgres|cockroach|…).
type RepositoryConfig struct {
	Type     string         `mapstructure:"type"`
	Postgres PostgresConfig `mapstructure:"postgres"`
}

// PostgresConfig — секция repository.postgres.
//
//	URL              — стандартный DSN postgres://user:pass@host:port/db (master).
//	SlaveURL         — DSN read-replica (опционально, skill evgeniy §6 G.4).
//	                   Пустая строка / совпадает с URL → Reader-TX идут на master
//	                   (fallback). Когда настроен — Reader использует slave-pool,
//	                   разгружая master от read-load (streaming replication,
//	                   `hot_standby=on` на реплике). Пароль читается из того же
//	                   `password-from-env` и подставляется в обе DSN.
//	MaxConns         — pgxpool max conns (одинаково для master и slave-pool);
//	                   0 = pgx default (max(4, NumCPU)).
//	SSLMode          — disable|require|verify-ca|verify-full (валидируется в Validate).
//	PasswordFromEnv  — имя ENV-переменной, из которой подтягивается пароль и
//	                   подставляется в URL и SlaveURL (legacy KACHO_VPC_DB_PASSWORD).
//	                   Пустая строка — пароль уже в URL (или sslmode=disable+no-password).
//
// Пароль в YAML/ConfigMap — нельзя (commit-able), поэтому он остаётся
// read-from-env через явный `password-from-env` мостик. Default —
// `KACHO_VPC_DB_PASSWORD` (backward-compat).
type PostgresConfig struct {
	URL             string `mapstructure:"url"`
	SlaveURL        string `mapstructure:"slave-url"`
	MaxConns        int    `mapstructure:"max-conns"`
	SSLMode         string `mapstructure:"ssl-mode"`
	PasswordFromEnv string `mapstructure:"password-from-env"`
}

// AuthNConfig — секция authn.
//
// Mode — общий режим работы сервиса (см. mode.go). Под-секция TLS зарезервирована
// под будущий serving-TLS (key-file/cert-file на listener) — пока сервис
// слушает plain gRPC, поле наполняется через viper, но в runtime не используется.
type AuthNConfig struct {
	Mode Mode      `mapstructure:"mode"`
	TLS  TLSServer `mapstructure:"tls"`
}

// TLSServer — TLS-параметры server-side listener'а (зарезервировано).
type TLSServer struct {
	KeyFile    string   `mapstructure:"key-file"`
	CertFile   string   `mapstructure:"cert-file"`
	ServerName string   `mapstructure:"server-name"`
	CAFiles    []string `mapstructure:"ca-files"`
}

// ExtAPIConfig — секция extapi (peer-сервисы).
type ExtAPIConfig struct {
	DefDialDuration time.Duration `mapstructure:"def-dial-duration"`
	ResourceManager PeerConfig    `mapstructure:"resource-manager"`
	Compute         PeerConfig    `mapstructure:"compute"`
}

// PeerConfig — параметры одного peer-сервиса.
//
//	Endpoint      — host:port (без `dns:///` — префикс добавляется в dialer'е,
//	                если DNSLB=true).
//	TLS           — TLS-параметры клиента к peer'у.
//	DialDuration  — таймаут на установление conn (0 — extapi.def-dial-duration).
//	DNSLB         — включить gRPC client-side round_robin + dns:/// resolver (KAC-39).
type PeerConfig struct {
	Endpoint     string        `mapstructure:"endpoint"`
	TLS          TLSClient     `mapstructure:"tls"`
	DialDuration time.Duration `mapstructure:"dial-duration"`
	DNSLB        bool          `mapstructure:"dns-lb"`
}

// TLSClient — TLS-параметры client-side (для peer-gRPC).
type TLSClient struct {
	Enable     bool     `mapstructure:"enable"`
	ServerName string   `mapstructure:"server-name"`
	CAFiles    []string `mapstructure:"ca-files"`
}

// WatchConfig — секция watch (InternalWatchService — outbox stream).
type WatchConfig struct {
	MaxStreams int `mapstructure:"max-streams"`
}

// NetworkConfig — секция network (VPC-domain бизнес-настройки).
type NetworkConfig struct {
	// DefaultSGInline — создавать ли default SecurityGroup inline при Network.Create.
	DefaultSGInline bool                    `mapstructure:"default-sg-inline"`
	FolderCache     FolderCacheConfigStruct `mapstructure:"folder-cache"`
}

// FolderCacheConfigStruct — TTL+LRU кеш FolderClient.Exists (KAC-39).
type FolderCacheConfigStruct struct {
	PositiveTTL time.Duration `mapstructure:"positive-ttl"`
	NegativeTTL time.Duration `mapstructure:"negative-ttl"`
	MaxSize     int           `mapstructure:"max-size"`
}

// baseDSN — стандартный postgres DSN без pgxpool-параметров; используется
// и pgxpool, и database/sql.Open("pgx"). Принимает явный raw-DSN (URL или
// SlaveURL), а sslmode подтягивает из общей PostgresConfig.SSLMode.
func (c Config) baseDSN() string {
	return c.composeDSN(c.Repository.Postgres.URL)
}

func (c Config) composeDSN(raw string) string {
	if raw == "" {
		return ""
	}
	mode := c.Repository.Postgres.SSLMode
	if mode == "" {
		mode = "disable"
	}
	if dsnHas(raw, "sslmode=") {
		return raw
	}
	sep := "?"
	if dsnHas(raw, "?") {
		sep = "&"
	}
	return raw + sep + "sslmode=" + mode
}

// DSN — connection string для pgxpool (поддерживает pool_max_conns).
// НЕ использовать для database/sql.Open("pgx") (FATAL — см. FINDING-007).
func (c Config) DSN() string {
	dsn := c.baseDSN()
	if dsn == "" {
		return ""
	}
	if c.Repository.Postgres.MaxConns > 0 {
		dsn += fmt.Sprintf("&pool_max_conns=%d", c.Repository.Postgres.MaxConns)
	}
	return dsn
}

// SlaveDSN — connection string для slave-pool (read-replica, skill evgeniy
// §6 G.4). Пустая строка → реплика не настроена, caller использует master
// (Repository.New(master, nil) → Reader fallback на master).
//
// SlaveURL совпадает с URL — slave-pool тоже не создаётся (caller передаст
// nil), чтобы не плодить второй pool к той же физической БД.
func (c Config) SlaveDSN() string {
	slaveRaw := c.Repository.Postgres.SlaveURL
	if slaveRaw == "" || slaveRaw == c.Repository.Postgres.URL {
		return ""
	}
	dsn := c.composeDSN(slaveRaw)
	if dsn == "" {
		return ""
	}
	if c.Repository.Postgres.MaxConns > 0 {
		dsn += fmt.Sprintf("&pool_max_conns=%d", c.Repository.Postgres.MaxConns)
	}
	return dsn
}

// MigrateDSN — connection string для goose/database/sql (без pool_max_conns).
// Всегда master — goose не должен писать в реплику.
func (c Config) MigrateDSN() string { return c.baseDSN() }

func dsnHas(dsn, frag string) bool {
	for i := 0; i+len(frag) <= len(dsn); i++ {
		if dsn[i:i+len(frag)] == frag {
			return true
		}
	}
	return false
}
