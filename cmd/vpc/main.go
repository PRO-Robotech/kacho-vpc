package main

import (
	"context"
	"crypto/tls"
	"database/sql"
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
	"github.com/pressly/goose/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/observability"
	"github.com/PRO-Robotech/kacho-corelib/operations"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"

	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: vpc {serve|migrate up|migrate down|migrate status}")
	}
	cmd := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	switch cmd {
	case "migrate":
		if len(os.Args) < 3 {
			log.Fatal("usage: vpc migrate {up|down|status}")
		}
		runMigrate(cfg, os.Args[2])
	case "serve":
		if err := runServe(cfg); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown command: %s", cmd)
	}
}

// services — собранный набор бизнес-сервисов (один composition-point вместо
// россыпи локальных переменных в runServe). Заполняется buildServices,
// используется register{Public,Internal}Services.
type services struct {
	network         *service.NetworkService
	subnet          *service.SubnetService
	address         *service.AddressService
	routeTable      *service.RouteTableService
	securityGroup   *service.SecurityGroupService
	gateway         *service.GatewayService
	privateEndpoint *service.PrivateEndpointService
	addressPool     *service.AddressPoolService
	networkInternal *service.NetworkInternal
}

func runServe(cfg config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	logger := observability.NewSlogger(os.Stdout)
	slog.SetDefault(logger)

	productionMode, err := validateAuthMode(cfg, logger)
	if err != nil {
		return err
	}

	pool, err := coredb.NewPool(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	opsRepo := operations.NewRepo(pool, "public")

	rmConn, err := dialResourceManager(cfg)
	if err != nil {
		return err
	}
	defer rmConn.Close()
	folderClient := clients.NewFolderClient(rmConn)

	// Geography (Region/Zone) — домен kacho-compute (эпик KAC-15): VPC валидирует
	// zone_id вызовом compute.v1.ZoneService.Get (см. workspace CLAUDE.md
	// §«Кросс-доменные ссылки на ресурсы»).
	computeConn, err := dialCompute(cfg)
	if err != nil {
		return err
	}
	defer computeConn.Close()
	geoClient := clients.NewComputeGeographyClient(computeConn)

	svcs := buildServices(pool, folderClient, geoClient, opsRepo, cfg, logger)

	// gRPC servers + tenant-interceptor (scaffold под IAM/AuthZ): сейчас читает
	// metadata, future — JWT claims; handler'ы делают AssertFolderOwnership.
	// Публичный listener — requireAdmin=false; internal :9091 — requireAdmin=true
	// (defense-in-depth поверх NetworkPolicy в helm).
	grpcSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(handler.TenantUnaryInterceptor(false, productionMode)),
		grpc.ChainStreamInterceptor(handler.TenantStreamInterceptor(false, productionMode)),
	)
	internalSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(handler.TenantUnaryInterceptor(true, productionMode)),
		grpc.ChainStreamInterceptor(handler.TenantStreamInterceptor(true, productionMode)),
	)
	registerPublicServices(grpcSrv, svcs, opsRepo)
	registerInternalServices(internalSrv, svcs, pool, cfg.MigrateDSN(), logger, cfg.WatchMaxStreams)

	listener, err := net.Listen("tcp", ":"+cfg.GrpcPort)
	if err != nil {
		return err
	}
	internalListener, err := net.Listen("tcp", ":"+cfg.InternalGrpcPort)
	if err != nil {
		_ = listener.Close()
		return err
	}
	logger.Info("kacho-vpc listening",
		"public_port", cfg.GrpcPort,
		"internal_port", cfg.InternalGrpcPort)

	// shutdownDone закрывается после полного дрейна (GracefulStop + LRO worker'ов).
	// runServe блокируется на нём перед возвратом — иначе main → os.Exit обрывает
	// in-flight LRO worker'ов до того как operations.Wait успел дождаться (P0 R7→R8).
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		internalSrv.GracefulStop()
		grpcSrv.GracefulStop()
		// Дождаться async LRO worker'ов (operations.Run): иначе in-flight
		// Create/Update/Delete теряются на SIGTERM (handler вернул Operation,
		// worker крутит INSERT/Allocate, процесс exit'ит → Operation.done=false навсегда).
		drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := operations.Wait(drainCtx); err != nil {
			logger.Warn("operations workers did not finish in time",
				"err", err, "active", operations.Active())
		}
	}()

	go func() {
		// grpc.ErrServerStopped — штатный exit на graceful shutdown, не Error
		// (без фильтра каждый clean shutdown шумит в alerting — R9).
		if err := internalSrv.Serve(internalListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.Error("internal grpc server stopped", "err", err)
		}
	}()

	serveErr := grpcSrv.Serve(listener)
	// Если Serve вернул из-за abnormal listener-exit (kernel закрыл socket, OOM,
	// listener.Close() извне) — SIGTERM не приходил, shutdown-горутина висит на
	// <-ctx.Done(); cancel() будит её → GracefulStop + operations.Wait → закрывает
	// shutdownDone. Без этого <-shutdownDone deadlock'нулся бы (R8 m1).
	cancel()
	<-shutdownDone
	return serveErr
}

