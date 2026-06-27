// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/H-BF/corlib/pkg/parallel"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/grpcclient"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/observability"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-corelib/outbox/bootgate"
	"github.com/PRO-Robotech/kacho-corelib/outbox/drainer"
	"github.com/PRO-Robotech/kacho-corelib/outbox/metrics"
	"github.com/PRO-Robotech/kacho-corelib/safeconv"

	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	addressapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	addresspoolapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	gatewayapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/gateway"
	networkapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/network"
	niapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/networkinterface"
	routetableapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/routetable"
	sgapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/securitygroup"
	subnetapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/subnet"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addressref"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/fgaboot"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/observability/health"
	vpcmetrics "github.com/PRO-Robotech/kacho-vpc/internal/observability/metrics"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// configPathEnv — путь к YAML-конфигу. Пустое значение допустимо (defaults +
// ENV-override). Helm chart выставляет KACHO_VPC_CONFIG_PATH=/etc/kacho-vpc/config.yaml.
const configPathEnv = "KACHO_VPC_CONFIG_PATH"

func main() {
	// kacho-vpc — single-purpose binary: только обслуживает API. Миграции живут в
	// отдельном `cmd/migrator` (cobra-based, см. internal/apps/migrator).
	// Subcommand-проверка — в switch ниже.

	cfg, err := config.Load(os.Getenv(configPathEnv))
	if err != nil {
		log.Fatalf("config load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config validate: %v", err)
	}

	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "serve":
			// no-op: продолжаем в runServe
		case "migrate":
			log.Fatal("migrations are not handled by this binary — use the kacho-migrator CLI ({up|down|status|create})")
		default:
			log.Fatalf("unknown command %q (this binary only serves the API; migrations live in `kacho-migrator`)", os.Args[1])
		}
	}

	if err := runServe(cfg); err != nil {
		log.Fatal(err)
	}
}

// services — собранный набор бизнес-сервисов (один composition-point вместо
// россыпи локальных переменных в runServe). Заполняется buildServices,
// используется register{Public,Internal}Services. Каждый ресурс представлен
// готовым use-case-handler'ом, а не «толстым» сервисом.
type services struct {
	networkHandler          *networkapp.Handler
	subnetHandler           *subnetapp.Handler
	addressHandler          *addressapp.Handler
	addressAllocate         *addressapp.AllocateUseCase
	addressRefService       *addressref.Service
	routeTableHandler       *routetableapp.Handler
	securityGroupHandler    *sgapp.Handler
	gatewayHandler          *gatewayapp.Handler
	addressPoolHandler      *addresspoolapp.Handler
	networkInternal         *networkinternal.Service
	networkInterfaceHandler *niapp.Handler
}

