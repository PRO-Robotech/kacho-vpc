// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLoad_Defaults — sanity: при пустом path и без ENV-override Load возвращает
// валидную конфигурацию с дефолтами.
func TestLoad_Defaults(t *testing.T) {
	// Очищаем legacy ENV которые могли утечь из окружения (CI).
	clearLegacyEnv(t)

	cfg, err := Load("")
	require.NoError(t, err)
	// Fail-closed prod-гардрейл (S1): production-по-дефолту требует явный
	// authz.iam-endpoint (либо authn.mode=dev). Задаем endpoint, чтобы изолировать
	// проверку дефолтов от guardrail-отказа.
	cfg.AuthZ.IAMEndpoint = "kacho-iam.kacho.svc.cluster.local:9091"
	// S1b prod-гардрейл: production требует защищённый sslmode. Дефолт "disable"
	// (проверяется ниже) валиден только для dev — переопределяем, чтобы изолировать
	// проверку загрузки дефолтов от sslmode-гардрейла.
	loadedSSLMode := cfg.Repository.Postgres.SSLMode
	cfg.Repository.Postgres.SSLMode = "require"
	require.NoError(t, cfg.Validate())
	cfg.Repository.Postgres.SSLMode = loadedSSLMode

	require.Equal(t, "INFO", cfg.Logger.Level)
	require.Equal(t, "tcp://0.0.0.0:9090", cfg.APIServer.Endpoint)
	require.Equal(t, "tcp://0.0.0.0:9091", cfg.APIServer.InternalEndpoint)
	require.Equal(t, 10*time.Second, cfg.APIServer.GracefulShutdown)

	require.Equal(t, "POSTGRES", cfg.Repository.Type)
	require.Equal(t, "disable", cfg.Repository.Postgres.SSLMode)
	require.Equal(t, 0, cfg.Repository.Postgres.MaxConns)
	require.Equal(t, "KACHO_VPC_DB_PASSWORD", cfg.Repository.Postgres.PasswordFromEnv)

	require.Equal(t, ModeProduction, cfg.AuthN.Mode)
	require.True(t, cfg.AuthN.Mode.IsProduction())

	require.True(t, cfg.Network.DefaultSGInline)
	require.Equal(t, 30*time.Second, cfg.Network.ProjectCache.PositiveTTL)
	require.Equal(t, 5*time.Second, cfg.Network.ProjectCache.NegativeTTL)
	require.Equal(t, 10000, cfg.Network.ProjectCache.MaxSize)

	require.Equal(t, "iam.kacho.svc.cluster.local:9090", cfg.ExtAPI.IAM.Endpoint)
	require.False(t, cfg.ExtAPI.IAM.TLS.Enable)
	require.False(t, cfg.ExtAPI.IAM.DNSLB)
	require.Equal(t, "kacho-geo.kacho.svc.cluster.local:9090", cfg.ExtAPI.Geo.Endpoint)
}

// TestLoad_GeoEndpointDialHost — regression guard на geo dial-host.
//
// geo k8s Service называется `kacho-geo`, а его server-cert SAN покрывает
// только `kacho-geo.*` / `kacho-geo-internal.*`. Dial по старому хосту
// `geo.kacho.svc...` ломает и DNS-резолв, и проверку TLS serverName, как только
// включается mTLS-ребро. Default extapi.geo.endpoint dial-host ОБЯЗАН быть
// `kacho-geo.kacho.svc.cluster.local:9090` (public :9090 listener, который
// обслуживает ZoneService.Get/List), а не `geo.kacho.svc.cluster.local:9090`.
func TestLoad_GeoEndpointDialHost(t *testing.T) {
	clearLegacyEnv(t)

	cfg, err := Load("")
	require.NoError(t, err)
	cfg.AuthZ.IAMEndpoint = "kacho-iam.kacho.svc.cluster.local:9091" // S1 prod-гардрейл
	cfg.Repository.Postgres.SSLMode = "require"                      // S1b prod-гардрейл
	require.NoError(t, cfg.Validate())

	require.Equal(t, "kacho-geo.kacho.svc.cluster.local:9090", cfg.ExtAPI.Geo.Endpoint,
		"geo dial-host must match the kacho-geo Service name + server-cert SAN (kacho-geo.*)")
}

