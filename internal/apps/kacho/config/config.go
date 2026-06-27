// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"fmt"
	"time"
)

// Config — корневая структура конфигурации kacho-vpc.
//
// Иерархия (YAML):
//
//	logger:        { level }
//	api-server:    { endpoint, internal-endpoint, graceful-shutdown }
//	metrics:       { enable }
//	healthcheck:   { enable }
//	repository:    { type, postgres }
//	authn:         { mode, tls }
//	authz:         { iam-endpoint, breakglass, ... }
//	extapi:        { def-dial-duration, iam, geo }
//	network:       { default-sg-inline, project-cache }
//
// Все секции — `mapstructure`-теги (viper по умолчанию использует mapstructure
// для Unmarshal). Default'ы — в defaults.go.
type Config struct {
	Logger      LoggerConfig      `mapstructure:"logger"`
	APIServer   APIServerConfig   `mapstructure:"api-server"`
	Metrics     MetricsConfig     `mapstructure:"metrics"`
	Healthcheck HealthcheckConfig `mapstructure:"healthcheck"`
	Repository  RepositoryConfig  `mapstructure:"repository"`
	AuthN       AuthNConfig       `mapstructure:"authn"`
	AuthZ       AuthZConfig       `mapstructure:"authz"`
	ExtAPI      ExtAPIConfig      `mapstructure:"extapi"`
	Network     NetworkConfig     `mapstructure:"network"`
}

// AuthZConfig — секция authz. Если IAMEndpoint пуст и Breakglass=false —
// interceptor НЕ навешивается (graceful start без kacho-iam в dev).
// См. internal/apps/kacho/check/factory.go.
type AuthZConfig struct {
	// IAMEndpoint — gRPC адрес kacho-iam internal-port'а (обычно
	// `kacho-iam.kacho.svc.cluster.local:9091`). Пустая строка → interceptor
	// не навешивается, если только Breakglass=true.
	IAMEndpoint string `mapstructure:"iam-endpoint"`

	// IAMTLS — TLS на peer-вызов в kacho-iam.
	IAMTLS TLSClient `mapstructure:"iam-tls"`

	// Breakglass — если true, interceptor пропускает все RPC без Check
	// (dev / emergency). Source: env `KACHO_VPC_AUTHZ__BREAKGLASS=true`.
	Breakglass bool `mapstructure:"breakglass"`

	// CheckTimeout — таймаут на один Check-вызов (default 2s).
	CheckTimeout time.Duration `mapstructure:"check-timeout"`

	// DenyRateLimitPerSec — token-bucket per-Principal на denied-storm
	// (default 100).
	DenyRateLimitPerSec float64 `mapstructure:"deny-rate-limit-per-sec"`

	// CacheTTL — TTL positive-results кеша (default 5s).
	CacheTTL time.Duration `mapstructure:"cache-ttl"`

	// ListFilter — конфиг FGA-filtered List handlers.
	ListFilter ListFilterConfig `mapstructure:"list-filter"`

	// TupleWrite — write-side FGA. Когда Enabled=true и OpenFGAEndpoint+StoreID
	// выставлены, каждый успешный resource Create публикует
	// `vpc_<resource>:<id>#project@project:<project_id>` tuple.
	TupleWrite TupleWriteConfig `mapstructure:"tuple-write"`
}

// TupleWriteConfig — конфигурация write-side FGA.
//
// Source: yaml `authz.tuple-write.{enabled,openfga-endpoint,store-id,model-id,timeout-ms}`.
// ENV-override: `KACHO_VPC_AUTHZ__TUPLE_WRITE__ENABLED=true`, etc.
//
// Без этого блока созданные VPC-ресурсы не получают per-resource hierarchy
// tuple → per-resource FGA Check `no path` → fail-closed deny.
type TupleWriteConfig struct {
	// Enabled — главный toggle. Default false (write-side выключен).
	// В production: true.
	Enabled bool `mapstructure:"enabled"`

	// OpenFGAEndpoint — host:port OpenFGA HTTP API (например
	// `kacho-umbrella-openfga:8080`). Тот же store, что использует kacho-iam.
	OpenFGAEndpoint string `mapstructure:"openfga-endpoint"`

	// StoreID — OpenFGA store id (shared с kacho-iam).
	StoreID string `mapstructure:"store-id"`

	// ModelID — pinned authorization_model_id. Empty → store default.
	ModelID string `mapstructure:"model-id"`

	// TimeoutMs — таймаут одного write-вызова (default 2000ms).
	TimeoutMs int `mapstructure:"timeout-ms"`
}

