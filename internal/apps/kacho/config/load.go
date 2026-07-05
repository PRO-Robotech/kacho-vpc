// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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

	// Подстановка пароля из password-from-env (если задан) — и в master URL,
	// и в slave URL. Тот же пароль используется для обоих pool'ов
	// (master + read-replica на одном Patroni-кластере).
	if envName := v.GetString("repository.postgres.password-from-env"); envName != "" {
		if pwd := os.Getenv(envName); pwd != "" {
			urlStr := v.GetString("repository.postgres.url")
			v.Set("repository.postgres.url", injectPasswordIntoDSN(urlStr, pwd))
			if slaveStr := v.GetString("repository.postgres.slave-url"); slaveStr != "" {
				v.Set("repository.postgres.slave-url", injectPasswordIntoDSN(slaveStr, pwd))
			}
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

// applyLegacyEnv — мост из старых ENV-имен в новые viper-ключи. Применяется
// ПОСЛЕ AutomaticEnv: если новый KACHO_VPC_REPOSITORY__POSTGRES__URL задан
// — он уже подхвачен через ENV-binding, legacy игнорируется.
//
// Если задан хотя бы один из KACHO_VPC_DB_HOST/PORT/USER/NAME — собираем DSN
// из них и переопределяем repository.postgres.url. Это нужно потому, что
// текущий values.yaml выставляет ENV-переменные именно так.
//
// KACHO_VPC_DB_PASSWORD остается отдельным механизмом (см. password-from-env).
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
		{"KACHO_VPC_IAM_GRPC_ADDR", "extapi.iam.endpoint"},
		{"KACHO_VPC_IAM_TLS", "extapi.iam.tls.enable"},
		{"KACHO_VPC_IAM_DNS_LB", "extapi.iam.dns-lb"},
		{"KACHO_VPC_GEO_GRPC_ADDR", "extapi.geo.endpoint"},
		{"KACHO_VPC_GEO_TLS", "extapi.geo.tls.enable"},
		{"KACHO_VPC_DEFAULT_SG_INLINE", "network.default-sg-inline"},
		{"KACHO_VPC_PROJECT_CACHE_TTL", "network.project-cache.positive-ttl"},
		{"KACHO_VPC_PROJECT_CACHE_NEGATIVE_TTL", "network.project-cache.negative-ttl"},
		{"KACHO_VPC_PROJECT_CACHE_SIZE", "network.project-cache.max-size"},
		{"KACHO_VPC_AUTH_MODE", "authn.mode"},
		// write-side FGA store-id / model-id. Helm Deployment пробрасывает их
		// как Secret-ref ENV (Secret'ы openfga store/model из bootstrap-job);
		// остальной `authz.tuple-write` приходит из YAML config-файла (ConfigMap).
		// `authz.tuple-write.*` НЕ в RegisterDefaults — ключи есть только в
		// ConfigMap с пустым значением — а viper'овский `Unmarshal` декодит из
		// AllSettings (config-file + defaults + явный Set), НЕ из AutomaticEnv.
		// Поэтому без явного моста ENV-override молча теряется: write-side FGA
		// client поднимается с пустым StoreID → guard `tw.StoreID != ""` не
		// проходит → `fgaTupleWriter` остается nil → hierarchy tuple
		// `vpc_<resource>:<id>#project@project:<pid>` не публикуется → каждый
		// per-resource Get/Update FGA Check уходит в `no path` (403). Явный
		// v.Set детерминированно связывает ENV → ключ (тот же паттерн, что у
		// legacy DB-env выше).
		{"KACHO_VPC_AUTHZ__TUPLE_WRITE__STORE_ID", "authz.tuple-write.store-id"},
		{"KACHO_VPC_AUTHZ__TUPLE_WRITE__MODEL_ID", "authz.tuple-write.model-id"},
		// Тот же класс фикса для read-side list-filter model-id (тоже Secret-ref
		// ENV из bootstrap-job). `authz.list-filter.model-id` ЕСТЬ в
		// RegisterDefaults, поэтому AutomaticEnv его подхватил бы, но явный
		// биндинг держит оба источника FGA model-id ENV согласованными.
		{"KACHO_VPC_AUTHZ__LIST_FILTER__MODEL_ID", "authz.list-filter.model-id"},
		// IAM-integration флаги — ранее читались ad-hoc os.LookupEnv в cmd/
		// (requireIAM / registerDrainerEnabled). Мост держит backward-compat со
		// старыми ENV-именами; значение уходит в bool-поле Config через Unmarshal
		// (строгая bool-валидация: нераспознанная строка → decode-ошибка, а не
		// тихий false — важно для fail-closed security-свитча Require).
		{"KACHO_VPC_REQUIRE_IAM", "iam.require"},
		{"KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED", "iam.register-drainer-enabled"},
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
// — формат, который ждет net.Listen("tcp", …).
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
	endpoint = strings.TrimPrefix(endpoint, "tcp://")
	if strings.HasPrefix(endpoint, "unix://") {
		// unix-сокет поддерживаем по pass-through.
		return endpoint
	}
	if !strings.Contains(endpoint, ":") {
		return ":" + endpoint
	}
	return endpoint
}

// ListenAddress — публичная обертка над listenAddress (для cmd/vpc/main.go).
func (c APIServerConfig) ListenAddress() string         { return listenAddress(c.Endpoint) }
func (c APIServerConfig) InternalListenAddress() string { return listenAddress(c.InternalEndpoint) }
