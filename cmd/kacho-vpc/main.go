package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/observability"
	"github.com/PRO-Robotech/kacho-corelib/operations"

	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: kacho-vpc {serve|migrate up|migrate down|migrate status}")
	}
	cmd := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	switch cmd {
	case "migrate":
		if len(os.Args) < 3 {
			log.Fatal("usage: kacho-vpc migrate {up|down|status}")
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

	pool, err := coredb.NewPool(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	// Operations repo (каждый сервис — своя схема, используем "public").
	opsRepo := operations.NewRepo(pool, "public")

	// Подключение к resource-manager для кросс-сервисной валидации Folder.
	rmConn, err := grpc.NewClient(cfg.ResourceManagerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer rmConn.Close()

	folderClient := clients.NewFolderClient(rmConn)

	// Репозитории.
	networkRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	sgRepo := repo.NewSecurityGroupRepo(pool)
	rtRepo := repo.NewRouteTableRepo(pool)
	addrRepo := repo.NewAddressRepo(pool)

	// Сервисы.
	networkSvc := service.NewNetworkService(networkRepo, opsRepo, folderClient)
	subnetSvc := service.NewSubnetService(subnetRepo, networkRepo, opsRepo, folderClient)
	sgSvc := service.NewSecurityGroupService(sgRepo, networkRepo, opsRepo, folderClient)
	rtSvc := service.NewRouteTableService(rtRepo, networkRepo, opsRepo, folderClient)
	addrSvc := service.NewAddressService(addrRepo, opsRepo, folderClient)

	// gRPC сервер.
	grpcSrv := grpcsrv.NewServer()
	pb.RegisterNetworkServiceServer(grpcSrv, handler.NewNetworkHandler(networkSvc))
	pb.RegisterSubnetServiceServer(grpcSrv, handler.NewSubnetHandler(subnetSvc))
	pb.RegisterSecurityGroupServiceServer(grpcSrv, handler.NewSecurityGroupHandler(sgSvc))
	pb.RegisterRouteTableServiceServer(grpcSrv, handler.NewRouteTableHandler(rtSvc))
	pb.RegisterAddressServiceServer(grpcSrv, handler.NewAddressHandler(addrSvc))

	listener, err := net.Listen("tcp", ":"+cfg.GrpcPort)
	if err != nil {
		return err
	}
	logger.Info("kacho-vpc listening", "port", cfg.GrpcPort)

	go func() {
		<-ctx.Done()
		grpcSrv.GracefulStop()
	}()

	return grpcSrv.Serve(listener)
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