// TestLoad_YAMLOverride — значения из YAML файла перекрывают defaults.
func TestLoad_YAMLOverride(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
logger:
  level: DEBUG
api-server:
  endpoint: tcp://127.0.0.1:18080
  internal-endpoint: tcp://127.0.0.1:18081
  graceful-shutdown: 25s
repository:
  postgres:
    url: postgres://vpc-test@db.test:5432/kacho_vpc_test
    max-conns: 99
    ssl-mode: require
authn:
  mode: production
authz:
  iam-endpoint: iam.test:9091
network:
  default-sg-inline: false
  project-cache:
    positive-ttl: 1m
    negative-ttl: 2s
    max-size: 555
extapi:
  iam:
    endpoint: iam.test:9090
    tls:
      enable: true
    dns-lb: true
  geo:
    endpoint: geo.test:9090
    tls:
      enable: true
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "DEBUG", cfg.Logger.Level)
	require.Equal(t, "tcp://127.0.0.1:18080", cfg.APIServer.Endpoint)
	require.Equal(t, "127.0.0.1:18080", cfg.APIServer.ListenAddress())
	require.Equal(t, "127.0.0.1:18081", cfg.APIServer.InternalListenAddress())
	require.Equal(t, 25*time.Second, cfg.APIServer.GracefulShutdown)

	require.Equal(t, "postgres://vpc-test@db.test:5432/kacho_vpc_test", cfg.Repository.Postgres.URL)
	require.Equal(t, 99, cfg.Repository.Postgres.MaxConns)
	require.Equal(t, "require", cfg.Repository.Postgres.SSLMode)

	require.Equal(t, ModeProduction, cfg.AuthN.Mode)
	require.True(t, cfg.AuthN.Mode.IsProduction())

	require.False(t, cfg.Network.DefaultSGInline)
	require.Equal(t, time.Minute, cfg.Network.ProjectCache.PositiveTTL)
	require.Equal(t, 2*time.Second, cfg.Network.ProjectCache.NegativeTTL)
	require.Equal(t, 555, cfg.Network.ProjectCache.MaxSize)

	require.Equal(t, "iam.test:9090", cfg.ExtAPI.IAM.Endpoint)
	require.True(t, cfg.ExtAPI.IAM.TLS.Enable)
	require.True(t, cfg.ExtAPI.IAM.DNSLB)
	require.Equal(t, "geo.test:9090", cfg.ExtAPI.Geo.Endpoint)
	require.True(t, cfg.ExtAPI.Geo.TLS.Enable)

	// DSN корректно подставляет ssl-mode.
	require.Contains(t, cfg.DSN(), "sslmode=require")
	require.Contains(t, cfg.DSN(), "pool_max_conns=99")
	// MigrateDSN — без pool_max_conns.
	require.NotContains(t, cfg.MigrateDSN(), "pool_max_conns")
	// Оба DSN обязаны нести libpq-параметр
	// `options=-c search_path=kacho_vpc,public`, иначе session попадет в
	// `public` (default) и не найдет таблицы схемы kacho_vpc.
	require.Contains(t, cfg.DSN(), "options=-c%20search_path%3Dkacho_vpc%2Cpublic")
	require.Contains(t, cfg.MigrateDSN(), "options=-c%20search_path%3Dkacho_vpc%2Cpublic")
}

// TestDSN_ServingTimeouts_InServingNotMigrate — statement_timeout / lock_timeout
// попадают только в serving-DSN (DSN/SlaveDSN), НЕ в MigrateDSN (миграции могут
// легитимно превышать лимит). Защита от bounded-pool exhaustion (CWE-770).
func TestDSN_ServingTimeouts_InServingNotMigrate(t *testing.T) {
	var c Config
	c.Repository.Postgres.URL = "postgres://u@h:5432/db"
	c.Repository.Postgres.StatementTimeout = 30 * time.Second
	c.Repository.Postgres.LockTimeout = 15 * time.Second

	dsn := c.DSN()
	require.Contains(t, dsn, "statement_timeout%3D30000", "serving DSN must carry statement_timeout (ms)")
	require.Contains(t, dsn, "lock_timeout%3D15000", "serving DSN must carry lock_timeout (ms)")
	// search_path остаётся первым сегментом options (обратная совместимость).
	require.Contains(t, dsn, "options=-c%20search_path%3Dkacho_vpc%2Cpublic")

	mig := c.MigrateDSN()
	require.NotContains(t, mig, "statement_timeout", "MigrateDSN must NOT bound migrations")
	require.NotContains(t, mig, "lock_timeout", "MigrateDSN must NOT bound migrations")
	require.Contains(t, mig, "options=-c%20search_path%3Dkacho_vpc%2Cpublic")
}