func runServe(cfg config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// logger.level из конфига уважается (валидность проверена в cfg.Validate()).
	logger := observability.NewSloggerLevel(os.Stdout, cfg.SlogLevel())
	slog.SetDefault(logger)

	// Логируем insecure dev-defaults.
	for _, w := range cfg.InsecureDevWarnings() {
		logger.Warn(w)
	}
	if cfg.AuthN.Mode == config.ModeProduction {
		logger.Warn("authn.mode=production: anonymous callers will be rejected (M5 fail-closed)")
	}
	if cfg.AuthN.Mode == config.ModeProductionStrict {
		logger.Warn("authn.mode=production-strict: anonymous rejected + TLS+SSL strictly validated")
	}
	// Громкое предупреждение, когда authz целиком обойдён в production (emergency).
	if cfg.AuthN.Mode.IsProduction() && cfg.AuthZ.Breakglass {
		logger.Warn(fmt.Sprintf(config.WarnBreakglassProduction, cfg.AuthN.Mode))
	}

	pool, err := coredb.NewPool(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	// Slave-pool (read-replica). Если slave-url настроен и отличается от master URL
	// — отдельный pgxpool для read-TX'ов; иначе slavePool = nil и kachopg.New()
	// делает fallback на master. Код во всех use-case'ах уже разделен на
	// Reader/Writer, так что переключение на реальную реплику — это только wiring.
	var slavePool *pgxpool.Pool
	if slaveDSN := cfg.SlaveDSN(); slaveDSN != "" {
		slavePool, err = coredb.NewPool(ctx, slaveDSN)
		if err != nil {
			return fmt.Errorf("new slave pool: %w", err)
		}
		defer slavePool.Close()
		logger.Info("kacho-vpc CQRS slave-pool enabled (read-replica)",
			"slave_url_masked", maskDSN(cfg.Repository.Postgres.SlaveURL))
	} else {
		logger.Info("kacho-vpc CQRS slave-pool disabled — Reader-TX fallback to master")
	}

	// Schema = `kacho_vpc`. cfg.DSN() уже несет `options=-c search_path=kacho_vpc,public`
	// — unqualified-references из repo-кода резолвятся в kacho_vpc. operations-repo
	// дополнительно передает схему явно для квалификации SQL-операций.
	opsRepo := operations.NewRepo(pool, "kacho_vpc")

	// Prometheus observability adapter: приватный реестр, питает outbox-recorder,
	// reconciler-recorder и diagnostic /metrics. Заменяет in-memory MemRecorder —
	// метрики теперь экспортируются наружу (scrape).
	metricsAdapter := vpcmetrics.New(buildVersion, buildCommit)

	// Per-edge opt-in mTLS-конфиг из env (KACHO_VPC_*). enable=false на ребре →
	// insecure (dev backward-compat). Используется для ребер vpc→iam
	// register-drainer, vpc→geo (zone_id) и для public/internal server-листенеров.
	mtlsCfg, err := config.LoadMTLS()
	if err != nil {
		return fmt.Errorf("load mTLS config: %w", err)
	}
	// Fail-closed boot-гардрейл S2: production-strict требует server-mTLS на обоих
	// листенерах. MTLSConfig грузится вне viper-Config, поэтому проверка — здесь,
	// ДО привязки листенеров (отказ старта вместо insecure-listener'а).
	if err := cfg.ValidateServerMTLS(mtlsCfg); err != nil {
		return fmt.Errorf("config validate (server mTLS): %w", err)
	}

	// Cross-service gRPC dial — через единый builder: retries=3 / dialTimeout=10s /
	// keepalive=30s / TLS / опц. dns:///+round_robin.
	//
	// Ребро vpc→iam ProjectService.Get — клиентский mTLS. При
	// KACHO_VPC_IAM_PROJECT_MTLS_ENABLE=true дилим с corelib client-cert creds
	// (предъявляем kacho-vpc-client-tls, проверяем iam server-cert против internal-CA +
	// ServerName=kacho-iam, fail-closed на плохой тройке); enable=false → insecure/
	// server-auth путь через clients.Build (dev backward-compat). Обязателен, когда
	// kacho-iam требует и проверяет client-cert — иначе TLS-handshake этого dial падает.
	iamPeer := cfg.ExtAPI.IAM
	var iamConn clients.Conn
	if mtlsCfg.IAMProjectMTLS.Enable {
		var icreds grpc.DialOption
		icreds, err = mtlsCfg.IAMProjectClientCreds()
		if err != nil {
			return fmt.Errorf("vpc→iam project mTLS creds: %w", err)
		}
		iamConn, err = grpc.NewClient(iamPeer.Endpoint, icreds, grpcclient.KeepaliveDialOption(false))
	} else {
		iamConn, err = clients.Build(ctx, clients.BuildOptions{
			Endpoint: iamPeer.Endpoint,
			TLS:      iamPeer.TLS.Enable,
			DNSLB:    iamPeer.DNSLB,
		})
	}
	if err != nil {
		return fmt.Errorf("dial iam: %w", err)
	}
	defer iamConn.Close()
	logger.Info("vpc→iam ProjectService.Get edge configured",
		"endpoint", iamPeer.Endpoint, "mtls", mtlsCfg.IAMProjectMTLS.Enable)
	// TTL+LRU кеш: снимает gRPC-hop в kacho-iam из hot-path Network.Create при
	// burst-нагрузке. См. internal/clients/project_cache.go.
	rawProjectClient := clients.NewProjectClient(iamConn)
	projectClient := clients.NewCachedProjectClient(rawProjectClient, clients.ProjectCacheConfig{
		PositiveTTL: cfg.Network.ProjectCache.PositiveTTL,
		NegativeTTL: cfg.Network.ProjectCache.NegativeTTL,
		MaxSize:     cfg.Network.ProjectCache.MaxSize,
	})
	logger.Info("project existence cache enabled",
		"positive_ttl", cfg.Network.ProjectCache.PositiveTTL,
		"negative_ttl", cfg.Network.ProjectCache.NegativeTTL,
		"max_size", cfg.Network.ProjectCache.MaxSize)

	// Geography (Region/Zone) — leaf-домен kacho-geo: VPC валидирует zone_id вызовом
	// geo.v1.ZoneService.Get (Subnet.Create / AddressPool.Create). Когда per-edge
	// mTLS включен (KACHO_VPC_GEO_MTLS_ENABLE=true) — дилим geo с corelib client-cert
	// creds (fail-closed); иначе insecure/one-way-TLS путь через clients.Build (dev
	// backward-compat).
	var geoConn clients.Conn
	if mtlsCfg.GeoMTLS.Enable {
		var gcreds grpc.DialOption
		gcreds, err = mtlsCfg.GeoClientCreds()
		if err != nil {
			return fmt.Errorf("vpc→geo mTLS creds: %w", err)
		}
		geoConn, err = grpc.NewClient(cfg.ExtAPI.Geo.Endpoint, gcreds, grpcclient.KeepaliveDialOption(false))
	} else {
		geoConn, err = clients.Build(ctx, clients.BuildOptions{
			Endpoint: cfg.ExtAPI.Geo.Endpoint,
			TLS:      cfg.ExtAPI.Geo.TLS.Enable,
		})
	}
	if err != nil {
		return fmt.Errorf("dial geo: %w", err)
	}
	defer geoConn.Close()
	geoClient := clients.NewGeoZoneClient(geoConn)

	// authz internal IAM conn: cfg.AuthZ.IAMEndpoint → **internal** listener kacho-iam
	// (:9091), единственный, что обслуживает InternalIAMService.Check. Общий conn для
	// per-RPC authz-gate (ниже) и project-level List authz. Пустой endpoint → nil conn
	// (dev / no-authz: per-RPC gate пропускается, list-filter тоже обязан быть выключен).
	//
	// Ребро vpc→iam Check — клиентский mTLS. Этот единственный authzConn обслуживает
	// и per-RPC gate, и list-filter. При KACHO_VPC_IAM_AUTHZ_MTLS_ENABLE=true дилим с
	// corelib client-cert creds (ServerName=kacho-iam-internal — SAN dial-host'а :9091;
	// fail-closed на плохой тройке); enable=false → insecure/server-auth путь через
	// clients.Build (dev).
	var authzConn clients.Conn
	if cfg.AuthZ.IAMEndpoint != "" {
		if mtlsCfg.IAMAuthzMTLS.Enable {
			var acreds grpc.DialOption
			acreds, err = mtlsCfg.IAMAuthzClientCreds()
			if err != nil {
				return fmt.Errorf("vpc→iam authz mTLS creds: %w", err)
			}
			authzConn, err = grpc.NewClient(cfg.AuthZ.IAMEndpoint, acreds, grpcclient.KeepaliveDialOption(false))
		} else {
			authzConn, err = clients.Build(ctx, clients.BuildOptions{
				Endpoint: cfg.AuthZ.IAMEndpoint,
				TLS:      cfg.AuthZ.IAMTLS.Enable,
			})
		}
		if err != nil {
			return fmt.Errorf("dial kacho-iam (authz): %w", err)
		}
		defer authzConn.Close()
		logger.Info("vpc→iam Check edge configured (per-RPC gate + list-filter)",
			"endpoint", cfg.AuthZ.IAMEndpoint, "mtls", mtlsCfg.IAMAuthzMTLS.Enable)
	}

	// Per-object FGA List/Get filter. Каждый List RPC резолвит доступный caller'у
	// object-set через kacho-iam AuthorizeService.ListObjects, а каждый Get RPC
	// энфорсит per-object no-leak против того же grant-set (read == enforce). Фильтр
	// дилит endpoint AuthorizeService (PUBLIC-проекция, :9090) — НЕ conn
	// InternalIAMService.Check (authzConn → :9091), который AuthorizeService не
	// обслуживает. Endpoint = authz.list-filter.authorize-endpoint или, если не задан,
	// authz.iam-endpoint. nil-фильтр (выключен / нет endpoint) → use-case'ы делают
	// нефильтрованный list, а no-leak Get-enforce пропускается.
	var authorizeConn clients.Conn
	if cfg.AuthZ.ListFilter.Enabled {
		authorizeConn, err = buildAuthorizeConn(ctx, cfg, mtlsCfg, logger)
		if err != nil {
			return err
		}
		if authorizeConn != nil {
			defer authorizeConn.Close()
		}
	}
	listFilter := buildListFilter(cfg, authorizeConn, logger)

	svcs := buildServices(pool, slavePool, projectClient, geoClient, authzfilter.AsPort(listFilter), opsRepo, cfg, logger)

	// Fail-closed boot-gate: при KACHO_VPC_REQUIRE_IAM мутирующий Create отвергается,
	// а readiness = NotReady, пока register-drainer не подключен к IAM. Стартует
	// неподключенным; SetConnected(true) срабатывает ниже, как только dial drainer'а
	// успешен.
	bootGate := bootgate.New(bootgate.Config{RequireIAM: requireIAM(), Service: "kacho-vpc"})
	// Prometheus-backed outbox-recorder: backlog/oldest/poisoned register-outbox
	// экспортируются на /metrics (заменяет in-memory MemRecorder). Тот же adapter
	// — operations.Recorder для reconciler'а ниже.
	outboxRec := metricsAdapter

	// register-drainer: применяет FGA owner-tuple register/unregister intents
	// (kacho_vpc.fga_register_outbox, записанные транзакционно в writer-TX ресурса)
	// через kacho-iam InternalIAMService.RegisterResource по ребру vpc→iam (mTLS
	// opt-in). Default-on: без него созданные ресурсы не получают owner-tuple →
	// per-resource Check DENY. Дилит iam-internal listener :9091 (cfg.AuthZ.IAMEndpoint
	// — RegisterResource Internal-only, ban #6). Пустой endpoint → drainer не стартует
	// (dev / no-iam).
	if registerDrainerEnabled() {
		if cfg.AuthZ.IAMEndpoint == "" {
			logger.Warn("FGA register-drainer NOT started — authz.iam-endpoint unset " +
				"(no kacho-iam internal endpoint to apply register-intents); intents stay durable until configured")
		} else {
			closeDrainer, derr := startRegisterDrainer(ctx, cfg.AuthZ.IAMEndpoint, mtlsCfg, pool, outboxRec, logger)
			if derr != nil {
				return fmt.Errorf("start register-drainer: %w", derr)
			}
			defer closeDrainer()
			// Dial drainer'а установлен → путь доставки IAM-register работает:
			// открываем boot-gate. reconciler + metrics-collector стартуют рядом.
			bootGate.SetConnected(true)
			if berr := startBackstop(ctx, pool, outboxRec, logger); berr != nil {
				return fmt.Errorf("start outbox backstop: %w", berr)
			}
		}
	} else {
		logger.Warn("FGA register-drainer DISABLED (KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED=false) — " +
			"register-intents accumulate in fga_register_outbox unapplied")
	}

	// authz: per-RPC OpenFGA Check на public И internal listener'ах.
	//
	// IAMEndpoint пуст → interceptor НЕ навешивается (graceful start без
	// kacho-iam в dev; production-deploy выставит authz.iam-endpoint в
	// values.yaml). Breakglass=true → interceptor навешивается, но все
	// пропускает + emit'ит WARN-метрику (dev / emergency).
	//
	// internal :9091 listener — С тем же authz-interceptor'ом (security-инвариант:
	// authN+authZ и на internal'е). cluster-scoped admin RPC (InternalNetworkService,
	// InternalAddressPoolService) проходят FGA Check на `cluster:cluster_kacho_root`;
	// IPAM-примитивы InternalAddressService.* — object-scoped verb-bearing Check на
	// самом ресурсе `vpc_address` (v_update/v_get, как публичный AddressService) — все
	// они в PermissionMap, exempt-путей больше нет.
	productionMode := cfg.AuthN.Mode.IsProduction()

	// principal-extract ОБЯЗАН стоять ПЕРВЫМ в public-цепочке: authz-interceptor и
	// use-case'ы, пишущие operations.principal_* колонки, читают principal'а из ctx.
	// UnaryPrincipalExtract достает его из x-kacho-principal-* gRPC metadata (их
	// форвардит api-gateway); без extract'а первым все request'ы получали бы
	// SystemPrincipal()-fallback вместо реального principal'а.
	//
	// Boot-gate guardCreateUnary — ПЕРВЫМ в public-цепочке: мутирующий Create
	// отвергается (UNAVAILABLE), когда KACHO_VPC_REQUIRE_IAM взведен, а
	// register-drainer не подключен к IAM, — чтобы ни один tenant-ресурс не создавался
	// без доставляемого owner-tuple intent. Read RPC не затронуты.
	publicUnary := []grpc.UnaryServerInterceptor{
		fgaboot.GuardCreateUnary(bootGate),
		grpcsrv.UnaryPrincipalExtract(),
		handler.TenantUnaryInterceptor(false, productionMode),
	}
	publicStream := []grpc.StreamServerInterceptor{
		grpcsrv.StreamPrincipalExtract(),
		handler.TenantStreamInterceptor(false, productionMode),
	}

	// internal listener :9091 — с тем же FGA-гейтом, что и public (security-инвариант:
	// authN+authZ и на internal'е тоже). principal-extract СТОИТ ПЕРВЫМ (как в public):
	// authzIntr читает principal'а из ctx (x-kacho-principal-* metadata), а
	// subject-extractor — из него subject для Check'а. TenantUnaryInterceptor(true,...)
	// — admin-metadata gate, сохраняется для defense-in-depth. authzIntr навешивается
	// ниже (когда != nil): mapped cluster-scoped internal RPC
	// (InternalNetworkService.GetNetwork/..., InternalAddressPoolService.*) проходят
	// Check; IPAM InternalAddressService.* — object-scoped verb-bearing Check на
	// `vpc_address` (v_update/v_get), все в PermissionMap.
	internalUnary := []grpc.UnaryServerInterceptor{
		grpcsrv.UnaryPrincipalExtract(),
		handler.TenantUnaryInterceptor(true, productionMode),
	}
	internalStream := []grpc.StreamServerInterceptor{
		grpcsrv.StreamPrincipalExtract(),
		handler.TenantStreamInterceptor(true, productionMode),
	}

	// authzConn (kacho-iam internal :9091, InternalIAMService.Check) собран один раз
	// выше и общий с project-level List authz.
	authzIntr, err := check.NewInterceptor(check.Options{
		ServiceName:         "kacho-vpc",
		IAMConn:             authzConn,
		Breakglass:          cfg.AuthZ.Breakglass,
		Logger:              logger,
		CheckTimeout:        cfg.AuthZ.CheckTimeout,
		DenyRateLimitPerSec: cfg.AuthZ.DenyRateLimitPerSec,
		CacheTTL:            cfg.AuthZ.CacheTTL,
	})
	// Fail-fast (S3): в production отсутствие authz-interceptor'а — фатально (не
	// продолжаем как раньше с Warn). Защита от регрессии, обходящей S1-гард: без
	// Check подделанная x-kacho-* metadata дала бы эскалацию. В dev — graceful
	// Warn+continue.
	authzIntr, err = authzWiringDecision(productionMode, authzIntr, err)
	if err != nil {
		return fmt.Errorf("authz wiring: %w", err)
	}
	if authzIntr != nil {
		publicUnary = append(publicUnary, authzIntr.Unary())
		publicStream = append(publicStream, authzIntr.Stream())
		// Тот же authzIntr instance на internal listener'е (общий cache/метрики).
		internalUnary = append(internalUnary, authzIntr.Unary())
		internalStream = append(internalStream, authzIntr.Stream())
		logger.Info("authz interceptor enabled",
			"iam_endpoint", cfg.AuthZ.IAMEndpoint,
			"breakglass", cfg.AuthZ.Breakglass,
			"cache_ttl", cfg.AuthZ.CacheTTL,
		)
	} else {
		// dev-стенд без kacho-iam — продолжаем без authz-interceptor'а.
		logger.Warn("authz interceptor NOT enabled — authz.iam-endpoint not configured (dev mode)")
	}

	// Per-listener opt-in mTLS server-creds. enable=false (default) → insecure (dev
	// backward-compat); enable=true → RequireAndVerifyClientCert (server-cert +
	// client-CA), fail-closed при отсутствии cert-тройки (без тихого downgrade в
	// insecure).
	publicServerCreds, err := mtlsCfg.PublicServerCreds()
	if err != nil {
		return fmt.Errorf("public listener mTLS creds: %w", err)
	}
	internalServerCreds, err := mtlsCfg.InternalServerCreds()
	if err != nil {
		return fmt.Errorf("internal listener mTLS creds: %w", err)
	}

	grpcSrv := grpcsrv.NewServer(
		publicServerCreds,
		grpc.ChainUnaryInterceptor(publicUnary...),
		grpc.ChainStreamInterceptor(publicStream...),
	)
	internalSrv := grpcsrv.NewServer(
		internalServerCreds,
		grpc.ChainUnaryInterceptor(internalUnary...),
		grpc.ChainStreamInterceptor(internalStream...),
	)
	logger.Info("kacho-vpc listener mTLS",
		"public_mtls", mtlsCfg.PublicServerMTLS.Enable,
		"internal_mtls", mtlsCfg.InternalServerMTLS.Enable)
	registerPublicServices(grpcSrv, svcs, opsRepo)
	registerInternalServices(internalSrv, svcs)

	// Dependency-aware readiness: /readyz отражает здоровье критичных зависимостей
	// (database / register-drainer / lro-worker / iam-authz), /healthz — только
	// живость процесса (защита от restart-storm). Результат зеркалится в
	// dependency_up Prometheus-gauge.
	healthAgg := health.New(
		buildReadinessCheckers(pool, bootGate, authzConn),
		health.WithResultObserver(metricsAdapter.SetDependencyUp),
	)

	// Diagnostic HTTP-listener (cluster-internal): /metrics + /healthz + /readyz.
	// Пустой endpoint (metrics.enable=false) → не поднимается (back-compat).
	diagTask, diagShutdown, err := startDiagnosticListener(cfg.MetricsEndpoint(), metricsAdapter, healthAgg, logger)
	if err != nil {
		return fmt.Errorf("start diagnostic listener: %w", err)
	}

	// Durable LRO recovery: доменный resolver + corelib-reconciler поверх schema
	// kacho_vpc. RecoverAll прогоняется ДО приёма трафика; периодический Run —
	// backstop до отмены ctx.
	startLRORecovery(ctx, pool, kachopg.New(pool, slavePool), metricsAdapter, logger)

	// Явно поднимаем package-level default-registry LRO-worker'а ДО приёма трафика:
	// readiness lro-worker зелёный без единой мутации (нет boot-deadlock), а
	// live-worker метрики (terminal-write retries/failures, inflight gauge) текут в
	// тот же Prometheus-adapter — раньше эти серии были мертвы (NopRecorder).
	if err := startLROWorker(metricsAdapter, logger); err != nil {
		return fmt.Errorf("start LRO worker: %w", err)
	}

	publicAddr := cfg.APIServer.ListenAddress()
	internalAddr := cfg.APIServer.InternalListenAddress()
	listener, err := net.Listen("tcp", publicAddr)
	if err != nil {
		return err
	}
	internalListener, err := net.Listen("tcp", internalAddr)
	if err != nil {
		_ = listener.Close()
		return err
	}
	logger.Info("kacho-vpc listening",
		"public_endpoint", publicAddr,
		"internal_endpoint", internalAddr)

	gracefulTimeout := cfg.APIServer.GracefulShutdown
	if gracefulTimeout <= 0 {
		gracefulTimeout = 10 * time.Second
	}

	// Параллельный запуск public-сервера + internal-сервера + shutdown-waiter через
	// `parallel.ExecAbstract` (`github.com/H-BF/corlib/pkg/parallel`).
	// Failure-isolation: первая ошибка / SIGTERM / SIGINT триггерит graceful-stop
	// ОБОИХ серверов — умерший internal не оставляет public крутиться.
	//
	// `grpc.Server.Serve` не реагирует на ctx-cancel сам — поэтому `triggerShutdown`
	// явно вызывает `GracefulStop` на обоих, после чего `Serve` возвращает
	// `nil`/`grpc.ErrServerStopped` (трактуется как штатное завершение). `sync.Once`
	// гарантирует, что параллельные триггеры (SIGTERM пришел одновременно с crash
	// internal'а) не сделают двойной GracefulStop.
	// shutdownCh закрывается ВНУТРИ triggerShutdown — он будит shutdown-waiter не
	// только по SIGTERM/SIGINT (ctx.Done), но и когда graceful-stop инициирован крашем
	// одного из серверов. Без этого waiter висел бы на `<-ctx.Done()` навечно (ctx —
	// только сигнальный), и `parallel.ExecAbstract` никогда бы не вернулся —
	// процесс-зомби.
	shutdownCh := make(chan struct{})
	var shutdownOnce sync.Once
	triggerShutdown := func() {
		shutdownOnce.Do(func() {
			// Readiness флипает в shutting_down ДО GracefulStop — kubelet перестаёт
			// слать трафик, пока in-flight RPC дренируются.
			healthAgg.SetShuttingDown()
			close(shutdownCh)
			internalSrv.GracefulStop()
			grpcSrv.GracefulStop()
		})
	}

	tasks := []func() error{
		// public gRPC server
		func() error {
			err := grpcSrv.Serve(listener)
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				triggerShutdown()
				return fmt.Errorf("public grpc server: %w", err)
			}
			return nil
		},
		// internal gRPC server (admin / kacho-only)
		func() error {
			err := internalSrv.Serve(internalListener)
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				logger.Error("internal grpc server stopped", "err", err)
				triggerShutdown()
				return fmt.Errorf("internal grpc server: %w", err)
			}
			return nil
		},
		// shutdown waiter: SIGTERM/SIGINT (ctx) ИЛИ краш сервера (shutdownCh) →
		// graceful-stop обоих + дрейн LRO worker'ов. select по обоим каналам, иначе
		// при краше сервера waiter висел бы на ctx навечно.
		func() error {
			select {
			case <-ctx.Done():
			case <-shutdownCh:
			}
			triggerShutdown()
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 3*gracefulTimeout)
			defer cancelDrain()
			if err := operations.Wait(drainCtx); err != nil {
				logger.Warn("operations workers did not finish in time",
					"err", err, "active", operations.Active())
			}
			// Diagnostic-listener гасится последним (после дренажа LRO worker'ов),
			// чтобы probe-flip /readyz→503 успел отработать до закрытия порта.
			diagShutdown(drainCtx)
			return nil
		},
	}
	// Diagnostic HTTP-listener — отдельная task (когда поднят). Краш одного
	// сервера триггерит graceful-stop всех через triggerShutdown/shutdownCh.
	if diagTask != nil {
		tasks = append(tasks, func() error {
			if derr := diagTask(); derr != nil {
				logger.Error("diagnostic listener stopped", "err", derr)
				triggerShutdown()
				return fmt.Errorf("diagnostic listener: %w", derr)
			}
			return nil
		})
	}

	// ExecAbstract(taskCount, maxConcurrency, fn): запускает все задачи
	// параллельно; собирает первую ошибку. maxConcurrency=len(tasks)-1 дает
	// схему «1 + (N-1)» — основная горутина + N-1 дополнительных, все
	// задачи реально параллельны (см. corlib/pkg/parallel/exec-in-parallel.go).
	err = parallel.ExecAbstract(len(tasks), safeconv.IntToInt32(len(tasks)-1), func(i int) error {
		return tasks[i]()
	})
	cancel()
	return err
}

