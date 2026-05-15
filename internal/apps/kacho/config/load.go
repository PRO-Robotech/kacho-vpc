package config

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Load загружает конфигурацию из YAML-файла (если path != "") + ENV-override.
//
// Поведение:
//  1. Регистрируются default'ы (RegisterDefaults).
//  2. ENV-binding: prefix `KACHO_VPC`, разделитель ключей `__` →
//     `KACHO_VPC_REPOSITORY__POSTGRES__URL` пробрасывается в
//     `repository.postgres.url`. Дефис в ключе (`max-conns`) подменяется
//     на `_` (`MAX_CONNS`) — viper's SetEnvKeyReplacer.
//  3. Если path != "" — YAML читается и накладывается на defaults.
//  4. ENV перекрывает YAML + defaults.
//  5. Legacy ENV-aliases (KACHO_VPC_DB_HOST/PORT/USER/NAME/PASSWORD/…)
//     транслируются в новые ключи через applyLegacyEnv — backward-compat
//     для уже задеплоенного Helm chart и dev-скриптов.
//  6. Unmarshal в Config с кастомным DecodeHook (Mode-ENUM из строки).
//
// Возвращает Config + ошибку. Validate() вызывает caller отдельно (в main).
func Load(path string) (Config, error) {
	v := viper.New()
	RegisterDefaults(v)

	// ENV-binding.
	v.SetEnvPrefix("KACHO_VPC")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "__", "-", "_"))
	v.AutomaticEnv()

	// YAML-файл (опционально).
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
	}

	// Legacy ENV → новые ключи (backward-compat).
	applyLegacyEnv(v)

	// Подстановка пароля из password-from-env (если задан).
	if envName := v.GetString("repository.postgres.password-from-env"); envName != "" {
		if pwd := os.Getenv(envName); pwd != "" {
			urlStr := v.GetString("repository.postgres.url")
			urlStr = injectPasswordIntoDSN(urlStr, pwd)
			v.Set("repository.postgres.url", urlStr)
		}
	}

	// Unmarshal в Config с кастомным hook для Mode-ENUM.
	var cfg Config
	decoderOpts := func(dc *mapstructure.DecoderConfig) {
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
			modeDecodeHook(),
		)
	}
	if err := v.Unmarshal(&cfg, decoderOpts); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg, nil
}