// TestDSN_ZeroTimeouts_Omitted — 0-Duration → соответствующий GUC не добавляется
// (Postgres default). search_path всё равно присутствует.
func TestDSN_ZeroTimeouts_Omitted(t *testing.T) {
	var c Config
	c.Repository.Postgres.URL = "postgres://u@h:5432/db"
	// StatementTimeout / LockTimeout = 0 (zero-value).
	dsn := c.DSN()
	require.NotContains(t, dsn, "statement_timeout")
	require.NotContains(t, dsn, "lock_timeout")
	require.Contains(t, dsn, "options=-c%20search_path%3Dkacho_vpc%2Cpublic")
}

// TestLoad_ENVOverride — KACHO_VPC_REPOSITORY__POSTGRES__URL перекрывает YAML/defaults.
func TestLoad_ENVOverride(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_REPOSITORY__POSTGRES__URL", "postgres://envuser@envhost:5432/envdb")
	t.Setenv("KACHO_VPC_AUTHN__MODE", "production")
	t.Setenv("KACHO_VPC_EXTAPI__IAM__TLS__ENABLE", "true")

	cfg, err := Load("")
	require.NoError(t, err)

	require.Equal(t, "postgres://envuser@envhost:5432/envdb", cfg.Repository.Postgres.URL)
	require.Equal(t, ModeProduction, cfg.AuthN.Mode)
	require.True(t, cfg.ExtAPI.IAM.TLS.Enable)
}

// TestLoad_TupleWriteSecretENVOverride — regression guard на Secret-ref ENV
// write-side FGA.
//
// Helm Deployment пробрасывает write-side FGA store-id / model-id как
// Secret-ref ENV (KACHO_VPC_AUTHZ__TUPLE_WRITE__STORE_ID / __MODEL_ID);
// остальной `authz.tuple-write` приходит из ConfigMap YAML. `tuple-write.*`
// НЕ в RegisterDefaults, поэтому без явного моста в applyLegacyEnv viper'овский
// `Unmarshal` молча терял ENV-значение — write-side FGA client поднимался с
// пустым StoreID, оставался nil и не публиковал per-resource hierarchy tuple →
// каждый per-resource Check уходил в FGA `no path`. Тест проверяет, что
// ENV-значение доходит до структуры, и `cmd/vpc` поднимает writer.
func TestLoad_TupleWriteSecretENVOverride(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_AUTHZ__TUPLE_WRITE__STORE_ID", "01STORE000000000000000000")
	t.Setenv("KACHO_VPC_AUTHZ__TUPLE_WRITE__MODEL_ID", "01MODEL000000000000000000")
	t.Setenv("KACHO_VPC_AUTHZ__LIST_FILTER__MODEL_ID", "01MODEL000000000000000000")

	cfg, err := Load("")
	require.NoError(t, err)

	require.Equal(t, "01STORE000000000000000000", cfg.AuthZ.TupleWrite.StoreID,
		"write-side FGA store-id ENV override must reach the struct")
	require.Equal(t, "01MODEL000000000000000000", cfg.AuthZ.TupleWrite.ModelID,
		"write-side FGA model-id ENV override must reach the struct")
	require.Equal(t, "01MODEL000000000000000000", cfg.AuthZ.ListFilter.ModelID,
		"read-side FGA list-filter model-id ENV override must reach the struct")
}

