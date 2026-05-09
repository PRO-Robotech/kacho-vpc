package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

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

func runServe(cfg config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	logger := observability.NewSlogger(os.Stdout)
	slog.SetDefault(logger)

	pool, err := coredb.NewPool(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	// Operations repo.
	opsRepo := operations.NewRepo(pool, "public")

	// gRPC клиент к resource-manager.
	// Security: TLS опциональный через KACHO_VPC_RESOURCE_MANAGER_TLS=true
	// (закрывает in-cluster MITM на FolderClient.Exists/GetCloudID — security
	// P0). По умолчанию insecure для backward-compat dev-стенда.
	var rmCreds credentials.TransportCredentials
	if cfg.ResourceManagerTLS {
		rmCreds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		rmCreds = insecure.NewCredentials()
	}
	rmConn, err := grpc.NewClient(cfg.ResourceManagerGRPCAddr, grpc.WithTransportCredentials(rmCreds))
	if err != nil {
		return err
	}
	defer rmConn.Close()

	folderClient := clients.NewFolderClient(rmConn)

	// Repos.
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
	regionRepo := repo.NewRegionRepo(pool)
	zoneRepo := repo.NewZoneRepo(pool)

	// Services.
	sgSvc := service.NewSecurityGroupService(sgRepo, networkRepo, folderClient, opsRepo)
	networkSvc := service.NewNetworkService(networkRepo, subnetRepo, routeTableRepo, sgSvc, folderClient, opsRepo)
	subnetSvc := service.NewSubnetService(subnetRepo, networkRepo, folderClient, opsRepo)
	addressSvc := service.NewAddressService(addressRepo, subnetRepo, folderClient, opsRepo)
	routeTableSvc := service.NewRouteTableService(routeTableRepo, networkRepo, folderClient, opsRepo)
	gatewaySvc := service.NewGatewayService(gatewayRepo, folderClient, opsRepo)
	peSvc := service.NewPrivateEndpointService(peRepo, folderClient, networkRepo, subnetRepo, opsRepo)
	addressPoolSvc := service.NewAddressPoolService(addressPoolRepo, addressPoolBindingRepo, cloudPoolSelectorRepo, addressRepo, networkRepo, subnetRepo, folderClient)
	addressAllocator := service.NewAddressAllocator(addressRepo, subnetRepo, addressPoolSvc)
	networkInternalSvc := service.NewNetworkInternal(networkRepo, sgRepo)
	regionSvc := service.NewRegionService(regionRepo)
	zoneSvc := service.NewZoneService(zoneRepo, regionRepo)

	// Inline IPAM allocation в request-path (Phase-2: kacho-vpc-controllers упразднён).
	addressSvc.SetAllocator(addressAllocator)
	// Inline default-SG creation в request-path NetworkService.doCreate.
	networkSvc.SetSGRepo(sgRepo)

	// gRPC server.
	// gRPC server с tenant-interceptor (scaffold под IAM/AuthZ).
	// Сейчас reads metadata; future — JWT claims. Handler'ы используют
	// AssertFolderOwnership(ctx, resource.FolderID) для AuthZ check.
	// requireAdmin=false: публичный listener, anonymous + folder-scoped tenant
	// допустимы; admin-flag не enforce'ится.
	grpcSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(handler.TenantUnaryInterceptor(false)),
		grpc.ChainStreamInterceptor(handler.TenantStreamInterceptor(false)),
	)

	// Регистрируем все публичные сервисы.
	vpcv1.RegisterNetworkServiceServer(grpcSrv, handler.NewNetworkHandler(networkSvc))
	vpcv1.RegisterSubnetServiceServer(grpcSrv, handler.NewSubnetHandler(subnetSvc))
	vpcv1.RegisterAddressServiceServer(grpcSrv, handler.NewAddressHandler(addressSvc))
	vpcv1.RegisterRouteTableServiceServer(grpcSrv, handler.NewRouteTableHandler(routeTableSvc))
	vpcv1.RegisterSecurityGroupServiceServer(grpcSrv, handler.NewSecurityGroupHandler(sgSvc))
	vpcv1.RegisterGatewayServiceServer(grpcSrv, handler.NewGatewayHandler(gatewaySvc))
	pepb.RegisterPrivateEndpointServiceServer(grpcSrv, handler.NewPrivateEndpointHandler(peSvc))
	operationpb.RegisterOperationServiceServer(grpcSrv, handler.NewOperationHandler(opsRepo))

	// gRPC reflection уже включён в grpcsrv.NewServer (corelib).

	// Internal gRPC server — отдельный порт, не виден через api-gateway.
	// Регистрируем InternalWatchService + InternalAddressService для kacho-vpc-controllers.
	// requireAdmin=true: с IAM-токеном на listener'е допустимы только caller'ы
	// с admin-claim'ом. Anonymous-mode (нет AuthN) — backward-compat (interceptor
	// принимает). NetworkPolicy в helm закрывает port на уровне k8s; admin-check
	// — defense-in-depth внутри.
	internalSrv := grpcsrv.NewServer(
		grpc.ChainUnaryInterceptor(handler.TenantUnaryInterceptor(true)),
		grpc.ChainStreamInterceptor(handler.TenantStreamInterceptor(true)),
	)
	vpcv1.RegisterInternalWatchServiceServer(internalSrv, handler.NewInternalWatchHandler(pool, cfg.DSN(), logger.With("component", "internal-watch"), cfg.WatchMaxStreams))
	// InternalAddressService — оба handler'а реализуют один и тот же gRPC service-interface
	// (legacy SetInternalIP в handler.InternalAddressHandler + AllocateInternal/External в
	// handler.InternalAddressAllocateHandler). gRPC требует ОДНУ имплементацию на сервис, поэтому
	// объединяем через композитный adapter.
	vpcv1.RegisterInternalAddressServiceServer(internalSrv, handler.NewInternalAddressCompositeHandler(
		handler.NewInternalAddressHandler(pool, logger.With("component", "internal-address")),
		handler.NewInternalAddressAllocateHandler(addressAllocator),
	))
	vpcv1.RegisterInternalAddressPoolServiceServer(internalSrv, handler.NewInternalAddressPoolHandler(addressPoolSvc))
	vpcv1.RegisterInternalNetworkServiceServer(internalSrv, handler.NewInternalNetworkHandler(networkInternalSvc))
	vpcv1.RegisterInternalCloudServiceServer(internalSrv, handler.NewInternalCloudHandler(addressPoolSvc))
	vpcv1.RegisterInternalRegionServiceServer(internalSrv, handler.NewInternalRegionHandler(regionSvc))
	vpcv1.RegisterInternalZoneServiceServer(internalSrv, handler.NewInternalZoneHandler(zoneSvc))

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
	// Без этого канала горутина детачилась бы — Serve() возвращал бы сразу после
	// GracefulStop, runServe → main → os.Exit обрывал бы in-flight LRO worker'ов
	// до того как Wait успел дождаться. Теперь runServe блокируется на shutdownDone
	// перед возвратом — fix P0 регрессии R7→R8.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		// 1) Stop accepting new RPC + ждать активные.
		internalSrv.GracefulStop()
		grpcSrv.GracefulStop()
		// 2) Дождаться async LRO worker'ов (operations.Run). Без этого
		//    in-flight Create/Update/Delete теряются на SIGTERM:
		//    handler уже вернул Operation, worker крутит INSERT/Allocate,
		//    процесс exit'ит mid-allocate → Operation.done=false навсегда.
		//    Concurrency P0 #1 closure (зависит от kacho-corelib operations
		//    Worker.Wait API).
		drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := operations.Wait(drainCtx); err != nil {
			logger.Warn("operations workers did not finish in time",
				"err", err, "active", operations.Active())
		}
	}()

	go func() {
		if err := internalSrv.Serve(internalListener); err != nil {
			logger.Error("internal grpc server stopped", "err", err)
		}
	}()

	serveErr := grpcSrv.Serve(listener)
	// Блокируемся до полного drain'а LRO worker'ов перед возвратом из runServe
	// (иначе main → os.Exit обрывает worker'ов).
	<-shutdownDone
	return serveErr
}

func runMigrate(cfg config.Config, direction string) {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("goose dialect: %v", err)
	}

	db, err := sql.Open("pgx", cfg.DSN())
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