// buildAuthorizeConn — дилит endpoint kacho-iam AuthorizeService для per-object
// List/Get-фильтра. AuthorizeService — это PUBLIC-проекция (:9090), НЕ listener
// InternalIAMService.Check (:9091, его обслуживает authzConn). Endpoint =
// authz.list-filter.authorize-endpoint, с fallback на authz.iam-endpoint, если не
// задан. Пусто → (nil, nil): caller логирует warn, а фильтр деградирует в
// passthrough.
//
// mTLS — opt-in через authz.list-filter.authorize-tls; когда выключен, переиспользует
// тот же vpc→iam authz client-cert, что и ребро Check (IAMAuthzMTLS), чтобы одна
// client-identity покрывала оба ребра. enable=false на обоих → insecure/server-auth
// dev-путь.
func buildAuthorizeConn(ctx context.Context, cfg config.Config, mtlsCfg config.MTLSConfig, logger *slog.Logger) (clients.Conn, error) {
	endpoint := cfg.AuthZ.ListFilter.AuthorizeEndpoint
	if endpoint == "" {
		endpoint = cfg.AuthZ.IAMEndpoint
	}
	if endpoint == "" {
		logger.Warn("authz.list-filter.enabled=true but neither authorize-endpoint nor iam-endpoint set — per-object list-filter disabled")
		return nil, nil
	}
	if cfg.AuthZ.ListFilter.AuthorizeTLS.Enable || mtlsCfg.IAMAuthzMTLS.Enable {
		creds, err := mtlsCfg.IAMAuthzClientCreds()
		if err != nil {
			return nil, fmt.Errorf("vpc→iam authorize mTLS creds: %w", err)
		}
		conn, err := grpc.NewClient(endpoint, creds, grpcclient.KeepaliveDialOption(false))
		if err != nil {
			return nil, fmt.Errorf("dial kacho-iam (authorize/list-filter): %w", err)
		}
		logger.Info("per-object list-filter authorize edge configured", "endpoint", endpoint, "mtls", true)
		return conn, nil
	}
	conn, err := clients.Build(ctx, clients.BuildOptions{
		Endpoint: endpoint,
		TLS:      cfg.AuthZ.ListFilter.AuthorizeTLS.Enable,
	})
	if err != nil {
		return nil, fmt.Errorf("dial kacho-iam (authorize/list-filter): %w", err)
	}
	logger.Info("per-object list-filter authorize edge configured", "endpoint", endpoint, "mtls", false)
	return conn, nil
}