// TestLoad_LegacyENV — старые ENV (KACHO_VPC_DB_HOST/PORT/...) транслируются
// в новые ключи через applyLegacyEnv (backward-compat для текущего Helm chart).
func TestLoad_LegacyENV(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_DB_HOST", "legacy-host")
	t.Setenv("KACHO_VPC_DB_PORT", "15432")
	t.Setenv("KACHO_VPC_DB_USER", "legacy-user")
	t.Setenv("KACHO_VPC_DB_NAME", "legacy_db")
	t.Setenv("KACHO_VPC_DB_PASSWORD", "legacy-secret")
	t.Setenv("KACHO_VPC_DB_SSLMODE", "require")
	t.Setenv("KACHO_VPC_DB_MAX_CONNS", "42")
	t.Setenv("KACHO_VPC_GRPC_PORT", "19090")
	t.Setenv("KACHO_VPC_INTERNAL_PORT", "19091")
	t.Setenv("KACHO_VPC_AUTH_MODE", "production")
	t.Setenv("KACHO_VPC_DEFAULT_SG_INLINE", "false")
	t.Setenv("KACHO_VPC_PROJECT_CACHE_TTL", "45s")
	t.Setenv("KACHO_VPC_PROJECT_CACHE_NEGATIVE_TTL", "3s")
	t.Setenv("KACHO_VPC_PROJECT_CACHE_SIZE", "9999")
	t.Setenv("KACHO_VPC_IAM_GRPC_ADDR", "iam.legacy:9090")
	t.Setenv("KACHO_VPC_IAM_TLS", "true")
	t.Setenv("KACHO_VPC_IAM_DNS_LB", "true")
	t.Setenv("KACHO_VPC_GEO_GRPC_ADDR", "geo.legacy:9090")
	t.Setenv("KACHO_VPC_GEO_TLS", "true")

	cfg, err := Load("")
	require.NoError(t, err)

	// URL собран из split env, пароль подставлен из password-from-env.
	require.Equal(t, "postgres://legacy-user:legacy-secret@legacy-host:15432/legacy_db", cfg.Repository.Postgres.URL)
	require.Equal(t, "require", cfg.Repository.Postgres.SSLMode)
	require.Equal(t, 42, cfg.Repository.Postgres.MaxConns)

	require.Equal(t, "tcp://0.0.0.0:19090", cfg.APIServer.Endpoint)
	require.Equal(t, "tcp://0.0.0.0:19091", cfg.APIServer.InternalEndpoint)
	require.Equal(t, "0.0.0.0:19090", cfg.APIServer.ListenAddress())
	require.Equal(t, "0.0.0.0:19091", cfg.APIServer.InternalListenAddress())

	require.Equal(t, ModeProduction, cfg.AuthN.Mode)
	require.False(t, cfg.Network.DefaultSGInline)
	require.Equal(t, 45*time.Second, cfg.Network.ProjectCache.PositiveTTL)
	require.Equal(t, 3*time.Second, cfg.Network.ProjectCache.NegativeTTL)
	require.Equal(t, 9999, cfg.Network.ProjectCache.MaxSize)

	require.Equal(t, "iam.legacy:9090", cfg.ExtAPI.IAM.Endpoint)
	require.True(t, cfg.ExtAPI.IAM.TLS.Enable)
	require.True(t, cfg.ExtAPI.IAM.DNSLB)
	require.Equal(t, "geo.legacy:9090", cfg.ExtAPI.Geo.Endpoint)
	require.True(t, cfg.ExtAPI.Geo.TLS.Enable)
}

// TestLoad_IAMFlags_Defaults — секция iam: fail-closed boot-gate выключен по
// умолчанию (require=false), register-drainer включён (default-on).
func TestLoad_IAMFlags_Defaults(t *testing.T) {
	clearLegacyEnv(t)

	cfg, err := Load("")
	require.NoError(t, err)
	require.False(t, cfg.IAM.Require, "iam.require default must be false (dev: Create allowed, Warn only)")
	require.True(t, cfg.IAM.RegisterDrainerEnabled, "iam.register-drainer-enabled default must be true (default-on)")
}

// TestLoad_IAMFlags_LegacyENV — старые ENV KACHO_VPC_REQUIRE_IAM /
// KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED транслируются в новые ключи
// iam.require / iam.register-drainer-enabled (backward-compat для задеплоенного
// Helm chart, ранее читались ad-hoc через os.LookupEnv в cmd/).
func TestLoad_IAMFlags_LegacyENV(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_REQUIRE_IAM", "true")
	t.Setenv("KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED", "false")

	cfg, err := Load("")
	require.NoError(t, err)
	require.True(t, cfg.IAM.Require, "KACHO_VPC_REQUIRE_IAM=true → iam.require=true")
	require.False(t, cfg.IAM.RegisterDrainerEnabled, "KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED=false → false")
}

// TestLoad_IAMFlags_NumericLegacyENV — «1» тоже включает флаг (совместимость с
// прежним requireIAM(): v=="true" || v=="1").
func TestLoad_IAMFlags_NumericLegacyENV(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_REQUIRE_IAM", "1")

	cfg, err := Load("")
	require.NoError(t, err)
	require.True(t, cfg.IAM.Require, "KACHO_VPC_REQUIRE_IAM=1 → iam.require=true")
}