// validateAuthMode разбирает KACHO_VPC_AUTH_MODE (whitelist — typo `prod`/`PRODUCTION`
// НЕ должен silently пройти как dev, R10 F-1), для production-strict дополнительно
// валидирует cross-service TLS + DB sslmode (R10 F-3), и логирует insecure dev-defaults.
func validateAuthMode(cfg config.Config, logger *slog.Logger) (productionMode bool, err error) {
	switch cfg.AuthMode {
	case "dev":
		productionMode = false
	case "production":
		productionMode = true
		logger.Warn("AuthMode=production: anonymous callers will be rejected (M5 fail-closed)")
	case "production-strict":
		productionMode = true
		if !cfg.ResourceManagerTLS {
			return false, fmt.Errorf("production-strict mode: KACHO_VPC_RESOURCE_MANAGER_TLS=true required")
		}
		switch cfg.DBSSLMode { // `prefer`/`allow` допускают TLS-fallback к plaintext под MITM
		case "require", "verify-ca", "verify-full":
			// OK
		default:
			return false, fmt.Errorf("production-strict mode: KACHO_VPC_DB_SSLMODE must be one of require|verify-ca|verify-full (got %q)", cfg.DBSSLMode)
		}
		logger.Warn("AuthMode=production-strict: anonymous rejected + TLS+SSL strictly validated")
	default:
		return false, fmt.Errorf("unknown KACHO_VPC_AUTH_MODE=%q (allowed: dev, production, production-strict)", cfg.AuthMode)
	}
	if !productionMode {
		if !cfg.ResourceManagerTLS {
			logger.Warn("KACHO_VPC_RESOURCE_MANAGER_TLS=false — cross-service gRPC plaintext (dev only)")
		}
		if cfg.DBSSLMode == "" || cfg.DBSSLMode == "disable" {
			logger.Warn("KACHO_VPC_DB_SSLMODE=disable — DB plaintext (dev only)")
		}
	}
	return productionMode, nil
}

// dialResourceManager открывает gRPC-клиент к resource-manager. TLS опционален
// через KACHO_VPC_RESOURCE_MANAGER_TLS=true (закрывает in-cluster MITM на
// FolderClient.Exists/GetCloudID — security P0); по умолчанию insecure для dev-стенда.
func dialResourceManager(cfg config.Config) (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	if cfg.ResourceManagerTLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}
	return grpc.NewClient(cfg.ResourceManagerGRPCAddr, grpc.WithTransportCredentials(creds))
}

// dialCompute открывает gRPC-клиент к kacho-compute (owner Geography). TLS опционален
// через KACHO_VPC_COMPUTE_TLS=true; по умолчанию insecure для dev-стенда.
func dialCompute(cfg config.Config) (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	if cfg.ComputeTLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}
	return grpc.NewClient(cfg.ComputeGRPCAddr, grpc.WithTransportCredentials(creds))
}

