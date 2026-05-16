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
// валидную конфигурацию с дефолтами, совпадающими с прежним envconfig (skill
// evgeniy §8 J.3).
func TestLoad_Defaults(t *testing.T) {
	// Очищаем legacy ENV которые могли утечь из окружения (CI).
	clearLegacyEnv(t)

	cfg, err := Load("")
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "INFO", cfg.Logger.Level)
	require.Equal(t, "tcp://0.0.0.0:9090", cfg.APIServer.Endpoint)
	require.Equal(t, "tcp://0.0.0.0:9091", cfg.APIServer.InternalEndpoint)
	require.Equal(t, 10*time.Second, cfg.APIServer.GracefulShutdown)

	require.Equal(t, "POSTGRES", cfg.Repository.Type)
	require.Equal(t, "disable", cfg.Repository.Postgres.SSLMode)
	require.Equal(t, 0, cfg.Repository.Postgres.MaxConns)
	require.Equal(t, "KACHO_VPC_DB_PASSWORD", cfg.Repository.Postgres.PasswordFromEnv)

	require.Equal(t, ModeDev, cfg.AuthN.Mode)
	require.False(t, cfg.AuthN.Mode.IsProduction())

	require.Equal(t, 32, cfg.Watch.MaxStreams)
	require.True(t, cfg.Network.DefaultSGInline)
	require.Equal(t, 30*time.Second, cfg.Network.FolderCache.PositiveTTL)
	require.Equal(t, 5*time.Second, cfg.Network.FolderCache.NegativeTTL)
	require.Equal(t, 10000, cfg.Network.FolderCache.MaxSize)

	require.Equal(t, "resource-manager.kacho.svc.cluster.local:9090", cfg.ExtAPI.ResourceManager.Endpoint)
	require.False(t, cfg.ExtAPI.ResourceManager.TLS.Enable)
	require.False(t, cfg.ExtAPI.ResourceManager.DNSLB)
	require.Equal(t, "compute.kacho.svc.cluster.local:9090", cfg.ExtAPI.Compute.Endpoint)
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
watch:
  max-streams: 17
network:
  default-sg-inline: false
  folder-cache:
    positive-ttl: 1m
    negative-ttl: 2s
    max-size: 555
extapi:
  resource-manager:
    endpoint: rm.test:9090
    tls:
      enable: true
    dns-lb: true
  compute:
    endpoint: compute.test:9090
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

	require.Equal(t, 17, cfg.Watch.MaxStreams)
	require.False(t, cfg.Network.DefaultSGInline)
	require.Equal(t, time.Minute, cfg.Network.FolderCache.PositiveTTL)
	require.Equal(t, 2*time.Second, cfg.Network.FolderCache.NegativeTTL)
	require.Equal(t, 555, cfg.Network.FolderCache.MaxSize)

	require.Equal(t, "rm.test:9090", cfg.ExtAPI.ResourceManager.Endpoint)
	require.True(t, cfg.ExtAPI.ResourceManager.TLS.Enable)
	require.True(t, cfg.ExtAPI.ResourceManager.DNSLB)
	require.Equal(t, "compute.test:9090", cfg.ExtAPI.Compute.Endpoint)
	require.True(t, cfg.ExtAPI.Compute.TLS.Enable)

	// DSN корректно подставляет ssl-mode.
	require.Contains(t, cfg.DSN(), "sslmode=require")
	require.Contains(t, cfg.DSN(), "pool_max_conns=99")
	// MigrateDSN — без pool_max_conns.
	require.NotContains(t, cfg.MigrateDSN(), "pool_max_conns")
}