// TestLoad_IAMFlags_RejectsUnrecognizedBool — ключевое ужесточение: нераспознанное
// значение security-свитча (yes/enabled/on) обязано ОТКАЗАТЬ загрузку, а не тихо
// свалиться в false (прежний requireIAM() принимал только "true"/"1" и молча
// возвращал false на "yes" → fail-open). Строгая bool-валидация на decode.
func TestLoad_IAMFlags_RejectsUnrecognizedBool(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_REQUIRE_IAM", "yes")

	_, err := Load("")
	require.Error(t, err, "unrecognized bool for iam.require must fail loudly, not silently default to false")
}

// TestLoad_IAMFlags_NewKeyENV — новый двойной-underscore ENV-ключ тоже работает
// (KACHO_VPC_IAM__REGISTER_DRAINER_ENABLED).
func TestLoad_IAMFlags_NewKeyENV(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_IAM__REGISTER_DRAINER_ENABLED", "false")

	cfg, err := Load("")
	require.NoError(t, err)
	require.False(t, cfg.IAM.RegisterDrainerEnabled)
}

// TestValidate_ProductionStrict_RequiresTLSAndSSL — production-strict не пускает
// без TLS на peer и не пускает с sslmode=disable.
func TestValidate_ProductionStrict_RequiresTLSAndSSL(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
authn:
  mode: production-strict
repository:
  postgres:
    url: postgres://u@h:5432/db
    ssl-mode: disable
extapi:
  iam:
    tls:
      enable: false
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)

	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "extapi.iam.tls.enable=true required")
	require.Contains(t, err.Error(), "ssl-mode must be one of require|verify-ca|verify-full")
}

// TestValidate_ProductionStrict_Passes — корректная production-strict конфигурация
// проходит Validate.
func TestValidate_ProductionStrict_Passes(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
authn:
  mode: production-strict
authz:
  iam-endpoint: kacho-iam.kacho.svc.cluster.local:9091
repository:
  postgres:
    url: postgres://u:p@h:5432/db
    ssl-mode: verify-full
extapi:
  iam:
    tls:
      enable: true
  geo:
    tls:
      enable: true
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	// S1: authz.iam-endpoint задан выше. server-mTLS (S2) проверяется отдельно
	// через ValidateServerMTLS, поэтому Validate() здесь проходит.
	require.NoError(t, cfg.Validate())
	require.True(t, cfg.AuthN.Mode.IsProduction())
}

// TestValidate_UnknownMode_FailsAtLoad — unknown authn.mode отбивается при
// Unmarshal через DecodeHook.
func TestValidate_UnknownMode_FailsAtLoad(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
authn:
  mode: xxx-bogus
`
	_, err := Load(writeTempYAML(t, yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown mode")
}

// TestValidate_BadSSLMode — sslmode=xxx отбивается Validate.
func TestValidate_BadSSLMode(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
repository:
  postgres:
    url: postgres://u@h:5432/db
    ssl-mode: bogus
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ssl-mode")
}

// TestValidate_EmptyDSN — пустой URL отбивается.
func TestValidate_EmptyDSN(t *testing.T) {
	clearLegacyEnv(t)

	// Очищаем default URL через явный override на пусто.
	yaml := `
repository:
  postgres:
    url: ""
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "repository.postgres.url is empty")
}

// TestMode_StringRoundtrip — String/parseMode симметричны для всех ENUM-значений.
func TestMode_StringRoundtrip(t *testing.T) {
	for _, m := range []Mode{ModeDev, ModeProduction, ModeProductionStrict} {
		got, err := parseMode(m.String())
		require.NoError(t, err)
		require.Equal(t, m, got, "roundtrip for %s", m)
	}
}

// TestMode_IsProduction — фиксирует контракт ENUM (Dev=non-prod, остальные=prod).
func TestMode_IsProduction(t *testing.T) {
	require.False(t, ModeDev.IsProduction())
	require.True(t, ModeProduction.IsProduction())
	require.True(t, ModeProductionStrict.IsProduction())
}

// TestListenAddress_Formats — нормализация tcp://host:port / :port / голый порт.
func TestListenAddress_Formats(t *testing.T) {
	tests := map[string]string{
		"tcp://0.0.0.0:9090": "0.0.0.0:9090",
		"tcp://:9090":        ":9090",
		":9090":              ":9090",
		"9090":               ":9090",
		"0.0.0.0:9090":       "0.0.0.0:9090",
		"":                   "",
	}
	for in, want := range tests {
		require.Equal(t, want, listenAddress(in), "input=%q", in)
	}
}