// ListFilterConfig — конфигурация FGA-filtered List.
//
// Source: yaml `authz.list-filter.{enabled,timeout-ms,cache-ttl,max-results,model-id,fail-open}`.
// ENV-override: `KACHO_VPC_AUTHZ__LIST_FILTER__ENABLED=true`, etc.
//
// Когда Enabled=true И authz.iam-endpoint выставлен → каждая List-RPC
// ходит к kacho-iam AuthorizeService.ListObjects на разрешенные ids.
type ListFilterConfig struct {
	// Enabled — главный toggle. Default false (unfiltered behaviour).
	// В production: true.
	Enabled bool `mapstructure:"enabled"`

	// AuthorizeEndpoint — gRPC адрес kacho-iam **public** listener'а
	// (AuthorizeService на :9090, в отличие от InternalIAMService на :9091).
	// Пустая строка → fallback на AuthZConfig.IAMEndpoint (для compat'а с
	// существующими values.yaml; production-mode должен указывать явно).
	AuthorizeEndpoint string `mapstructure:"authorize-endpoint"`

	// AuthorizeTLS — TLS на peer-вызов в kacho-iam AuthorizeService.
	AuthorizeTLS TLSClient `mapstructure:"authorize-tls"`

	// TimeoutMs — таймаут одного ListObjects-вызова (default 500ms):
	// per-call budget ≤100ms p95 + 5x safety margin.
	TimeoutMs int `mapstructure:"timeout-ms"`

	// CacheTTL — TTL positive entries в LRU-кэше (default 5s).
	CacheTTL time.Duration `mapstructure:"cache-ttl"`

	// MaxEntries — hard cap кэша (default 10000). LRU eviction.
	MaxEntries int `mapstructure:"max-entries"`

	// MaxResults — hard cap для ListObjects results (default 10000).
	MaxResults int `mapstructure:"max-results"`

	// ModelID — pinned authorization_model_id.
	// Empty → kacho-iam использует свой default. В production:
	// тот же model id, что seed-ит kacho-iam.
	ModelID string `mapstructure:"model-id"`

	// FailOpen — если true, FGA-error возвращает unfiltered list.
	// Default false (fail-closed). WARN-log + Critical-alert при включении.
	FailOpen bool `mapstructure:"fail-open"`
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

// MetricsConfig — секция metrics: cluster-internal diagnostic HTTP-listener
// (/metrics + /healthz + /readyz). Endpoint пуст ИЛИ Enable=false → listener не
// поднимается (byte-identical back-compat).
type MetricsConfig struct {
	Enable bool `mapstructure:"enable"`
	// Endpoint — адрес diagnostic-listener'а (напр. ":9095"). Cluster-internal,
	// НЕ публикуется на external endpoint и НЕ проксируется api-gateway.
	Endpoint string `mapstructure:"endpoint"`
}

// MetricsEndpoint возвращает адрес diagnostic-listener'а, либо "" если метрики
// выключены (Enable=false) — composition root тогда не поднимает listener.
func (c Config) MetricsEndpoint() string {
	if !c.Metrics.Enable {
		return ""
	}
	return listenAddress(c.Metrics.Endpoint)
}

// HealthcheckConfig — секция healthcheck (placeholder под /healthz).
type HealthcheckConfig struct {
	Enable bool `mapstructure:"enable"`
}

// RepositoryConfig — секция repository. Сейчас single-backend (Postgres);
// `Type` зарезервирован под мульти-БД (migrator должен уметь
// postgres|cockroach|…).
type RepositoryConfig struct {
	Type     string         `mapstructure:"type"`
	Postgres PostgresConfig `mapstructure:"postgres"`
}

// PostgresConfig — секция repository.postgres.
//
//	URL              — стандартный DSN postgres://user:pass@host:port/db (master).
//	SlaveURL         — DSN read-replica (опционально).
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
// Пароль в YAML/ConfigMap — нельзя (commit-able), поэтому он остается
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
// Project-existence peer — kacho-iam (ProjectService.Get); поддерживается
// только `extapi.iam`. zone_id валидируется через kacho-geo (`extapi.geo`) —
// leaf-домен Geography, а не kacho-compute.
type ExtAPIConfig struct {
	DefDialDuration time.Duration `mapstructure:"def-dial-duration"`
	IAM             PeerConfig    `mapstructure:"iam"`
	Geo             PeerConfig    `mapstructure:"geo"`
}

// PeerConfig — параметры одного peer-сервиса.
//
//	Endpoint      — host:port (без `dns:///` — префикс добавляется в dialer'е,
//	                если DNSLB=true).
//	TLS           — TLS-параметры клиента к peer'у.
//	DialDuration  — таймаут на установление conn (0 — extapi.def-dial-duration).
//	DNSLB         — включить gRPC client-side round_robin + dns:/// resolver.
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

// NetworkConfig — секция network (VPC-domain бизнес-настройки).
type NetworkConfig struct {
	// DefaultSGInline — создавать ли default SecurityGroup inline при Network.Create.
	DefaultSGInline bool                     `mapstructure:"default-sg-inline"`
	ProjectCache    ProjectCacheConfigStruct `mapstructure:"project-cache"`
}

// ProjectCacheConfigStruct — TTL+LRU кеш ProjectClient.Exists.
type ProjectCacheConfigStruct struct {
	PositiveTTL time.Duration `mapstructure:"positive-ttl"`
	NegativeTTL time.Duration `mapstructure:"negative-ttl"`
	MaxSize     int           `mapstructure:"max-size"`
}

// schemaOptionsParam — URL-encoded libpq-параметр `options=-c search_path=…`.
// Добавляется в baseDSN автоматически (если еще не задано), чтобы каждое
// соединение (pgxpool, dedicated pgx.Conn для LISTEN, goose-через-database/sql)
// видело таблицы kacho-vpc по unqualified-имени.
//
// Значение search_path — «kacho_vpc, public»:
//   - `kacho_vpc` впереди — наши таблицы (схема создается в baseline
//     `0001_initial.sql`, там же заданы все таблицы);
//   - `public` сзади — `btree_gist`-extension и built-in объекты Postgres,
//     которые extension/CREATE-команды по умолчанию создают там.
//
// Пробел в `-c search_path=…` обязан быть `%20`; знак `=` внутри значения —
// `%3D`; запятая — `%2C`. При смене схемы (ребрендинг / multi-tenant) — менять
// здесь и в `0001_initial.sql` одновременно.
const schemaOptionsParam = "options=-c%20search_path%3Dkacho_vpc%2Cpublic"

// baseDSN — стандартный postgres DSN без pgxpool-параметров; используется
// и pgxpool, и database/sql.Open("pgx"). Делегирует composeDSN(URL) — общему
// формирователю для master- и slave-DSN.
func (c Config) baseDSN() string {
	return c.composeDSN(c.Repository.Postgres.URL)
}

// composeDSN добавляет к raw-DSN (master URL или slave URL) недостающие libpq-
// параметры: `sslmode=<mode>` (из PostgresConfig.SSLMode, default `disable`)
// и `options=-c search_path=kacho_vpc,public` (все VPC-таблицы живут в схеме
// `kacho_vpc`, поэтому каждое соединение должно установить корректный
// search_path).
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
	// Append search_path via libpq `options` parameter, если еще не задан.
	// Распознаем как `options=`, так и URL-encoded `options%3D` (на всякий
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
// НЕ использовать для database/sql.Open("pgx") — pool_max_conns там FATAL.
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

// SlaveDSN — connection string для slave-pool (read-replica). Пустая строка →
// реплика не настроена, caller использует master (Repository.New(master, nil)
// → Reader fallback на master).
//
// SlaveURL совпадает с URL — slave-pool тоже не создается (caller передаст
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