// buildListFilter — возвращает per-object фильтр, готовый питать И фильтрованный
// List, И no-leak Get use-case'ы. Выключен (или нет conn) → nil, который use-case'ы
// трактуют как passthrough (нефильтрованный list + Get no-leak-enforce пропускается;
// per-RPC interceptor все равно гейтит).
func buildListFilter(cfg config.Config, conn clients.Conn, logger *slog.Logger) authzfilter.Filter {
	if !cfg.AuthZ.ListFilter.Enabled {
		logger.Info("per-object list-filter disabled (authz.list-filter.enabled=false)")
		return nil
	}
	if conn == nil {
		logger.Warn("per-object list-filter enabled but authorize conn is nil — disabled (passthrough)")
		return nil
	}
	cli := clients.NewIAMAuthorizeClient(conn)
	f := authzfilter.NewFGAFilter(cli, authzfilter.Config{
		Enabled:         true,
		Timeout:         time.Duration(cfg.AuthZ.ListFilter.TimeoutMs) * time.Millisecond,
		CacheTTL:        cfg.AuthZ.ListFilter.CacheTTL,
		CacheMaxEntries: cfg.AuthZ.ListFilter.MaxEntries,
		MaxResults:      cfg.AuthZ.ListFilter.MaxResults,
		FailOpen:        cfg.AuthZ.ListFilter.FailOpen,
	})
	logger.Info("per-object list-filter enabled",
		"timeout_ms", cfg.AuthZ.ListFilter.TimeoutMs,
		"cache_ttl", cfg.AuthZ.ListFilter.CacheTTL,
		"max_entries", cfg.AuthZ.ListFilter.MaxEntries,
		"max_results", cfg.AuthZ.ListFilter.MaxResults,
		"fail_open", cfg.AuthZ.ListFilter.FailOpen,
	)
	return f
}

