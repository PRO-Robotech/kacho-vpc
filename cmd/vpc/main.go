package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/observability"
	"github.com/PRO-Robotech/kacho-corelib/operations"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
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
type services struct {
	network          *service.NetworkService
	subnet           *service.SubnetService
	address          *service.AddressService
	routeTable       *service.RouteTableService
	securityGroup    *service.SecurityGroupService
	gateway          *service.GatewayService
	privateEndpoint  *service.PrivateEndpointService
	addressPool      *service.AddressPoolService
	networkInternal  *service.NetworkInternal
	networkInterface *service.NetworkInterfaceService
}

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

	opsRepo := operations.NewRepo(pool, "public")

	// Cross-service gRPC dial — через единый builder (KAC-97, skill evgeniy §9 K.6):
	// retries=3 / dialTimeout=10s / keepalive=30s / TLS / опц. dns:///+round_robin (KAC-39).
	// См. internal/clients/builder.go.
	rmConn, err := clients.Build(ctx, clients.BuildOptions{
		Endpoint: cfg.ExtAPI.ResourceManager.Endpoint,
		TLS:      cfg.ExtAPI.ResourceManager.TLS.Enable,
		DNSLB:    cfg.ExtAPI.ResourceManager.DNSLB,
	})
	if err != nil {
		return fmt.Errorf("dial resource-manager: %w", err)
	}
	defer rmConn.Close()
	// TTL+LRU кеш (KAC-39): снимает gRPC-hop в RM из hot-path Network.Create
	// при burst-нагрузке (10k RPS). См. internal/clients/folder_cache.go.
	rawFolderClient := clients.NewFolderClient(rmConn)
	folderClient := clients.NewCachedFolderClient(rawFolderClient, clients.FolderCacheConfig{
		PositiveTTL: cfg.Network.FolderCache.PositiveTTL,
		NegativeTTL: cfg.Network.FolderCache.NegativeTTL,
		MaxSize:     cfg.Network.FolderCache.MaxSize,
	})
	logger.Info("folder existence cache enabled",
		"positive_ttl", cfg.Network.FolderCache.PositiveTTL,
		"negative_ttl", cfg.Network.FolderCache.NegativeTTL,
		"max_size", cfg.Network.FolderCache.MaxSize)

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

	svcs := buildServices(pool, folderClient, geoClient, opsRepo, cfg, logger)

	// gRPC servers + tenant-interceptor (scaffold под IAM/AuthZ): сейчас читает
	// metadata, future — JWT claims; handler'ы делают AssertFolderOwnership.
	productionMode := cfg.AuthN.Mode.IsProduction()
	grpcSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(handler.TenantUnaryInterceptor(false, productionMode)),
		grpc.ChainStreamInterceptor(handler.TenantStreamInterceptor(false, productionMode)),
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

	// shutdownDone закрывается после полного дрейна (GracefulStop + LRO worker'ов).
	shutdownDone := make(chan struct{})
	gracefulTimeout := cfg.APIServer.GracefulShutdown
	if gracefulTimeout <= 0 {
		gracefulTimeout = 10 * time.Second
	}
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		internalSrv.GracefulStop()
		grpcSrv.GracefulStop()
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), 3*gracefulTimeout)
		defer cancelDrain()
		if err := operations.Wait(drainCtx); err != nil {
			logger.Warn("operations workers did not finish in time",
				"err", err, "active", operations.Active())
		}
	}()

	go func() {
		if err := internalSrv.Serve(internalListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.Error("internal grpc server stopped", "err", err)
		}
	}()

	serveErr := grpcSrv.Serve(listener)
	cancel()
	<-shutdownDone
	return serveErr
}

// KAC-97: dialResourceManager / dialCompute / peerCreds удалены — заменены на
// единый clients.Build (internal/clients/builder.go). validateAuthMode из KAC-95
// перенесён в config.Validate() / config.InsecureDevWarnings().

