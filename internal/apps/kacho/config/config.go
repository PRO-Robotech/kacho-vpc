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
//
// KAC-106 (E1): renamed `resource-manager` peer to `iam`. The `ResourceManager`
// struct field is retained as alias backed by the same `IAM` peer for backward-
// compat during transition — both `extapi.iam` and `extapi.resource-manager`
// YAML keys are accepted.
type ExtAPIConfig struct {
	DefDialDuration time.Duration `mapstructure:"def-dial-duration"`
	IAM             PeerConfig    `mapstructure:"iam"`
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
	DefaultSGInline bool                     `mapstructure:"default-sg-inline"`
	ProjectCache    ProjectCacheConfigStruct `mapstructure:"project-cache"`
}

// ProjectCacheConfigStruct — TTL+LRU кеш ProjectClient.Exists (KAC-39).
type ProjectCacheConfigStruct struct {
	PositiveTTL time.Duration `mapstructure:"positive-ttl"`
	NegativeTTL time.Duration `mapstructure:"negative-ttl"`
	MaxSize     int           `mapstructure:"max-size"`
}

// schemaOptionsParam — URL-encoded libpq-параметр `options=-c search_path=…`.
// Добавляется в baseDSN автоматически (если ещё не задано), чтобы каждое
// соединение (pgxpool, dedicated pgx.Conn для LISTEN, goose-через-database/sql)
// видело таблицы kacho-vpc по unqualified-имени.
//
// Значение search_path — «kacho_vpc, public»:
//   - `kacho_vpc` впереди — наши таблицы (миграция 0034 переехала их из public);
//   - `public` сзади — `btree_gist`-extension и built-in объекты Postgres,
//     которые extension/CREATE-команды по умолчанию создают там.
//
// Пробел в `-c search_path=…` обязан быть `%20`; знак `=` внутри значения —
// `%3D`; запятая — `%2C`. При смене схемы (ребрендинг / multi-tenant) — менять
// здесь и в миграции 0034 одновременно.
const schemaOptionsParam = "options=-c%20search_path%3Dkacho_vpc%2Cpublic"

// baseDSN — стандартный postgres DSN без pgxpool-параметров; используется
// и pgxpool, и database/sql.Open("pgx"). Делегирует composeDSN(URL) — общему
// формирователю для master- и slave-DSN.
func (c Config) baseDSN() string {
	return c.composeDSN(c.Repository.Postgres.URL)
}

// composeDSN добавляет к raw-DSN (master URL или slave URL) недостающие libpq-
// параметры: `sslmode=<mode>` (из PostgresConfig.SSLMode, default `disable`)
// и `options=-c search_path=kacho_vpc,public` (KAC-94: миграция 0034 переехала
// все VPC-таблицы из схемы `public` в `kacho_vpc`, поэтому каждое соединение
// должно установить корректный search_path).
//
// Если соответствующий параметр уже задан в raw-URL — не перетираем (упрощает
// override через прямой ENV/yaml). Для пустого raw возвращаем пустую строку
// — caller интерпретирует это как «slave не настроен».
func (c Config) composeDSN(raw string) string {
	if raw == "" {
		return ""
	}
	mode := c.Repository.Postgres.SSLMode
	if mode == "" {
		mode = "disable"
	}
	if !dsnHas(raw, "sslmode=") {
		sep := "?"
		if dsnHas(raw, "?") {
			sep = "&"
		}
		raw = raw + sep + "sslmode=" + mode
	}
	// Append search_path via libpq `options` parameter, если ещё не задан.
	// Распознаём как `options=`, так и URL-encoded `options%3D` (на всякий
	// случай). Если пользователь сам прописал `options=...` в URL — оставляем
	// его, не перетираем (упрощает override в dev/debug).
	if !dsnHas(raw, "options=") && !dsnHas(raw, "options%3D") {
		sep := "?"
		if dsnHas(raw, "?") {
			sep = "&"
		}
		raw = raw + sep + schemaOptionsParam
	}
	return raw
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