// registerDrainerEnabled — register-drainer default-on (без него созданные ресурсы
// не получат owner-tuple). Отключается только явным
// KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED=false.
func registerDrainerEnabled() bool {
	if v, ok := os.LookupEnv("KACHO_VPC_FGA_REGISTER_DRAINER_ENABLED"); ok {
		return v == "true" || v == "1"
	}
	return true
}

// startRegisterDrainer — дилит internal endpoint kacho-iam по ребру vpc→iam (mTLS
// opt-in через mtlsCfg.IAMRegisterClientCreds — enable=false → insecure dev) и
// запускает corelib outbox/drainer поверх kacho_vpc.fga_register_outbox. Каждый
// pending intent переигрывается через InternalIAMService.RegisterResource /
// UnregisterResource (idempotent; Unavailable → retry с backoff; InvalidArgument →
// poison). Run-loop drainer'а владеет claim-CAS для exactly-once между репликами.
// Возвращает closer, закрывающий dial-conn.
//
// iamAddr — listener iam-internal :9091; RegisterResource Internal-only (ban #6).
func startRegisterDrainer(ctx context.Context, iamAddr string, mtlsCfg config.MTLSConfig, pool *pgxpool.Pool, rec metrics.Recorder, logger *slog.Logger) (func(), error) {
	creds, err := mtlsCfg.IAMRegisterClientCreds()
	if err != nil {
		return nil, fmt.Errorf("vpc→iam register mTLS creds: %w", err)
	}
	// idle-склонное ребро (drainer почти все время ждет NOTIFY) → keepalive idle pings.
	conn, err := grpc.NewClient(iamAddr, creds, grpcclient.KeepaliveDialOption(true))
	if err != nil {
		return nil, fmt.Errorf("dial kacho-iam (register-drainer): %w", err)
	}

	iamClient := iamv1.NewInternalIAMServiceClient(conn)
	d, err := drainer.New[clients.FGARegisterPayload](
		pool,
		drainer.Config{
			Table:   fgaRegisterOutboxTable,
			Channel: fgaRegisterOutboxChannel,
		},
		clients.DecodeFGARegisterPayload,
		clients.NewIAMRegisterApplier(iamClient),
		logger.With(slog.String("component", "fga-register-drainer")),
		// Каждая отравленная строка инкрементит outbox_poisoned_total{table=…},
		// чтобы потерянная доставка owner-tuple была alertable, а не тихим Warn.
		drainer.WithPoisonObserver[clients.FGARegisterPayload](func() {
			rec.IncPoisoned(fgaRegisterOutboxTable)
		}),
	)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("build register-drainer: %w", err)
	}

	go func() {
		if rerr := d.Run(ctx); rerr != nil {
			logger.Error("register-drainer stopped", "err", rerr)
		}
	}()
	logger.Info("FGA register-drainer started",
		"iam_addr", iamAddr, "mtls", mtlsCfg.IAMRegisterMTLS.Enable)

	return func() { _ = conn.Close() }, nil
}