// applyLegacyEnv — мост из старых ENV-имён в новые viper-ключи. Применяется
// ПОСЛЕ AutomaticEnv: если новый KACHO_VPC_REPOSITORY__POSTGRES__URL задан
// — он уже подхвачен через ENV-binding, legacy игнорируется.
//
// Если задан хотя бы один из KACHO_VPC_DB_HOST/PORT/USER/NAME — собираем DSN
// из них и переопределяем repository.postgres.url. Это нужно потому, что
// текущий values.yaml выставляет ENV-переменные именно так.
//
// KACHO_VPC_DB_PASSWORD остаётся отдельным механизмом (см. password-from-env).
func applyLegacyEnv(v *viper.Viper) {
	type mapping struct {
		env string
		key string
	}
	simple := []mapping{
		{"KACHO_VPC_DB_SSLMODE", "repository.postgres.ssl-mode"},
		{"KACHO_VPC_DB_MAX_CONNS", "repository.postgres.max-conns"},
		{"KACHO_VPC_GRPC_PORT", "_legacy.grpc-port"},
		{"KACHO_VPC_INTERNAL_PORT", "_legacy.internal-port"},
		{"KACHO_VPC_WATCH_MAX_STREAMS", "watch.max-streams"},
		{"KACHO_VPC_RESOURCE_MANAGER_GRPC_ADDR", "extapi.resource-manager.endpoint"},
		{"KACHO_VPC_RESOURCE_MANAGER_TLS", "extapi.resource-manager.tls.enable"},
		{"KACHO_VPC_RESOURCE_MANAGER_DNS_LB", "extapi.resource-manager.dns-lb"},
		{"KACHO_VPC_COMPUTE_GRPC_ADDR", "extapi.compute.endpoint"},
		{"KACHO_VPC_COMPUTE_TLS", "extapi.compute.tls.enable"},
		{"KACHO_VPC_DEFAULT_SG_INLINE", "network.default-sg-inline"},
		{"KACHO_VPC_FOLDER_CACHE_TTL", "network.folder-cache.positive-ttl"},
		{"KACHO_VPC_FOLDER_CACHE_NEGATIVE_TTL", "network.folder-cache.negative-ttl"},
		{"KACHO_VPC_FOLDER_CACHE_SIZE", "network.folder-cache.max-size"},
		{"KACHO_VPC_AUTH_MODE", "authn.mode"},
	}
	for _, m := range simple {
		if val, ok := os.LookupEnv(m.env); ok {
			v.Set(m.key, val)
		}
	}

	// DB DSN compose из split-env (KACHO_VPC_DB_HOST/PORT/USER/NAME).
	host, hasHost := os.LookupEnv("KACHO_VPC_DB_HOST")
	port, hasPort := os.LookupEnv("KACHO_VPC_DB_PORT")
	user, hasUser := os.LookupEnv("KACHO_VPC_DB_USER")
	db, hasDB := os.LookupEnv("KACHO_VPC_DB_NAME")
	if hasHost || hasPort || hasUser || hasDB {
		if host == "" {
			host = "localhost"
		}
		if port == "" {
			port = "5432"
		}
		if user == "" {
			user = "vpc"
		}
		if db == "" {
			db = "kacho_vpc"
		}
		v.Set("repository.postgres.url", fmt.Sprintf("postgres://%s@%s:%s/%s", user, host, port, db))
	}

	// Legacy port→endpoint composer.
	if p := v.GetString("_legacy.grpc-port"); p != "" {
		v.Set("api-server.endpoint", "tcp://0.0.0.0:"+p)
	}
	if p := v.GetString("_legacy.internal-port"); p != "" {
		v.Set("api-server.internal-endpoint", "tcp://0.0.0.0:"+p)
	}
}

// injectPasswordIntoDSN добавляет пароль в DSN (postgres://user@host →
// postgres://user:pwd@host). Если пароль уже в URL — оставляем как есть.
func injectPasswordIntoDSN(dsn, pwd string) string {
	if dsn == "" {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	if u.User == nil {
		return dsn
	}
	if _, has := u.User.Password(); has {
		return dsn
	}
	u.User = url.UserPassword(u.User.Username(), pwd)
	return u.String()
}

// modeDecodeHook — DecodeHook для viper.Unmarshal: парсит string → Mode (ENUM).
// Без него mapstructure не знает, как превратить "dev" в config.Mode (int).
func modeDecodeHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
		if to != reflect.TypeOf(Mode(0)) {
			return data, nil
		}
		switch v := data.(type) {
		case string:
			return parseMode(v)
		case int:
			return Mode(v), nil
		case int64:
			return Mode(v), nil
		case float64:
			return Mode(int(v)), nil
		default:
			return data, nil
		}
	}
}

// listenAddress нормализует строку Endpoint из YAML в `:port` или `host:port`
// — формат, который ждёт net.Listen("tcp", …).
//
// Поддерживаемые входы:
//
//	`tcp://0.0.0.0:9090` → `0.0.0.0:9090`
//	`tcp://:9090`        → `:9090`
//	`:9090`              → `:9090`
//	`9090`               → `:9090`
//	`0.0.0.0:9090`       → `0.0.0.0:9090`
func listenAddress(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if strings.HasPrefix(endpoint, "tcp://") {
		endpoint = strings.TrimPrefix(endpoint, "tcp://")
	}
	if strings.HasPrefix(endpoint, "unix://") {
		// unix-сокет поддерживаем по pass-through.
		return endpoint
	}
	if !strings.Contains(endpoint, ":") {
		return ":" + endpoint
	}
	return endpoint
}

// ListenAddress — публичная обёртка над listenAddress (для cmd/vpc/main.go).
func (c APIServerConfig) ListenAddress() string         { return listenAddress(c.Endpoint) }
func (c APIServerConfig) InternalListenAddress() string { return listenAddress(c.InternalEndpoint) }