// TestInjectPasswordIntoDSN_Idempotent — injectPasswordIntoDSN не перепишет
// уже-указанный пароль.
func TestInjectPasswordIntoDSN_Idempotent(t *testing.T) {
	dsn := "postgres://u:already@h:5432/db"
	require.Equal(t, dsn, injectPasswordIntoDSN(dsn, "new-pwd"))
}

// TestSlaveDSN_EmptyWhenUnset — slave-url не задан → SlaveDSN возвращает "".
// Composition root читает это как "slavePool=nil" → fallback к master.
func TestSlaveDSN_EmptyWhenUnset(t *testing.T) {
	clearLegacyEnv(t)
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "", cfg.Repository.Postgres.SlaveURL)
	require.Equal(t, "", cfg.SlaveDSN())
}

// TestSlaveDSN_EmptyWhenEqualToMaster — slave-url == url считаем как "не настроено".
// Не плодим второй pool к той же физической БД.
func TestSlaveDSN_EmptyWhenEqualToMaster(t *testing.T) {
	clearLegacyEnv(t)
	yaml := `
repository:
  postgres:
    url: postgres://u@h:5432/db
    slave-url: postgres://u@h:5432/db
    ssl-mode: disable
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	require.Equal(t, "", cfg.SlaveDSN())
}

// TestSlaveDSN_PopulatedFromYAML — slave-url из YAML формирует валидный DSN
// с подставленным sslmode и pool_max_conns.
func TestSlaveDSN_PopulatedFromYAML(t *testing.T) {
	clearLegacyEnv(t)
	yaml := `
repository:
  postgres:
    url: postgres://u@master:5432/db
    slave-url: postgres://u@replica:5432/db
    ssl-mode: require
    max-conns: 25
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	dsn := cfg.SlaveDSN()
	require.Contains(t, dsn, "@replica:5432/db")
	require.Contains(t, dsn, "sslmode=require")
	require.Contains(t, dsn, "pool_max_conns=25")
}

// TestSlaveDSN_PasswordFromEnvAppliedToBoth — пароль из password-from-env
// подставляется и в master URL, и в slave URL.
func TestSlaveDSN_PasswordFromEnvAppliedToBoth(t *testing.T) {
	clearLegacyEnv(t)
	t.Setenv("KACHO_VPC_DB_PASSWORD", "s3cret")
	yaml := `
repository:
  postgres:
    url: postgres://u@master:5432/db
    slave-url: postgres://u@replica:5432/db
    ssl-mode: disable
    password-from-env: KACHO_VPC_DB_PASSWORD
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	require.Contains(t, cfg.Repository.Postgres.URL, "u:s3cret@master")
	require.Contains(t, cfg.Repository.Postgres.SlaveURL, "u:s3cret@replica")
	require.Contains(t, cfg.SlaveDSN(), "u:s3cret@replica")
}

// TestSlaveDSN_FromENV — KACHO_VPC_REPOSITORY__POSTGRES__SLAVE_URL пробрасывается
// через ENV-binding viper'а.
func TestSlaveDSN_FromENV(t *testing.T) {
	clearLegacyEnv(t)
	t.Setenv("KACHO_VPC_REPOSITORY__POSTGRES__URL", "postgres://u@master:5432/db")
	t.Setenv("KACHO_VPC_REPOSITORY__POSTGRES__SLAVE_URL", "postgres://u@replica:5432/db")
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "postgres://u@replica:5432/db", cfg.Repository.Postgres.SlaveURL)
	require.Contains(t, cfg.SlaveDSN(), "@replica:5432/db")
}

// --- helpers ---

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

// clearLegacyEnv снимает все KACHO_VPC_*-переменные, чтобы тест стартовал в
// чистом окружении. CI может наследовать KACHO_VPC_DB_PASSWORD/KACHO_VPC_AUTH_MODE
// — это сломает independence тестов. Зовется перед каждым TestXxx, который
// опирается на defaults.
func clearLegacyEnv(t *testing.T) {
	t.Helper()
	for _, name := range os.Environ() {
		if i := strings.IndexByte(name, '='); i > 0 {
			n := name[:i]
			if strings.HasPrefix(n, "KACHO_VPC_") {
				t.Setenv(n, "")    // временно пусто
				_ = os.Unsetenv(n) // и реально снимем (t.Setenv восстанавливает после теста)
			}
		}
	}
}