// buildServices создает все repo'ы поверх pool и собирает из них бизнес-сервисы.
// При network.default-sg-inline=false sgRepo не передается в Network.Create —
// default SG не создается inline.
//
// slavePool — опц. read-replica pool; nil → kachopg.New делает fallback и Reader-TX
// идут на master.
func buildServices(pool, slavePool *pgxpool.Pool, projectClient repo.ProjectClient, geoClient repo.ZoneRegistry, listFilter authzfilter.UseCasePort, opsRepo operations.Repo, cfg config.Config, logger *slog.Logger) *services {
	if !cfg.Network.DefaultSGInline {
		logger.Warn("network.default-sg-inline=false — Network.Create НЕ создает default SG")
	}

	// Прямой write-side FGA убран: каждый Create/Delete ресурса эмитит FGA
	// owner-tuple register/unregister INTENT в своей writer-TX (один commit, без
	// dual-write); register-drainer применяет каждый intent через kacho-iam
	// InternalIAMService.RegisterResource по mTLS. В use-case'ах больше нет
	// fgaTupleWriter / OpenFGAWriteClient.

	// Все VPC-ресурсы (Network/Subnet/Address/RouteTable/SecurityGroup/Gateway/
	// NetworkInterface) работают через `kacho.Repository` (Reader/Writer split).
	// pgxpool-impl — `internal/repo/kacho/pg`. Admin-сервисы и peer-port'ы
	// use-case-пакетов получают тонкие adapter'ы поверх kachoRepo из пакета
	// `internal/repo/cqrsadapter`.
	kachoRepo := kachopg.New(pool, slavePool)

	// Adapter'ы под узкие port-интерфейсы admin/peer-сервисов. Каждый adapter
	// открывает свежую Reader/Writer-TX на каждый вызов (read на slave-pool, если он
	// настроен; write — на master).
	networkAdapter := cqrsadapter.NewNetwork(kachoRepo)
	subnetAdapter := cqrsadapter.NewSubnet(kachoRepo)
	addressAdapter := cqrsadapter.NewAddress(kachoRepo)
	routeTableAdapter := cqrsadapter.NewRouteTable(kachoRepo)
	sgAdapter := cqrsadapter.NewSecurityGroup(kachoRepo)
	niAdapter := cqrsadapter.NewNetworkInterface(kachoRepo)

	// AddressPool — admin-only use-case-структура (см.
	// `internal/apps/kacho/api/addresspool/`). Composition root собирает use-case'ы +
	// ResolverService под единый Handler. Все use-case'ы работают через `kachoRepo`
	// (CQRS-Repository) — каждый mutate открывает писатель, делает DML + outbox emit в
	// одной TX. Узкие read-port'ы Address/Subnet/Network удовлетворяются adapter'ами
	// поверх kachoRepo (cqrsadapter.Address / Subnet / Network).
	addressPoolResolver := addresspoolapp.NewResolverService(
		kachoRepo, addressAdapter, subnetAdapter,
	)
	addressPoolHandler := addresspoolapp.NewHandler(
		addresspoolapp.NewCreateAddressPoolUseCase(kachoRepo, geoClient),
		addresspoolapp.NewUpdateAddressPoolUseCase(kachoRepo),
		addresspoolapp.NewDeleteAddressPoolUseCase(kachoRepo),
		addresspoolapp.NewGetAddressPoolUseCase(kachoRepo),
		addresspoolapp.NewListAddressPoolsUseCase(kachoRepo),
		addresspoolapp.NewBindAsNetworkDefaultUseCase(kachoRepo, networkAdapter),
		addresspoolapp.NewUnbindNetworkDefaultUseCase(kachoRepo),
		addresspoolapp.NewGetPoolUtilizationUseCase(kachoRepo),
		addresspoolapp.NewListPoolAddressesUseCase(kachoRepo),
		addresspoolapp.NewAddCidrBlocksUseCase(kachoRepo),
		addresspoolapp.NewRemoveCidrBlocksUseCase(kachoRepo),
	)

	addressRefSvc := addressref.NewService(addressAdapter)

	// Network — use-case-структура. Все use-case'ы работают через kachoRepo (CQRS);
	// checkNetworkEmpty / default-SG cleanup в Network.Delete получают subnet/RT/SG
	// adapter'ы, отделенные от writer-TX (каждый открывает свою TX).
	// defaultSGInline=true (default) — при Network.Create в одной writer-TX создается
	// inline default SG и Network.default_security_group_id заполняется атомарно.
	netCreateUC := networkapp.NewCreateNetworkUseCase(kachoRepo, projectClient, opsRepo, cfg.Network.DefaultSGInline).
		WithLogger(logger)
	netUpdateUC := networkapp.NewUpdateNetworkUseCase(kachoRepo, opsRepo)
	netDeleteUC := networkapp.NewDeleteNetworkUseCase(kachoRepo, subnetAdapter, routeTableAdapter, sgAdapter, opsRepo)
	// Per-object FGA-фильтр (listFilter) питает И no-leak Get, И фильтрованный List.
	// listFilter == nil → passthrough (выключен / dev).
	netGetUC := networkapp.NewGetNetworkUseCase(kachoRepo, listFilter)
	netListUC := networkapp.NewListNetworksUseCase(kachoRepo, listFilter)
	netListSubUC := networkapp.NewListSubnetsUseCase(kachoRepo, subnetAdapter)
	netListSGUC := networkapp.NewListSecurityGroupsUseCase(kachoRepo, sgAdapter)
	netListRTUC := networkapp.NewListRouteTablesUseCase(kachoRepo, routeTableAdapter)
	netListOpsUC := networkapp.NewListOperationsUseCase(opsRepo)
	netHandler := networkapp.NewHandler(
		netCreateUC, netUpdateUC, netDeleteUC,
		netGetUC, netListUC, netListSubUC, netListSGUC, netListRTUC, netListOpsUC,
	)

	// Gateway use-case'ы работают через CQRS-Repository (kachoRepo) — конструктор
	// принимает Repository, каждый use-case открывает Reader/Writer внутри.
	gwHandler := gatewayapp.NewHandler(
		gatewayapp.NewCreateGatewayUseCase(kachoRepo, projectClient, opsRepo),
		gatewayapp.NewUpdateGatewayUseCase(kachoRepo, opsRepo),
		gatewayapp.NewDeleteGatewayUseCase(kachoRepo, opsRepo),
		gatewayapp.NewGetGatewayUseCase(kachoRepo, listFilter),
		gatewayapp.NewListGatewaysUseCase(kachoRepo, listFilter),
		gatewayapp.NewListOperationsUseCase(opsRepo),
	)

	// RouteTable use-case'ы работают через CQRS-Repository. routeTableAdapter
	// передается Network.Delete для child-check.
	rtHandler := routetableapp.NewHandler(
		routetableapp.NewCreateRouteTableUseCase(kachoRepo, projectClient, opsRepo),
		routetableapp.NewUpdateRouteTableUseCase(kachoRepo, opsRepo),
		routetableapp.NewDeleteRouteTableUseCase(kachoRepo, opsRepo),
		routetableapp.NewGetRouteTableUseCase(kachoRepo, listFilter),
		routetableapp.NewListRouteTablesUseCase(kachoRepo, listFilter),
		routetableapp.NewListOperationsUseCase(opsRepo),
	)

	// Subnet use-case'ы работают через CQRS-Repository (kachoRepo). niAdapter
	// передается в Delete для precondition-check «нет привязанных NIC».
	subnetHandler := subnetapp.NewHandler(
		subnetapp.NewCreateSubnetUseCase(kachoRepo, projectClient, geoClient, opsRepo),
		subnetapp.NewUpdateSubnetUseCase(kachoRepo, opsRepo),
		subnetapp.NewDeleteSubnetUseCase(kachoRepo, niAdapter, opsRepo),
		subnetapp.NewGetSubnetUseCase(kachoRepo, listFilter),
		subnetapp.NewListSubnetsUseCase(kachoRepo, listFilter),
		subnetapp.NewAddCidrBlocksUseCase(kachoRepo, opsRepo),
		subnetapp.NewRemoveCidrBlocksUseCase(kachoRepo, opsRepo),
		subnetapp.NewListUsedAddressesUseCase(kachoRepo, addressAdapter),
		subnetapp.NewListOperationsUseCase(opsRepo),
	)

	// Address — use-case-структура. Composition с AddressPoolService для IPAM cascade
	// resolve. Internal Allocate UC отделен — принимается
	// InternalAddressAllocateHandler через узкий port.
	//
	// Все Address use-cases работают через CQRS-Repository (`kachoRepo`). IPAM
	// atomicity (Insert + Allocate + Outbox) гарантируется одной writer-TX в
	// `CreateAddressUseCase.doCreate` / `AllocateUseCase.*`. subnetAdapter — peer-port
	// для SubnetReader (Get + AddressesBySubnet), удовлетворяется тем же kachoRepo
	// через cqrsadapter.
	addressCreateUC := addressapp.NewCreateAddressUseCase(kachoRepo, subnetAdapter, projectClient, opsRepo, addressPoolResolver)
	addressUpdateUC := addressapp.NewUpdateAddressUseCase(kachoRepo, opsRepo)
	addressDeleteUC := addressapp.NewDeleteAddressUseCase(kachoRepo, opsRepo)
	addressGetUC := addressapp.NewGetAddressUseCase(kachoRepo, listFilter)
	addressGetByValueUC := addressapp.NewGetByValueUseCase(kachoRepo)
	addressListUC := addressapp.NewListAddressesUseCase(kachoRepo, listFilter)
	addressListBySubnetUC := addressapp.NewListBySubnetUseCase(kachoRepo, subnetAdapter)
	addressListOpsUC := addressapp.NewListOperationsUseCase(opsRepo)
	addressAllocateUC := addressapp.NewAllocateUseCase(kachoRepo, subnetAdapter, addressPoolResolver)
	addressHandler := addressapp.NewHandler(
		addressCreateUC, addressUpdateUC, addressDeleteUC,
		addressGetUC, addressGetByValueUC, addressListUC, addressListBySubnetUC, addressListOpsUC,
		// SubnetAuthZ: handler-level ownership pre-check для ListBySubnet
		// (defense-in-depth поверх per-RPC authz-interceptor).
		subnetAdapter,
	)

	// SecurityGroup — use-case-структура. Split-endpoint Update / UpdateRules /
	// UpdateRule (OCC через xmin в repo). Все DML + outbox-emit идут в одной writer-TX.
	// sgAdapter (cqrsadapter поверх kachoRepo) передается в Network use-case'ы для
	// checkNetworkEmpty / default-SG cleanup при Network.Delete (отдельная TX от
	// Network writer'а).
	sgHandler := sgapp.NewHandler(
		sgapp.NewCreateSecurityGroupUseCase(kachoRepo, networkAdapter, projectClient, opsRepo).
			WithSGReader(sgAdapter),
		sgapp.NewUpdateSecurityGroupUseCase(kachoRepo, opsRepo).WithSGReader(sgAdapter),
		// sgAdapter (SecurityGroupReader) — same-network-валидация SG-target-правил
		// на UpdateRules/UpdateRule.
		sgapp.NewUpdateRulesUseCase(kachoRepo, opsRepo, sgAdapter),
		sgapp.NewUpdateRuleUseCase(kachoRepo, opsRepo, sgAdapter),
		sgapp.NewDeleteSecurityGroupUseCase(kachoRepo, opsRepo),
		sgapp.NewGetSecurityGroupUseCase(kachoRepo, listFilter),
		sgapp.NewListSecurityGroupsUseCase(kachoRepo, listFilter),
		sgapp.NewListOperationsUseCase(kachoRepo, opsRepo),
	)

	// NetworkInterface — use-case-структура. Все use-case'ы работают через
	// CQRS-Repository (`kachoRepo`). У NIC нет Move RPC (NIC привязан к Subnet).
	// addressAdapter передается в Create/Update/Delete UC — он удовлетворяет
	// `networkinterface.AddressRepo` port (Get + SetReference + ClearReference).
	niHandler := niapp.NewHandler(
		niapp.NewCreateNetworkInterfaceUseCase(kachoRepo, addressAdapter, projectClient, opsRepo),
		niapp.NewUpdateNetworkInterfaceUseCase(kachoRepo, addressAdapter, opsRepo),
		niapp.NewDeleteNetworkInterfaceUseCase(kachoRepo, opsRepo),
		niapp.NewGetNetworkInterfaceUseCase(kachoRepo, listFilter),
		niapp.NewListNetworkInterfacesUseCase(kachoRepo, listFilter),
		niapp.NewListOperationsUseCase(opsRepo),
	)

	return &services{
		networkHandler:          netHandler,
		subnetHandler:           subnetHandler,
		addressHandler:          addressHandler,
		addressAllocate:         addressAllocateUC,
		addressRefService:       addressRefSvc,
		routeTableHandler:       rtHandler,
		securityGroupHandler:    sgHandler,
		gatewayHandler:          gwHandler,
		addressPoolHandler:      addressPoolHandler,
		networkInternal:         networkinternal.NewService(networkAdapter, sgAdapter),
		networkInterfaceHandler: niHandler,
	}
}