// buildServices создаёт все repo'ы поверх pool и собирает из них бизнес-сервисы.
// defaultSGRepo: nil при network.default-sg-inline=false → Network.Create не создаёт
// inline default SG.
func buildServices(pool *pgxpool.Pool, folderClient service.FolderClient, geoClient service.ZoneRegistry, opsRepo operations.Repo, cfg config.Config, logger *slog.Logger) *services {
	networkRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	addressRepo := repo.NewAddressRepo(pool)
	routeTableRepo := repo.NewRouteTableRepo(pool)
	sgRepo := repo.NewSecurityGroupRepo(pool)
	gatewayRepo := repo.NewGatewayRepo(pool)
	peRepo := repo.NewPrivateEndpointRepo(pool)
	addressPoolRepo := repo.NewAddressPoolRepo(pool)
	addressPoolBindingRepo := repo.NewAddressPoolBindingRepo(pool)
	cloudPoolSelectorRepo := repo.NewCloudPoolSelectorRepo(pool)
	niRepo := repo.NewNetworkInterfaceRepo(pool)

	var defaultSGRepo service.SecurityGroupRepo
	if cfg.Network.DefaultSGInline {
		defaultSGRepo = sgRepo
	} else {
		logger.Warn("network.default-sg-inline=false — Network.Create НЕ создаёт default SG")
	}

	sgSvc := service.NewSecurityGroupService(sgRepo, networkRepo, folderClient, opsRepo)
	addressPoolSvc := service.NewAddressPoolService(addressPoolRepo, addressPoolBindingRepo, cloudPoolSelectorRepo, addressRepo, networkRepo, subnetRepo, folderClient, geoClient)
	subnetSvc := service.NewSubnetService(subnetRepo, networkRepo, folderClient, opsRepo, geoClient)
	subnetSvc.SetAddressRefRepo(addressRepo)
	subnetSvc.SetNICRepo(niRepo)
	return &services{
		network:          service.NewNetworkService(networkRepo, subnetRepo, routeTableRepo, sgSvc, folderClient, opsRepo, defaultSGRepo),
		subnet:           subnetSvc,
		address:          service.NewAddressService(addressRepo, subnetRepo, folderClient, opsRepo, addressPoolSvc),
		routeTable:       service.NewRouteTableService(routeTableRepo, networkRepo, folderClient, opsRepo),
		securityGroup:    sgSvc,
		gateway:          service.NewGatewayService(gatewayRepo, folderClient, opsRepo),
		privateEndpoint:  service.NewPrivateEndpointService(peRepo, folderClient, networkRepo, subnetRepo, opsRepo),
		addressPool:      addressPoolSvc,
		networkInternal:  service.NewNetworkInternal(networkRepo, sgRepo),
		networkInterface: service.NewNetworkInterfaceService(niRepo, subnetRepo, addressRepo, folderClient, opsRepo),
	}
}

// registerPublicServices — публичные RPC + OperationService на внешний listener.
func registerPublicServices(srv *grpc.Server, svcs *services, opsRepo operations.Repo) {
	vpcv1.RegisterNetworkServiceServer(srv, handler.NewNetworkHandler(svcs.network))
	vpcv1.RegisterSubnetServiceServer(srv, handler.NewSubnetHandler(svcs.subnet))
	vpcv1.RegisterAddressServiceServer(srv, handler.NewAddressHandler(svcs.address, svcs.subnet))
	vpcv1.RegisterRouteTableServiceServer(srv, handler.NewRouteTableHandler(svcs.routeTable))
	vpcv1.RegisterSecurityGroupServiceServer(srv, handler.NewSecurityGroupHandler(svcs.securityGroup))
	vpcv1.RegisterGatewayServiceServer(srv, handler.NewGatewayHandler(svcs.gateway))
	vpcv1.RegisterNetworkInterfaceServiceServer(srv, handler.NewNetworkInterfaceHandler(svcs.networkInterface))
	pepb.RegisterPrivateEndpointServiceServer(srv, handler.NewPrivateEndpointHandler(svcs.privateEndpoint))
	operationpb.RegisterOperationServiceServer(srv, handler.NewOperationHandler(opsRepo))
}

// registerInternalServices — kacho-only/admin RPC на internal listener.
func registerInternalServices(srv *grpc.Server, svcs *services, pool *pgxpool.Pool, dsn string, logger *slog.Logger, watchMaxStreams int) {
	vpcv1.RegisterInternalWatchServiceServer(srv, handler.NewInternalWatchHandler(pool, dsn, logger.With("component", "internal-watch"), watchMaxStreams))
	vpcv1.RegisterInternalAddressServiceServer(srv, handler.NewInternalAddressAllocateHandler(svcs.address))
	vpcv1.RegisterInternalAddressPoolServiceServer(srv, handler.NewInternalAddressPoolHandler(svcs.addressPool))
	vpcv1.RegisterInternalNetworkServiceServer(srv, handler.NewInternalNetworkHandler(svcs.networkInternal))
	vpcv1.RegisterInternalCloudServiceServer(srv, handler.NewInternalCloudHandler(svcs.addressPool))
}
