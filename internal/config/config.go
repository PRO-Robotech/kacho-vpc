package config

import (
	"fmt"

	corecfg "github.com/PRO-Robotech/kacho-corelib/config"
)

/*// TODO:
1) конфиг должен быть наглядным а не просто в виде структуры,
чтобы ты же потом через какое-то время не изучал мучительно код как у тебя ниже
вот например

logger:
  level: oneof<FATAL | ERROR | WARN | INFO | DEBUG> #optional; default=DEBUG
api-server:
  endpoint: oneof< <tcp://<IP:PORT>|<FQDN[:PORT]> | <unix:///unix-sock-path> > #server address
  graceful-shutdown: 10s #optional; default=10s
  grpc-gw-enable: oneof<true|false>  #enables GRPC-GATEWAY; optional; enables '/docs' handle; shows swagger-paper; default=false; use it in dev mode only
metrics:
  enable: true # enables '/metrics' handle; optional; default=true
healthcheck:
  enable: true # enables '/healthcheck' handle; optional; default=true
repository:
  type: POSTGRES #selects storage type; only the POSTGRES is supported for now
  postgres: #used when storage/type points to POSTGRES
    url: postgres://un:psw@host/db #nodefault; is postgres connection string in URL format
authn:
  type: oneof<none|tls> # authentication type; `none` by default
  tls: #uses TLS when authn/type points to 'tls'
    key-file: "filename1.pem" #used when we should send cert to client when client requires secured TLS
    cert-file: "filename2.pem" #used when we should send cert to client when client requires secured TLS
    client:
      verify: oneof<skip|certs-required|verify> #optional; clent verification level; default='skip'
      ca-files: ["file1.pem", "file2.pem", "file3.pem", ...] #CA files when client/verify points to 'verify'

extapi: # секция для подключентя к внешнм API (интеграции)
  def-dial-duration: 10s #optional; default=10s
                     # длительность ожидания подключения
  agents: #секция используемая для создания GRPC подключения к API агентов
    dial-duration: 3s; #optional; default=$(extapi/def-dial-duration)
                       # длительность ожидания подключения
    authn: #fсекция аутентификации
    type: "none|tls" #optional; default="none"
          # выбор типа аутентификации
          # если = tls то связь со всеми типами агентов подразумевает испоьзование TLS
      # если = none аутентификация не используется
      tls: # <- это клиентский TLS, используется если в аутентификации задействован TLS
        key-file: "private-key-file.pem" #mandatory if we use mTLS
        cert-file: "cert-file.pem" #mandatory is we use mTLS
              # key-file и key-file нужны если на стороне сервера идет проверка
              # клиента, его подписи и сертификата
        server:
          verify: true|false #optional; #default=false(insecured TLS)
          name: "server-name" #optional; #can used when $(server/verify)==true; this is Subject Alternate Name (SAN)
          ca-files: ["file1.pem", "file2.pem", ...] #mandatory-when($(server/verify)==true); no-default

2) де факто для конфигов принято использовать
   https://github.com/spf13/viper
   + https://github.com/knadh/koanf
3) основное требование к конфигу - он дожен быть понятным и структурирован так
   чтобы было понятно какие его части к каким компонентам относятся
*/

// Config — конфигурация kacho-vpc.
type Config struct {
	DBHost string `envconfig:"KACHO_VPC_DB_HOST" default:"localhost"`
	//                 ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
	//1) такая привязка к фиксированному ENV очень неудобна
	//2) default:"localhost" - все дефолты должны быть в специальном месте, в main пакете
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

	//^^DBxxx^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
	//см пример конфига выше - секция repository

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