// registerPublicServices — публичные RPC + OperationService на внешний listener.
func registerPublicServices(srv *grpc.Server, svcs *services, opsRepo operations.Repo) {
	vpcv1.RegisterNetworkServiceServer(srv, svcs.networkHandler)
	vpcv1.RegisterSubnetServiceServer(srv, svcs.subnetHandler)
	vpcv1.RegisterAddressServiceServer(srv, svcs.addressHandler)
	vpcv1.RegisterRouteTableServiceServer(srv, svcs.routeTableHandler)
	vpcv1.RegisterSecurityGroupServiceServer(srv, svcs.securityGroupHandler)
	vpcv1.RegisterGatewayServiceServer(srv, svcs.gatewayHandler)
	vpcv1.RegisterNetworkInterfaceServiceServer(srv, svcs.networkInterfaceHandler)
	operationpb.RegisterOperationServiceServer(srv, handler.NewOperationHandler(opsRepo))
}

// registerInternalServices — kacho-only/admin RPC на internal listener.
func registerInternalServices(srv *grpc.Server, svcs *services) {
	vpcv1.RegisterInternalAddressServiceServer(srv, handler.NewInternalAddressAllocateHandler(svcs.addressAllocate, svcs.addressRefService))
	vpcv1.RegisterInternalAddressPoolServiceServer(srv, svcs.addressPoolHandler)
	vpcv1.RegisterInternalNetworkServiceServer(srv, handler.NewInternalNetworkHandler(svcs.networkInternal))
}

// maskDSN отдает DSN с замаскированным паролем — для безопасного логирования
// slave-URL. Возвращает оригинальную строку, если она не парсится как URL.
// Если password не найден, ничего не меняет (DSN без пароля — нормальная
// dev-конфигурация sslmode=disable).
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, hasPwd := u.User.Password(); !hasPwd {
		return dsn
	}
	u.User = url.UserPassword(u.User.Username(), "***")
	return u.String()
}