// TestLoad_ENVOverride — KACHO_VPC_REPOSITORY__POSTGRES__URL перекрывает YAML/defaults.
func TestLoad_ENVOverride(t *testing.T) {
	clearLegacyEnv(t)

	t.Setenv("KACHO_VPC_REPOSITORY__POSTGRES__URL", "postgres://envuser@envhost:5432/envdb")
	t.Setenv("KACHO_VPC_WATCH__MAX_STREAMS", "64")
	t.Setenv("KACHO_VPC_AUTHN__MODE", "production")
	t.Setenv("KACHO_VPC_EXTAPI__RESOURCE_MANAGER__TLS__ENABLE", "true")

	cfg, err := Load("")
	require.NoError(t, err)

	require.Equal(t, "postgres://envuser@envhost:5432/envdb", cfg.Repository.Postgres.URL)
	require.Equal(t, 64, cfg.Watch.MaxStreams)
	require.Equal(t, ModeProduction, cfg.AuthN.Mode)
	require.True(t, cfg.ExtAPI.ResourceManager.TLS.Enable)
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
	t.Setenv("KACHO_VPC_WATCH_MAX_STREAMS", "16")
	t.Setenv("KACHO_VPC_AUTH_MODE", "production")
	t.Setenv("KACHO_VPC_DEFAULT_SG_INLINE", "false")
	t.Setenv("KACHO_VPC_FOLDER_CACHE_TTL", "45s")
	t.Setenv("KACHO_VPC_FOLDER_CACHE_NEGATIVE_TTL", "3s")
	t.Setenv("KACHO_VPC_FOLDER_CACHE_SIZE", "9999")
	t.Setenv("KACHO_VPC_RESOURCE_MANAGER_GRPC_ADDR", "rm.legacy:9090")
	t.Setenv("KACHO_VPC_RESOURCE_MANAGER_TLS", "true")
	t.Setenv("KACHO_VPC_RESOURCE_MANAGER_DNS_LB", "true")
	t.Setenv("KACHO_VPC_COMPUTE_GRPC_ADDR", "compute.legacy:9090")
	t.Setenv("KACHO_VPC_COMPUTE_TLS", "true")

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

	require.Equal(t, 16, cfg.Watch.MaxStreams)
	require.Equal(t, ModeProduction, cfg.AuthN.Mode)
	require.False(t, cfg.Network.DefaultSGInline)
	require.Equal(t, 45*time.Second, cfg.Network.FolderCache.PositiveTTL)
	require.Equal(t, 3*time.Second, cfg.Network.FolderCache.NegativeTTL)
	require.Equal(t, 9999, cfg.Network.FolderCache.MaxSize)

	require.Equal(t, "rm.legacy:9090", cfg.ExtAPI.ResourceManager.Endpoint)
	require.True(t, cfg.ExtAPI.ResourceManager.TLS.Enable)
	require.True(t, cfg.ExtAPI.ResourceManager.DNSLB)
	require.Equal(t, "compute.legacy:9090", cfg.ExtAPI.Compute.Endpoint)
	require.True(t, cfg.ExtAPI.Compute.TLS.Enable)
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
  resource-manager:
    tls:
      enable: false
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)

	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "extapi.resource-manager.tls.enable=true required")
	require.Contains(t, err.Error(), "ssl-mode must be one of require|verify-ca|verify-full")
}

// TestValidate_ProductionStrict_Passes — корректная production-strict конфигурация
// проходит Validate.
func TestValidate_ProductionStrict_Passes(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
authn:
  mode: production-strict
repository:
  postgres:
    url: postgres://u:p@h:5432/db
    ssl-mode: verify-full
extapi:
  resource-manager:
    tls:
      enable: true
  compute:
    tls:
      enable: true
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
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

// TestValidate_WatchMaxStreams_MustBePositive — watch.max-streams=0 отбивается.
func TestValidate_WatchMaxStreams_MustBePositive(t *testing.T) {
	clearLegacyEnv(t)

	yaml := `
watch:
  max-streams: 0
`
	cfg, err := Load(writeTempYAML(t, yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "watch.max-streams must be > 0")
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
// Skill evgeniy §6 G.4.
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

// clearLegacyEnv — пустит фоновый shell-наполнитель тестов с pristine окружением.
// CI может наследовать KACHO_VPC_DB_PASSWORD/KACHO_VPC_AUTH_MODE — это сломает
// independence тестов. Очищаем перед каждым TestXxx, который опирается на defaults.
func clearLegacyEnv(t *testing.T) {
	t.Helper()
	for _, name := range os.Environ() {
		if i := indexByte(name, '='); i > 0 {
			n := name[:i]
			if strings.HasPrefix(n, "KACHO_VPC_") {
				t.Setenv(n, "")    // временно пусто
				_ = os.Unsetenv(n) // и реально снимем (t.Setenv восстанавливает после теста)
			}
		}
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
