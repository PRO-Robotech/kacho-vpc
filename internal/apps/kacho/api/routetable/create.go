package routetable

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// CreateRouteTableUseCase инициирует создание RouteTable. Sync-проверки (folder
// exists, parent network exists, name unique, static_routes валидны)
// выполняются ДО создания Operation — клиент получает fast-fail gRPC-status,
// а не «200 + операция, упавшая через секунду». Async-часть (`doCreate`) —
// атомарный backstop через FK/UNIQUE.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): worker открывает Writer-TX
// и делает в ней Insert(RouteTable) + outbox-emit CREATED атомарно. Auto-association
// DB-trigger (KAC-56) дополнительно эмитит `Subnet.UPDATED` события в той же
// tx-области — это часть Commit'а единой writer-TX.
type CreateRouteTableUseCase struct {
	repo         Repo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewCreateRouteTableUseCase создаёт CreateRouteTableUseCase.
func NewCreateRouteTableUseCase(r Repo, folderClient FolderClient, opsRepo operations.Repo) *CreateRouteTableUseCase {
	return &CreateRouteTableUseCase{
		repo:         r,
		folderClient: folderClient,
		opsRepo:      opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
//
// Принимает `domain.RouteTable` напрямую (KAC-94, skill evgeniy §2 B.3 / §7 I.1):
// тривиальная `CreateInput{RouteTable: …}`-обёртка удалена — она лишь
// перепаковывала domain.X без дополнительного контекста. Поле `rt.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixRouteTable)`.
func (u *CreateRouteTableUseCase) Execute(ctx context.Context, rt domain.RouteTable) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, rt.NetworkID); err != nil {
		return nil, err
	}
	if rt.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if rt.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// Domain self-validation (skill evgeniy §4 D.5 / AP-1).
	if err := rt.Validate(); err != nil {
		return nil, err
	}
	// RT-CIDR-VALIDATION.
	if err := validateStaticRoutes(rt.StaticRoutes); err != nil {
		return nil, err
	}

	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	// Sync uniqueness/network-existence-проверки (через DB-state в той же сервис-БД)
	// остаются — они race-free относительно peer-сервисов.
	// Existence parent Network через CQRS Reader.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if _, gerr := rd.Networks().Get(ctx, rt.NetworkID); gerr != nil {
		_ = rd.Close()
		if errors.Is(gerr, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", rt.NetworkID)
		}
		return nil, mapRepoErr(gerr)
	}
	// Uniqueness (folder_id, name) — partial UNIQUE WHERE name<>'' покрывает на
	// DB-уровне (миграция 0002). Sync-precheck для fast-fail UX.
	name := string(rt.Name)
	if name != "" {
		existing, _, lerr := rd.RouteTables().List(ctx, RouteTableFilter{FolderID: rt.FolderID, Name: name}, Pagination{})
		if lerr != nil {
			_ = rd.Close()
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			_ = rd.Close()
			return nil, status.Errorf(codes.AlreadyExists, "RouteTable with name %s already exists", name)
		}
	}
	_ = rd.Close()

	rtID := ids.NewID(ids.PrefixRouteTable)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create route table %s", name),
		&vpcv1.CreateRouteTableMetadata{RouteTableId: rtID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, rtID, rt)
	})

	return &op, nil
}

// doCreate — async-часть Create. Атомарный backstop:
//   - folder-exists peer-API
//   - Writer-TX: Insert(RouteTable) + outbox-emit RouteTable.CREATED
//
// Auto-association trigger (KAC-56, миграция 0019) внутри Postgres сразу
// после INSERT route_tables перебирает `subnets WHERE network_id = NEW.network_id
// AND route_table_id IS NULL` и проставляет им `route_table_id = NEW.id`;
// сопутствующие `Subnet.UPDATED` события записываются в outbox триггером —
// всё в одной БД-TX, commit'ится атомарно с нашим Insert + outbox-emit.
func (u *CreateRouteTableUseCase) doCreate(ctx context.Context, rtID string, rt domain.RouteTable) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, rt.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", rt.FolderID)
	}

	rt.ID = rtID

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	// Parent Network existence — повторная проверка внутри writer-TX (FK ниже —
	// атомарный backstop). FK route_tables.network_id → networks(id) даёт
	// 23503 если parent исчез между sync-check и Insert.
	if _, gerr := w.Networks().Get(ctx, rt.NetworkID); gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", rt.NetworkID)
		}
		return nil, mapRepoErr(gerr)
	}

	created, err := w.RouteTables().Insert(ctx, &rt)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "RouteTable", created.ID, "CREATED", repo.RouteTablePayload(created)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalRouteTableRecord(created)
}