// buildServices создаёт все repo'ы поверх pool и собирает из них бизнес-сервисы.
// defaultSGRepo: nil при KACHO_VPC_DEFAULT_SG_INLINE=false → Network.Create не создаёт
// inline default SG (verbatim YC: SG создаётся внешним reconciler'ом; убирает 2 INSERT +
// 1 UPDATE из hot-path). geoClient — ZoneRegistry-impl над kacho-compute (валидация zone_id).
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

	var defaultSGRepo service.SecurityGroupRepo
	if cfg.DefaultSGInline {
		defaultSGRepo = sgRepo
	} else {
		logger.Warn("KACHO_VPC_DEFAULT_SG_INLINE=false — Network.Create НЕ создаёт default SG")
	}

	sgSvc := service.NewSecurityGroupService(sgRepo, networkRepo, folderClient, opsRepo)
	addressPoolSvc := service.NewAddressPoolService(addressPoolRepo, addressPoolBindingRepo, cloudPoolSelectorRepo, addressRepo, networkRepo, subnetRepo, folderClient)
	subnetSvc := service.NewSubnetService(subnetRepo, networkRepo, folderClient, opsRepo, geoClient)
	// addressRepo обогащает SubnetService.ListUsedAddresses записями referrer'ов
	// (UsedAddress.references[] — кто использует адрес; YC-like).
	subnetSvc.SetAddressRefRepo(addressRepo)
	return &services{
		network:         service.NewNetworkService(networkRepo, subnetRepo, routeTableRepo, sgSvc, folderClient, opsRepo, defaultSGRepo),
		subnet:          subnetSvc,
		address:         service.NewAddressService(addressRepo, subnetRepo, folderClient, opsRepo, addressPoolSvc),
		routeTable:      service.NewRouteTableService(routeTableRepo, networkRepo, folderClient, opsRepo),
		securityGroup:   sgSvc,
		gateway:         service.NewGatewayService(gatewayRepo, folderClient, opsRepo),
		privateEndpoint: service.NewPrivateEndpointService(peRepo, folderClient, networkRepo, subnetRepo, opsRepo),
		addressPool:     addressPoolSvc,
		networkInternal: service.NewNetworkInternal(networkRepo, sgRepo),
	}
}

// registerPublicServices — публичные (verbatim-YC) RPC + OperationService на
// внешний listener (:9090, проксируется api-gateway). reflection включён в grpcsrv.NewServer.
func registerPublicServices(srv *grpc.Server, svcs *services, opsRepo operations.Repo) {
	vpcv1.RegisterNetworkServiceServer(srv, handler.NewNetworkHandler(svcs.network))
	vpcv1.RegisterSubnetServiceServer(srv, handler.NewSubnetHandler(svcs.subnet))
	vpcv1.RegisterAddressServiceServer(srv, handler.NewAddressHandler(svcs.address, svcs.subnet))
	vpcv1.RegisterRouteTableServiceServer(srv, handler.NewRouteTableHandler(svcs.routeTable))
	vpcv1.RegisterSecurityGroupServiceServer(srv, handler.NewSecurityGroupHandler(svcs.securityGroup))
	vpcv1.RegisterGatewayServiceServer(srv, handler.NewGatewayHandler(svcs.gateway))
	pepb.RegisterPrivateEndpointServiceServer(srv, handler.NewPrivateEndpointHandler(svcs.privateEndpoint))
	operationpb.RegisterOperationServiceServer(srv, handler.NewOperationHandler(opsRepo))
}

// registerInternalServices — kacho-only/admin RPC на internal listener (:9091, не
// маршрутизируется наружу; NetworkPolicy в helm + requireAdmin-interceptor).
// InternalWatch держит dedicated pgx.Conn вне пула — отсюда отдельный dsn.
func registerInternalServices(srv *grpc.Server, svcs *services, pool *pgxpool.Pool, dsn string, logger *slog.Logger, watchMaxStreams int) {
	vpcv1.RegisterInternalWatchServiceServer(srv, handler.NewInternalWatchHandler(pool, dsn, logger.With("component", "internal-watch"), watchMaxStreams))
	vpcv1.RegisterInternalAddressServiceServer(srv, handler.NewInternalAddressAllocateHandler(svcs.address))
	vpcv1.RegisterInternalAddressPoolServiceServer(srv, handler.NewInternalAddressPoolHandler(svcs.addressPool))
	vpcv1.RegisterInternalNetworkServiceServer(srv, handler.NewInternalNetworkHandler(svcs.networkInternal))
	vpcv1.RegisterInternalCloudServiceServer(srv, handler.NewInternalCloudHandler(svcs.addressPool))
}

func runMigrate(cfg config.Config, direction string) {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("goose dialect: %v", err)
	}

	db, err := sql.Open("pgx", cfg.MigrateDSN())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var gooseErr error
	switch direction {
	case "up":
		gooseErr = goose.Up(db, ".")
	case "down":
		gooseErr = goose.Down(db, ".")
	case "status":
		gooseErr = goose.Status(db, ".")
	default:
		log.Fatalf("unknown migrate direction: %s", direction)
	}
	if gooseErr != nil {
		log.Fatalf("migrate %s: %v", direction, gooseErr)
	}
}
