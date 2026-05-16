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
	"sync"
	"syscall"
	"time"

	"github.com/H-BF/corlib/pkg/parallel"
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

	addressapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	gatewayapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/gateway"
	networkapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/network"
	niapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/networkinterface"
	peapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/privateendpoint"
	routetableapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/routetable"
	sgapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/securitygroup"
	subnetapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/subnet"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addressref"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
	addressPool             *addresspool.AddressPoolService
	networkInternal         *networkinternal.Service
	networkInterfaceHandler *niapp.Handler
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
	err = parallel.ExecAbstract(len(tasks), int32(len(tasks)-1), func(i int) error {
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
func buildServices(pool *pgxpool.Pool, folderClient repo.FolderClient, geoClient repo.ZoneRegistry, opsRepo operations.Repo, cfg config.Config, logger *slog.Logger) *services {
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

	if !cfg.Network.DefaultSGInline {
		logger.Warn("network.default-sg-inline=false — Network.Create НЕ создаёт default SG")
	}

	addressPoolSvc := addresspool.NewAddressPoolService(addressPoolRepo, addressPoolBindingRepo, cloudPoolSelectorRepo, addressRepo, networkRepo, subnetRepo, folderClient, geoClient)
	addressRefSvc := addressref.NewService(addressRepo)

	// Wave 5 pilot (KAC-94, skill evgeniy §6 G.1-G.7): Network use-case'ы
	// работают через CQRS-Repository (Reader / Writer split). pgxpool-impl —
	// `internal/repo/kacho/pg`. Остальные 7 ресурсов продолжают работать
	// через legacy `*repo.NetworkRepo` etc. — replicate-фаза (отдельный
	// эпик-subtask).
	kachoRepo := kachopg.New(pool)

	// Wave 3a + Wave 5 pilot: каждый use-case инжектируется в Handler.
	// kachoRepo используется Network'ом + SG (Wave 5 batch 33/34, KAC-94: SG
	// переехал на CQRS). Subnet/RT — пока legacy.
	// defaultSGInline=true (default) — при Network.Create в одной writer-TX
	// создаётся inline default SG и Network.default_security_group_id
	// заполняется атомарно.
	netCreateUC := networkapp.NewCreateNetworkUseCase(kachoRepo, folderClient, opsRepo, cfg.Network.DefaultSGInline)
	netUpdateUC := networkapp.NewUpdateNetworkUseCase(kachoRepo, opsRepo)
	netDeleteUC := networkapp.NewDeleteNetworkUseCase(kachoRepo, subnetRepo, routeTableRepo, sgRepo, opsRepo)
	netMoveUC := networkapp.NewMoveNetworkUseCase(kachoRepo, folderClient, opsRepo)
	netGetUC := networkapp.NewGetNetworkUseCase(kachoRepo)
	netListUC := networkapp.NewListNetworksUseCase(kachoRepo)
	netListSubUC := networkapp.NewListSubnetsUseCase(kachoRepo, subnetRepo)
	netListSGUC := networkapp.NewListSecurityGroupsUseCase(kachoRepo, sgRepo)
	netListRTUC := networkapp.NewListRouteTablesUseCase(kachoRepo, routeTableRepo)
	netListOpsUC := networkapp.NewListOperationsUseCase(opsRepo)
	netHandler := networkapp.NewHandler(
		netCreateUC, netUpdateUC, netDeleteUC, netMoveUC,
		netGetUC, netListUC, netListSubUC, netListSGUC, netListRTUC, netListOpsUC,
	)

	// Wave 3b (skill evgeniy §2): Gateway / PrivateEndpoint / RouteTable —
	// use-case-структура. Replicate Wave 3a pilot шаблона.
	gwHandler := gatewayapp.NewHandler(
		gatewayapp.NewCreateGatewayUseCase(gatewayRepo, folderClient, opsRepo),
		gatewayapp.NewUpdateGatewayUseCase(gatewayRepo, opsRepo),
		gatewayapp.NewDeleteGatewayUseCase(gatewayRepo, opsRepo),
		gatewayapp.NewMoveGatewayUseCase(gatewayRepo, folderClient, opsRepo),
		gatewayapp.NewGetGatewayUseCase(gatewayRepo),
		gatewayapp.NewListGatewaysUseCase(gatewayRepo),
		gatewayapp.NewListOperationsUseCase(opsRepo),
	)

	peHandler := peapp.NewHandler(
		peapp.NewCreatePrivateEndpointUseCase(peRepo, networkRepo, subnetRepo, folderClient, opsRepo),
		peapp.NewUpdatePrivateEndpointUseCase(peRepo, opsRepo),
		peapp.NewDeletePrivateEndpointUseCase(peRepo, opsRepo),
		peapp.NewGetPrivateEndpointUseCase(peRepo),
		peapp.NewListPrivateEndpointsUseCase(peRepo),
		peapp.NewListOperationsUseCase(opsRepo),
	)

	rtHandler := routetableapp.NewHandler(
		routetableapp.NewCreateRouteTableUseCase(routeTableRepo, networkRepo, folderClient, opsRepo),
		routetableapp.NewUpdateRouteTableUseCase(routeTableRepo, opsRepo),
		routetableapp.NewDeleteRouteTableUseCase(routeTableRepo, opsRepo),
		routetableapp.NewMoveRouteTableUseCase(routeTableRepo, folderClient, opsRepo),
		routetableapp.NewGetRouteTableUseCase(routeTableRepo),
		routetableapp.NewListRouteTablesUseCase(routeTableRepo),
		routetableapp.NewListOperationsUseCase(opsRepo),
	)

	// Wave 3 (skill evgeniy §2): Subnet — use-case-структура с 11 use-case'ами
	// включая специфические AddCidrBlocks / RemoveCidrBlocks / Relocate /
	// ListUsedAddresses.
	subnetHandler := subnetapp.NewHandler(
		subnetapp.NewCreateSubnetUseCase(subnetRepo, networkRepo, folderClient, geoClient, opsRepo),
		subnetapp.NewUpdateSubnetUseCase(subnetRepo, opsRepo),
		subnetapp.NewDeleteSubnetUseCase(subnetRepo, niRepo, opsRepo),
		subnetapp.NewMoveSubnetUseCase(subnetRepo, folderClient, opsRepo),
		subnetapp.NewGetSubnetUseCase(subnetRepo),
		subnetapp.NewListSubnetsUseCase(subnetRepo),
		subnetapp.NewAddCidrBlocksUseCase(subnetRepo, opsRepo),
		subnetapp.NewRemoveCidrBlocksUseCase(subnetRepo, opsRepo),
		subnetapp.NewRelocateUseCase(subnetRepo, geoClient),
		subnetapp.NewListUsedAddressesUseCase(subnetRepo, addressRepo),
		subnetapp.NewListOperationsUseCase(opsRepo),
	)

	// Wave 3 (skill evgeniy §2): Address — use-case-структура. Composition с
	// AddressPoolService для IPAM cascade resolve. Internal Allocate UC отделён —
	// принимается InternalAddressAllocateHandler через узкий port.
	addressCreateUC := addressapp.NewCreateAddressUseCase(addressRepo, subnetRepo, folderClient, opsRepo, addressPoolSvc)
	addressUpdateUC := addressapp.NewUpdateAddressUseCase(addressRepo, opsRepo)
	addressDeleteUC := addressapp.NewDeleteAddressUseCase(addressRepo, opsRepo)
	addressMoveUC := addressapp.NewMoveAddressUseCase(addressRepo, folderClient, opsRepo)
	addressGetUC := addressapp.NewGetAddressUseCase(addressRepo)
	addressGetByValueUC := addressapp.NewGetByValueUseCase(addressRepo)
	addressListUC := addressapp.NewListAddressesUseCase(addressRepo)
	addressListBySubnetUC := addressapp.NewListBySubnetUseCase(addressRepo, subnetRepo)
	addressListOpsUC := addressapp.NewListOperationsUseCase(opsRepo)
	addressAllocateUC := addressapp.NewAllocateUseCase(addressRepo, subnetRepo, addressPoolSvc)
	addressHandler := addressapp.NewHandler(
		addressCreateUC, addressUpdateUC, addressDeleteUC, addressMoveUC,
		addressGetUC, addressGetByValueUC, addressListUC, addressListBySubnetUC, addressListOpsUC,
		nil, // SubnetAuthZ опционален — пока nil
	)

	// Wave 3 (skill evgeniy §2): SecurityGroup — use-case-структура. Split-endpoint
	// Update / UpdateRules / UpdateRule (OCC через xmin в repo).
	sgHandler := sgapp.NewHandler(
		sgapp.NewCreateSecurityGroupUseCase(sgRepo, networkRepo, folderClient, opsRepo),
		sgapp.NewUpdateSecurityGroupUseCase(sgRepo, opsRepo),
		sgapp.NewUpdateRulesUseCase(sgRepo, opsRepo),
		sgapp.NewUpdateRuleUseCase(sgRepo, opsRepo),
		sgapp.NewDeleteSecurityGroupUseCase(sgRepo, opsRepo),
		sgapp.NewMoveSecurityGroupUseCase(sgRepo, folderClient, opsRepo),
		sgapp.NewGetSecurityGroupUseCase(sgRepo),
		sgapp.NewListSecurityGroupsUseCase(sgRepo),
		sgapp.NewListOperationsUseCase(sgRepo, opsRepo),
	)

	// Wave 3 (skill evgeniy §2): NetworkInterface — use-case-структура. Replicate
	// Wave 3a pilot шаблона. У NIC нет Move RPC (NIC привязан к Subnet), но есть
	// специфические AttachToInstance / DetachFromInstance с atomic CAS (KAC-52).
	niHandler := niapp.NewHandler(
		niapp.NewCreateNetworkInterfaceUseCase(niRepo, subnetRepo, addressRepo, folderClient, opsRepo),
		niapp.NewUpdateNetworkInterfaceUseCase(niRepo, addressRepo, opsRepo),
		niapp.NewDeleteNetworkInterfaceUseCase(niRepo, addressRepo, opsRepo),
		niapp.NewGetNetworkInterfaceUseCase(niRepo),
		niapp.NewListNetworkInterfacesUseCase(niRepo),
		niapp.NewAttachToInstanceUseCase(niRepo, opsRepo),
		niapp.NewDetachFromInstanceUseCase(niRepo, opsRepo),
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
		addressPool:             addressPoolSvc,
		networkInternal:         networkinternal.NewService(networkRepo, sgRepo),
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
	vpcv1.RegisterInternalAddressPoolServiceServer(srv, handler.NewInternalAddressPoolHandler(svcs.addressPool))
	vpcv1.RegisterInternalNetworkServiceServer(srv, handler.NewInternalNetworkHandler(svcs.networkInternal))
	vpcv1.RegisterInternalCloudServiceServer(srv, handler.NewInternalCloudHandler(svcs.addressPool))
}
