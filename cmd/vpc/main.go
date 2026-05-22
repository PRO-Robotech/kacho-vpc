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

	"github.com/PRO-Robotech/kacho-corelib/authz"
	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/observability"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-corelib/safeconv"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"

	addressapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	addresspoolapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	gatewayapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/gateway"
	networkapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/network"
	niapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/networkinterface"
	peapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/privateendpoint"
	routetableapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/routetable"
	sgapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/securitygroup"
	subnetapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/subnet"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgawrite"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addressref"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// configPathEnv — путь к YAML-конфигу. Пустое значение допустимо (defaults +
// ENV-override). Helm chart выставляет KACHO_VPC_CONFIG_PATH=/etc/kacho-vpc/config.yaml.
const configPathEnv = "KACHO_VPC_CONFIG_PATH"

func main() {
	// kacho-vpc — single-purpose binary (skill evgeniy §9 K.1, AP-9). До KAC-96
	// subcommand-mux `serve | migrate ...` — миграции вынесены в отдельный
	// `cmd/migrator` (cobra-based, см. internal/apps/migrator). Subcommand
	// проверка ниже в switch case.

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
			log.Fatal("`kacho-vpc migrate ...` removed in KAC-96 — use the separate binary `kacho-migrator {up|down|status|create}`")
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
// используется register{Public,Internal}Services.
//
// Wave 3a pilot (KAC-94, skill evgeniy §2 B.1-B.4): Network переехал на
// use-case-структуру — здесь хранится готовый `*networkapp.Handler`, а не
// «толстый» NetworkService. Wave 3b — replicate на оставшиеся 7 ресурсов.
type services struct {
	networkHandler          *networkapp.Handler
	subnetHandler           *subnetapp.Handler
	addressHandler          *addressapp.Handler
	addressAllocate         *addressapp.AllocateUseCase
	addressRefService       *addressref.Service
	routeTableHandler       *routetableapp.Handler
	securityGroupHandler    *sgapp.Handler
	gatewayHandler          *gatewayapp.Handler
	privateEndpointHandler  *peapp.Handler
	addressPoolHandler      *addresspoolapp.Handler
	cloudSelSet             *addresspoolapp.SetCloudPoolSelectorUseCase
	cloudSelUnset           *addresspoolapp.UnsetCloudPoolSelectorUseCase
	cloudSelGet             *addresspoolapp.GetCloudPoolSelectorUseCase
	networkInternal         *networkinternal.Service
	networkInterfaceHandler *niapp.Handler
}

// runServe — composition root: поднимает gRPC-серверы VPC, wiring всех слоёв,
// и блокируется до SIGTERM/SIGINT с graceful shutdown.
func runServe(cfg config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	logger := observability.NewSlogger(os.Stdout)
	slog.SetDefault(logger)

	// Логируем insecure dev-defaults (раньше — в validateAuthMode).
	for _, w := range cfg.InsecureDevWarnings() {
		logger.Warn(w)
	}
	if cfg.AuthN.Mode == config.ModeProduction {
		logger.Warn("authn.mode=production: anonymous callers will be rejected (M5 fail-closed)")
	}
	if cfg.AuthN.Mode == config.ModeProductionStrict {
		logger.Warn("authn.mode=production-strict: anonymous rejected + TLS+SSL strictly validated")
	}

	pool, err := coredb.NewPool(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	// Skill evgeniy §6 G.4 — slave-pool wiring (read-replica). Если slave-url
	// настроен и отличается от master URL — отдельный pgxpool для read-TX'ов;
	// иначе slavePool = nil и kachopg.New() сделает fallback на master.
	// Это структурный задел: код во всех use-case'ах уже разделён на Reader/
	// Writer, переключение на реальную реплику — wiring-only.
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

	// Schema = `kacho_vpc` после KAC-94 (миграция 0034 перенесла все VPC-таблицы
	// из `public`). cfg.DSN() уже несёт `options=-c search_path=kacho_vpc,public`
	// — unqualified-references из repo-кода резолвятся в kacho_vpc. operations-repo
	// дополнительно передаёт схему явно для квалификации SQL-операций.
	opsRepo := operations.NewRepo(pool, "kacho_vpc")

	// Cross-service gRPC dial — через единый builder (KAC-97, skill evgeniy §9 K.6):
	// retries=3 / dialTimeout=10s / keepalive=30s / TLS / опц. dns:///+round_robin (KAC-39).
	// KAC-106 (E1): peer switched from kacho-resource-manager to kacho-iam.
	// KAC-127: legacy `extapi.resource-manager` fallback удалён.
	iamPeer := cfg.ExtAPI.IAM
	iamConn, err := clients.Build(ctx, clients.BuildOptions{
		Endpoint: iamPeer.Endpoint,
		TLS:      iamPeer.TLS.Enable,
		DNSLB:    iamPeer.DNSLB,
	})
	if err != nil {
		return fmt.Errorf("dial iam: %w", err)
	}
	defer iamConn.Close()
	// TTL+LRU кеш (KAC-39, KAC-106): снимает gRPC-hop в kacho-iam из hot-path
	// Network.Create при burst-нагрузке (10k RPS). См. internal/clients/project_cache.go.
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

	// Geography (Region/Zone) — домен kacho-compute (эпик KAC-15): VPC валидирует
	// zone_id вызовом compute.v1.ZoneService.Get. KAC-97: через clients.Build.
	computeConn, err := clients.Build(ctx, clients.BuildOptions{
		Endpoint: cfg.ExtAPI.Compute.Endpoint,
		TLS:      cfg.ExtAPI.Compute.TLS.Enable,
	})
	if err != nil {
		return fmt.Errorf("dial compute: %w", err)
	}
	defer computeConn.Close()
	geoClient := clients.NewComputeGeographyClient(computeConn)

	// KAC-127 Phase 4 — FGA-filtered List handlers.
	//
	// Композиция: gRPC-conn к kacho-iam **public** AuthorizeService → corelib
	// authz.ListObjectsService (cache 5s LRU + LISTEN-invalidate) → узкий
	// adapter `listauthz.Adapter` → каждый list-use-case.
	//
	// Если list-filter disabled — listAuthz остаётся nil; use-case'ы получают
	// nil-authz и идут по legacy unfiltered path.
	var listAuthz *listauthz.Adapter
	if cfg.AuthZ.ListFilter.Enabled {
		// Endpoint resolution: явный authorize-endpoint > fallback на authz.iam-endpoint
		// (для compat с existing values.yaml). Public AuthorizeService обычно
		// на :9090 (internal на :9091). Если оба пусты — fail-closed, не enable'им.
		endpoint := cfg.AuthZ.ListFilter.AuthorizeEndpoint
		if endpoint == "" {
			endpoint = cfg.AuthZ.IAMEndpoint
		}
		if endpoint == "" {
			logger.Warn("authz.list-filter.enabled=true но authorize-endpoint и iam-endpoint пусты — list-filter отключён")
		} else {
			listAuthzConn, err := clients.Build(ctx, clients.BuildOptions{
				Endpoint: endpoint,
				TLS:      cfg.AuthZ.ListFilter.AuthorizeTLS.Enable,
			})
			if err != nil {
				return fmt.Errorf("dial kacho-iam authorize-endpoint (list-filter): %w", err)
			}
			defer listAuthzConn.Close()

			listObjectsClient := clients.NewIAMListObjectsClient(listAuthzConn)
			listObjectsSvc := authz.NewListObjectsService(listObjectsClient, authz.ListObjectsConfig{
				TTL:             cfg.AuthZ.ListFilter.CacheTTL,
				MaxEntries:      cfg.AuthZ.ListFilter.MaxEntries,
				MaxResults:      safeconv.IntToUint32(cfg.AuthZ.ListFilter.MaxResults),
				FollowupTimeout: time.Duration(cfg.AuthZ.ListFilter.TimeoutMs) * time.Millisecond,
				AuthzModelID:    cfg.AuthZ.ListFilter.ModelID,
				ServiceName:     "kacho-vpc",
			})
			listAuthz = listauthz.New(listObjectsSvc)
			logger.Info("KAC-127 Phase 4 list-filter enabled",
				"endpoint", endpoint,
				"cache_ttl", cfg.AuthZ.ListFilter.CacheTTL,
				"timeout_ms", cfg.AuthZ.ListFilter.TimeoutMs,
				"max_results", cfg.AuthZ.ListFilter.MaxResults,
				"model_id", cfg.AuthZ.ListFilter.ModelID,
				"fail_open", cfg.AuthZ.ListFilter.FailOpen,
			)
			if cfg.AuthZ.ListFilter.FailOpen {
				logger.Warn("authz.list-filter.fail-open=true — FGA errors fallback to unfiltered list; raises Critical alert in production")
			}
		}
	}

	svcs := buildServices(pool, slavePool, projectClient, geoClient, listAuthz, opsRepo, cfg, logger)

	// authz (E3 / KAC-108): per-RPC OpenFGA Check на public listener'е.
	//
	// IAMEndpoint пуст → interceptor НЕ навешивается (graceful start без
	// kacho-iam в dev; production-deploy выставит authz.iam-endpoint в
	// values.yaml). Breakglass=true → interceptor навешивается, но всё
	// пропускает + emit'ит WARN-метрику (dev / emergency).
	//
	// internal :9091 listener — БЕЗ authz-interceptor'а: на нём admin/RC-only
	// сервисы (запрет #6), которые либо ходят cluster-internal cross-service
	// (sidecar / impl-controller), либо проксируются api-gateway'ем на
	// admin-UI (там auth-z делается на api-gw слое).
	productionMode := cfg.AuthN.Mode.IsProduction()

	// KAC-127 bug #104: principal-extract ОБЯЗАН стоять ПЕРВЫМ в public-цепочке —
	// раньше authz-interceptor вызывал operations.PrincipalFromContext(ctx) без
	// предварительного UnaryPrincipalExtract, и для КАЖДОГО request'а получал
	// SystemPrincipal() fallback ("user:bootstrap") вместо реального principal'а,
	// который api-gateway форвардит через x-kacho-principal-* gRPC metadata.
	// UnaryPrincipalExtract читает эти metadata-headers и кладёт реальный
	// operations.Principal в ctx → authz-interceptor (и use-case'ы, пишущие
	// operations.principal_* колонки) видят верного principal'а.
	publicUnary := []grpc.UnaryServerInterceptor{
		grpcsrv.UnaryPrincipalExtract(),
		handler.TenantUnaryInterceptor(false, productionMode),
	}
	publicStream := []grpc.StreamServerInterceptor{
		grpcsrv.StreamPrincipalExtract(),
		handler.TenantStreamInterceptor(false, productionMode),
	}

	var authzConn clients.Conn
	if cfg.AuthZ.IAMEndpoint != "" {
		authzConn, err = clients.Build(ctx, clients.BuildOptions{
			Endpoint: cfg.AuthZ.IAMEndpoint,
			TLS:      cfg.AuthZ.IAMTLS.Enable,
		})
		if err != nil {
			return fmt.Errorf("dial kacho-iam (authz): %w", err)
		}
		defer authzConn.Close()
	}
	authzIntr, err := check.NewInterceptor(check.Options{
		ServiceName:         "kacho-vpc",
		IAMConn:             authzConn,
		Breakglass:          cfg.AuthZ.Breakglass,
		Logger:              logger,
		CheckTimeout:        cfg.AuthZ.CheckTimeout,
		DenyRateLimitPerSec: cfg.AuthZ.DenyRateLimitPerSec,
		CacheTTL:            cfg.AuthZ.CacheTTL,
	})
	switch {
	case err == nil && authzIntr != nil:
		publicUnary = append(publicUnary, authzIntr.Unary())
		publicStream = append(publicStream, authzIntr.Stream())
		logger.Info("authz interceptor enabled",
			"iam_endpoint", cfg.AuthZ.IAMEndpoint,
			"breakglass", cfg.AuthZ.Breakglass,
			"cache_ttl", cfg.AuthZ.CacheTTL,
		)
	case errors.Is(err, check.ErrIAMConnNotConfigured):
		// Dev-стенд без kacho-iam — продолжаем без authz-interceptor'а
		// (scope-guard KAC-108). В production cfg.AuthZ.IAMEndpoint обязан
		// быть выставлен — это проверяется на стороне deploy/values.
		logger.Warn("authz interceptor NOT enabled — authz.iam-endpoint not configured (dev mode)")
	case err != nil:
		return fmt.Errorf("build authz interceptor: %w", err)
	}

	grpcSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(publicUnary...),
		grpc.ChainStreamInterceptor(publicStream...),
	)
	internalSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(handler.TenantUnaryInterceptor(true, productionMode)),
		grpc.ChainStreamInterceptor(handler.TenantStreamInterceptor(true, productionMode)),
	)
	registerPublicServices(grpcSrv, svcs, opsRepo)
	registerInternalServices(internalSrv, svcs, pool, cfg.MigrateDSN(), logger, cfg.Watch.MaxStreams)

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

	// K.4 + K.5 + AP-7 (skill evgeniy §9, §11): параллельный запуск
	// public-сервера + internal-сервера + shutdown-waiter через
	// `parallel.ExecAbstract` (`github.com/H-BF/corlib/pkg/parallel`).
	// Failure-isolation: первая ошибка / SIGTERM / SIGINT триггерит
	// graceful-stop ОБОИХ серверов (раньше — bare `go func() { Serve }()`
	// без error-prop: умерший internal оставлял public крутиться).
	//
	// `grpc.Server.Serve` не реагирует на ctx-cancel сам — поэтому
	// `triggerShutdown` явно вызывает `GracefulStop` на обоих, после чего
	// `Serve` возвращает `nil`/`grpc.ErrServerStopped` (трактуется как
	// штатное завершение). `sync.Once` гарантирует, что параллельные
	// триггеры (SIGTERM пришёл одновременно с crash internal'а) не
	// сделают двойной GracefulStop.
	var shutdownOnce sync.Once
	triggerShutdown := func() {
		shutdownOnce.Do(func() {
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
		// shutdown waiter: SIGTERM/SIGINT → graceful-stop обоих + дрейн LRO worker'ов.
		func() error {
			<-ctx.Done()
			triggerShutdown()
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 3*gracefulTimeout)
			defer cancelDrain()
			if err := operations.Wait(drainCtx); err != nil {
				logger.Warn("operations workers did not finish in time",
					"err", err, "active", operations.Active())
			}
			return nil
		},
	}

	// ExecAbstract(taskCount, maxConcurrency, fn): запускает все задачи
	// параллельно; собирает первую ошибку. maxConcurrency=len(tasks)-1 даёт
	// схему «1 + (N-1)» — основная горутина + N-1 дополнительных, все
	// задачи реально параллельны (см. corlib/pkg/parallel/exec-in-parallel.go).
	err = parallel.ExecAbstract(len(tasks), safeconv.IntToInt32(len(tasks)-1), func(i int) error {
		return tasks[i]()
	})
	cancel()
	return err
}

// KAC-97: dialResourceManager / dialCompute / peerCreds удалены — заменены на
// единый clients.Build (internal/clients/builder.go). validateAuthMode из KAC-95
// перенесён в config.Validate() / config.InsecureDevWarnings().

// buildServices создаёт все repo'ы поверх pool и собирает из них бизнес-сервисы.
// defaultSGRepo: nil при network.default-sg-inline=false → Network.Create не создаёт
// inline default SG.
//
// slavePool — опц. read-replica pool (skill evgeniy §6 G.4); nil → kachopg.New
// делает fallback и Reader-TX идут на master.
func buildServices(pool, slavePool *pgxpool.Pool, projectClient repo.ProjectClient, geoClient repo.ZoneRegistry, listAuthz *listauthz.Adapter, opsRepo operations.Repo, cfg config.Config, logger *slog.Logger) *services {
	if !cfg.Network.DefaultSGInline {
		logger.Warn("network.default-sg-inline=false — Network.Create НЕ создаёт default SG")
	}

	// KAC-127 issue #22: write-side FGA. fgaTupleWriter — nil unless
	// authz.tuple-write is configured; nil makes fgawrite.Emit a no-op
	// (dev / degraded). When wired, each resource Create publishes
	// `vpc_<resource>:<id>#project@project:<project_id>` so a per-resource
	// FGA Check resolves through the `<rel> from project` cascade.
	var fgaTupleWriter fgawrite.HierarchyTupleWriter
	if tw := cfg.AuthZ.TupleWrite; tw.Enabled && tw.OpenFGAEndpoint != "" && tw.StoreID != "" {
		timeout := time.Duration(tw.TimeoutMs) * time.Millisecond
		fgaTupleWriter = &clients.OpenFGAWriteClient{
			Endpoint: tw.OpenFGAEndpoint,
			StoreID:  tw.StoreID,
			ModelID:  tw.ModelID,
			Timeout:  timeout,
		}
		logger.Info("vpc write-side FGA wired (KAC-127 #22)",
			"openfga_endpoint", tw.OpenFGAEndpoint, "store_id", tw.StoreID,
			"model_id", tw.ModelID)
	} else {
		logger.Warn("vpc write-side FGA NOT wired — authz.tuple-write disabled; " +
			"created resources will have no per-resource FGA hierarchy tuple (KAC-127 #22)")
	}

	// CQRS pilot (KAC-94, skill evgeniy §6 G.1-G.7): все 8 VPC-ресурсов
	// (Network/Subnet/Address/RouteTable/SecurityGroup/Gateway/PrivateEndpoint/
	// NetworkInterface) работают через `kacho.Repository` (Reader/Writer split).
	// pgxpool-impl — `internal/repo/kacho/pg`. KAC-94 A.7 ultra-final: legacy
	// concrete-структуры `*repo.<X>Repo` удалены полностью; admin-сервисы и
	// peer-port'ы use-case-пакетов получают тонкие adapter'ы поверх kachoRepo
	// из пакета `internal/repo/cqrsadapter`.
	kachoRepo := kachopg.New(pool, slavePool)

	// Adapter'ы под узкие port-интерфейсы admin/peer-сервисов. Каждый adapter
	// открывает свежую Reader/Writer-TX на каждый вызов (G.4 — read на slave-
	// pool, если он настроен; write — на master).
	networkAdapter := cqrsadapter.NewNetwork(kachoRepo)
	subnetAdapter := cqrsadapter.NewSubnet(kachoRepo)
	addressAdapter := cqrsadapter.NewAddress(kachoRepo)
	routeTableAdapter := cqrsadapter.NewRouteTable(kachoRepo)
	sgAdapter := cqrsadapter.NewSecurityGroup(kachoRepo)
	niAdapter := cqrsadapter.NewNetworkInterface(kachoRepo)

	// AddressPool — admin-only use-case-структура (см.
	// `internal/apps/kacho/api/addresspool/`). Composition root собирает 13
	// use-case'ов + ResolverService под единый Handler. Все use-case'ы работают
	// через `kachoRepo` (CQRS-Repository) — каждый mutate открывает писатель,
	// делает DML + outbox emit в одной TX. KAC-94 A.7 ultra-final: узкие
	// read-port'ы Address/Subnet/Network удовлетворяются adapter'ами поверх
	// kachoRepo (cqrsadapter.Address / Subnet / Network).
	addressPoolResolver := addresspoolapp.NewResolverService(
		kachoRepo, addressAdapter, subnetAdapter, projectClient,
	)
	addressPoolHandler := addresspoolapp.NewHandler(
		addresspoolapp.NewCreateAddressPoolUseCase(kachoRepo, geoClient),
		addresspoolapp.NewUpdateAddressPoolUseCase(kachoRepo),
		addresspoolapp.NewDeleteAddressPoolUseCase(kachoRepo),
		addresspoolapp.NewGetAddressPoolUseCase(kachoRepo),
		addresspoolapp.NewListAddressPoolsUseCase(kachoRepo),
		addresspoolapp.NewCheckUseCase(kachoRepo),
		addresspoolapp.NewExplainResolutionUseCase(addressAdapter, addressPoolResolver),
		addresspoolapp.NewBindAsNetworkDefaultUseCase(kachoRepo, networkAdapter),
		addresspoolapp.NewUnbindNetworkDefaultUseCase(kachoRepo),
		addresspoolapp.NewBindAsAddressOverrideUseCase(kachoRepo, addressAdapter),
		addresspoolapp.NewUnbindAddressOverrideUseCase(kachoRepo),
		addresspoolapp.NewGetPoolUtilizationUseCase(kachoRepo),
		addresspoolapp.NewListPoolAddressesUseCase(kachoRepo),
	)
	cloudSelSet := addresspoolapp.NewSetCloudPoolSelectorUseCase(kachoRepo)
	cloudSelUnset := addresspoolapp.NewUnsetCloudPoolSelectorUseCase(kachoRepo)
	cloudSelGet := addresspoolapp.NewGetCloudPoolSelectorUseCase(kachoRepo)

	addressRefSvc := addressref.NewService(addressAdapter)

	// Network — use-case-структура (Wave 3a + Wave 5 pilot). Все use-case'ы
	// работают через kachoRepo (CQRS); checkNetworkEmpty / default-SG cleanup
	// в Network.Delete получают subnet/RT/SG adapter'ы, отделённые от writer-TX
	// (так как они каждый открывают свою TX).
	// defaultSGInline=true (default) — при Network.Create в одной writer-TX
	// создаётся inline default SG и Network.default_security_group_id
	// заполняется атомарно.
	netCreateUC := networkapp.NewCreateNetworkUseCase(kachoRepo, projectClient, opsRepo, cfg.Network.DefaultSGInline).
		WithFGAWriter(fgaTupleWriter, logger)
	netUpdateUC := networkapp.NewUpdateNetworkUseCase(kachoRepo, opsRepo)
	netDeleteUC := networkapp.NewDeleteNetworkUseCase(kachoRepo, subnetAdapter, routeTableAdapter, sgAdapter, opsRepo)
	netMoveUC := networkapp.NewMoveNetworkUseCase(kachoRepo, projectClient, opsRepo)
	netGetUC := networkapp.NewGetNetworkUseCase(kachoRepo)
	// KAC-127 Phase 4 — passes listAuthz to FGA-filter the List handler.
	// listAuthz == nil → use-case fallback'нёт на legacy unfiltered list.
	var netListAuthz networkapp.ListAuthorizer
	if listAuthz != nil {
		netListAuthz = listAuthz
	}
	netListUC := networkapp.NewListNetworksUseCase(kachoRepo, netListAuthz)
	netListSubUC := networkapp.NewListSubnetsUseCase(kachoRepo, subnetAdapter)
	netListSGUC := networkapp.NewListSecurityGroupsUseCase(kachoRepo, sgAdapter)
	netListRTUC := networkapp.NewListRouteTablesUseCase(kachoRepo, routeTableAdapter)
	netListOpsUC := networkapp.NewListOperationsUseCase(opsRepo)
	netHandler := networkapp.NewHandler(
		netCreateUC, netUpdateUC, netDeleteUC, netMoveUC,
		netGetUC, netListUC, netListSubUC, netListSGUC, netListRTUC, netListOpsUC,
	)

	// Gateway use-case'ы работают через CQRS-Repository (kachoRepo) —
	// конструктор принимает Repository, каждый use-case открывает Reader/Writer
	// внутри. KAC-94 A.7 ultra-final: legacy *repo.GatewayRepo удалён.
	gwHandler := gatewayapp.NewHandler(
		gatewayapp.NewCreateGatewayUseCase(kachoRepo, projectClient, opsRepo).
			WithFGAWriter(fgaTupleWriter, logger),
		gatewayapp.NewUpdateGatewayUseCase(kachoRepo, opsRepo),
		gatewayapp.NewDeleteGatewayUseCase(kachoRepo, opsRepo),
		gatewayapp.NewMoveGatewayUseCase(kachoRepo, projectClient, opsRepo),
		gatewayapp.NewGetGatewayUseCase(kachoRepo),
		gatewayapp.NewListGatewaysUseCase(kachoRepo, listauthz.AsPort(listAuthz)),
		gatewayapp.NewListOperationsUseCase(opsRepo),
	)

	// PrivateEndpoint use-case'ы работают через CQRS-Repository (kachoRepo).
	// NetworkReader/SubnetReader для request-path-precheck — adapter'ы поверх
	// kachoRepo (cqrsadapter.Network / Subnet).
	peHandler := peapp.NewHandler(
		peapp.NewCreatePrivateEndpointUseCase(kachoRepo, networkAdapter, subnetAdapter, projectClient, opsRepo).
			WithFGAWriter(fgaTupleWriter, logger),
		peapp.NewUpdatePrivateEndpointUseCase(kachoRepo, opsRepo),
		peapp.NewDeletePrivateEndpointUseCase(kachoRepo, opsRepo),
		peapp.NewGetPrivateEndpointUseCase(kachoRepo),
		peapp.NewListPrivateEndpointsUseCase(kachoRepo, listauthz.AsPort(listAuthz)),
		peapp.NewListOperationsUseCase(opsRepo),
	)

	// RouteTable use-case'ы работают через CQRS-Repository (parity с pilot
	// Network). routeTableAdapter передаётся Network.Delete для child-check.
	rtHandler := routetableapp.NewHandler(
		routetableapp.NewCreateRouteTableUseCase(kachoRepo, projectClient, opsRepo).
			WithFGAWriter(fgaTupleWriter, logger),
		routetableapp.NewUpdateRouteTableUseCase(kachoRepo, opsRepo),
		routetableapp.NewDeleteRouteTableUseCase(kachoRepo, opsRepo),
		routetableapp.NewMoveRouteTableUseCase(kachoRepo, projectClient, opsRepo),
		routetableapp.NewGetRouteTableUseCase(kachoRepo),
		routetableapp.NewListRouteTablesUseCase(kachoRepo, listauthz.AsPort(listAuthz)),
		routetableapp.NewListOperationsUseCase(opsRepo),
	)

	// Subnet use-case'ы работают через CQRS-Repository (kachoRepo). niAdapter
	// передаётся в Delete для precondition-check «нет привязанных NIC».
	subnetHandler := subnetapp.NewHandler(
		subnetapp.NewCreateSubnetUseCase(kachoRepo, projectClient, geoClient, opsRepo).
			WithFGAWriter(fgaTupleWriter, logger),
		subnetapp.NewUpdateSubnetUseCase(kachoRepo, opsRepo),
		subnetapp.NewDeleteSubnetUseCase(kachoRepo, niAdapter, opsRepo),
		subnetapp.NewMoveSubnetUseCase(kachoRepo, projectClient, opsRepo),
		subnetapp.NewGetSubnetUseCase(kachoRepo),
		subnetapp.NewListSubnetsUseCase(kachoRepo, listauthz.AsPort(listAuthz)),
		subnetapp.NewAddCidrBlocksUseCase(kachoRepo, opsRepo),
		subnetapp.NewRemoveCidrBlocksUseCase(kachoRepo, opsRepo),
		subnetapp.NewRelocateUseCase(kachoRepo, geoClient),
		subnetapp.NewListUsedAddressesUseCase(kachoRepo, addressAdapter),
		subnetapp.NewListOperationsUseCase(opsRepo),
	)

	// Address — use-case-структура. Composition с AddressPoolService для IPAM
	// cascade resolve. Internal Allocate UC отделён — принимается
	// InternalAddressAllocateHandler через узкий port.
	//
	// Все Address use-cases работают через CQRS-Repository (`kachoRepo`). IPAM
	// atomicity (Insert + Allocate + Outbox) гарантируется одной writer-TX в
	// `CreateAddressUseCase.doCreate` / `AllocateUseCase.*`. subnetAdapter —
	// peer-port для SubnetReader (Get + AddressesBySubnet), удовлетворяется
	// тем же kachoRepo через cqrsadapter.
	addressCreateUC := addressapp.NewCreateAddressUseCase(kachoRepo, subnetAdapter, projectClient, opsRepo, addressPoolResolver).
		WithFGAWriter(fgaTupleWriter, logger)
	addressUpdateUC := addressapp.NewUpdateAddressUseCase(kachoRepo, opsRepo)
	addressDeleteUC := addressapp.NewDeleteAddressUseCase(kachoRepo, opsRepo)
	addressMoveUC := addressapp.NewMoveAddressUseCase(kachoRepo, projectClient, opsRepo)
	addressGetUC := addressapp.NewGetAddressUseCase(kachoRepo)
	addressGetByValueUC := addressapp.NewGetByValueUseCase(kachoRepo)
	addressListUC := addressapp.NewListAddressesUseCase(kachoRepo, listauthz.AsPort(listAuthz))
	addressListBySubnetUC := addressapp.NewListBySubnetUseCase(kachoRepo, subnetAdapter)
	addressListOpsUC := addressapp.NewListOperationsUseCase(opsRepo)
	addressAllocateUC := addressapp.NewAllocateUseCase(kachoRepo, subnetAdapter, addressPoolResolver)
	addressHandler := addressapp.NewHandler(
		addressCreateUC, addressUpdateUC, addressDeleteUC, addressMoveUC,
		addressGetUC, addressGetByValueUC, addressListUC, addressListBySubnetUC, addressListOpsUC,
		nil, // SubnetAuthZ опционален — пока nil
	)

	// SecurityGroup — use-case-структура. Split-endpoint Update / UpdateRules
	// / UpdateRule (OCC через xmin в repo). Все DML + outbox-emit идут в одной
	// writer-TX (G.5). sgAdapter (cqrsadapter поверх kachoRepo) передаётся в
	// Network use-case'ы для checkNetworkEmpty / default-SG cleanup при
	// Network.Delete (отдельная TX от Network writer'а).
	sgHandler := sgapp.NewHandler(
		sgapp.NewCreateSecurityGroupUseCase(kachoRepo, networkAdapter, projectClient, opsRepo).
			WithFGAWriter(fgaTupleWriter, logger),
		sgapp.NewUpdateSecurityGroupUseCase(kachoRepo, opsRepo),
		sgapp.NewUpdateRulesUseCase(kachoRepo, opsRepo),
		sgapp.NewUpdateRuleUseCase(kachoRepo, opsRepo),
		sgapp.NewDeleteSecurityGroupUseCase(kachoRepo, opsRepo),
		sgapp.NewMoveSecurityGroupUseCase(kachoRepo, projectClient, opsRepo),
		sgapp.NewGetSecurityGroupUseCase(kachoRepo),
		sgapp.NewListSecurityGroupsUseCase(kachoRepo, listauthz.AsPort(listAuthz)),
		sgapp.NewListOperationsUseCase(kachoRepo, opsRepo),
	)

	// NetworkInterface — use-case-структура. Все use-case'ы работают через
	// CQRS-Repository (`kachoRepo`). У NIC нет Move RPC (NIC привязан к
	// Subnet); AttachToInstance / DetachFromInstance — atomic CAS на DB-уровне
	// (KAC-52, `writer.NetworkInterfaces().AttachToInstance`).
	// addressAdapter передаётся в Create/Update/Delete UC — он удовлетворяет
	// `networkinterface.AddressRepo` port (Get + SetReference + ClearReference).
	niHandler := niapp.NewHandler(
		niapp.NewCreateNetworkInterfaceUseCase(kachoRepo, addressAdapter, projectClient, opsRepo),
		niapp.NewUpdateNetworkInterfaceUseCase(kachoRepo, addressAdapter, opsRepo),
		niapp.NewDeleteNetworkInterfaceUseCase(kachoRepo, addressAdapter, opsRepo),
		niapp.NewGetNetworkInterfaceUseCase(kachoRepo),
		niapp.NewListNetworkInterfacesUseCase(kachoRepo, listauthz.AsPort(listAuthz)),
		niapp.NewAttachToInstanceUseCase(kachoRepo, opsRepo),
		niapp.NewDetachFromInstanceUseCase(kachoRepo, opsRepo),
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
		privateEndpointHandler:  peHandler,
		addressPoolHandler:      addressPoolHandler,
		cloudSelSet:             cloudSelSet,
		cloudSelUnset:           cloudSelUnset,
		cloudSelGet:             cloudSelGet,
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
	pepb.RegisterPrivateEndpointServiceServer(srv, svcs.privateEndpointHandler)
	operationpb.RegisterOperationServiceServer(srv, handler.NewOperationHandler(opsRepo))
}

// registerInternalServices — kacho-only/admin RPC на internal listener.
func registerInternalServices(srv *grpc.Server, svcs *services, pool *pgxpool.Pool, dsn string, logger *slog.Logger, watchMaxStreams int) {
	vpcv1.RegisterInternalWatchServiceServer(srv, handler.NewInternalWatchHandler(pool, dsn, logger.With("component", "internal-watch"), watchMaxStreams))
	vpcv1.RegisterInternalAddressServiceServer(srv, handler.NewInternalAddressAllocateHandler(svcs.addressAllocate, svcs.addressRefService))
	vpcv1.RegisterInternalAddressPoolServiceServer(srv, svcs.addressPoolHandler)
	vpcv1.RegisterInternalNetworkServiceServer(srv, handler.NewInternalNetworkHandler(svcs.networkInternal))
	vpcv1.RegisterInternalCloudServiceServer(srv, handler.NewInternalCloudHandler(svcs.cloudSelSet, svcs.cloudSelUnset, svcs.cloudSelGet))
}

// maskDSN отдаёт DSN с замаскированным паролем — для безопасного логирования
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
