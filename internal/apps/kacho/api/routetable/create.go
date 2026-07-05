// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// CreateRouteTableUseCase инициирует создание RouteTable. Sync-проверки (parent
// network exists, name unique, static_routes валидны) выполняются ДО создания
// Operation — клиент получает fast-fail gRPC-status, а не «200 + операция,
// упавшая через секунду». Async-часть (`doCreate`) — атомарный backstop через FK/UNIQUE.
//
// Worker открывает Writer-TX и делает в ней Insert(RouteTable) + outbox-emit
// CREATED атомарно. Auto-association DB-trigger дополнительно эмитит
// `Subnet.UPDATED` в той же tx-области — это часть Commit'а единой writer-TX.
type CreateRouteTableUseCase struct {
	repo          Repo
	projectClient ProjectClient
	opsRepo       operations.Repo
	registrar     fgaregister.Registrar
}

// WithRegistrar подключает синхронный owner-tuple registrar (Decision 2): после
// commit RouteTable owner-tuple синхронно регистрируется в kacho-iam. Nil →
// sync-путь пропускается (только async drainer).
func (u *CreateRouteTableUseCase) WithRegistrar(r fgaregister.Registrar) *CreateRouteTableUseCase {
	u.registrar = r
	return u
}

// NewCreateRouteTableUseCase создает CreateRouteTableUseCase.
func NewCreateRouteTableUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo) *CreateRouteTableUseCase {
	return &CreateRouteTableUseCase{
		repo:          r,
		projectClient: projectClient,
		opsRepo:       opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
//
// Принимает `domain.RouteTable` напрямую (без тривиальной input-обертки). Поле
// `rt.ID` на входе пустое — назначаем внутри use-case'а через
// `ids.NewID(ids.PrefixRouteTable)`.
func (u *CreateRouteTableUseCase) Execute(ctx context.Context, rt domain.RouteTable) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, rt.NetworkID); err != nil {
		return nil, err
	}
	if rt.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if rt.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// Domain self-validation.
	if err := serviceerr.FromValidation(rt.Validate()); err != nil {
		return nil, err
	}
	if err := validateStaticRoutes(rt.StaticRoutes); err != nil {
		return nil, err
	}

	// Sync project.Exists precheck здесь не делаем — он race-prone: между
	// sync-проверкой и async-частью project может быть удален peer-сервисом, и
	// тогда ресурс создался бы безусловно. NotFound для project возвращается через
	// `operation.error` из async `doCreate`. Sync uniqueness/network-existence
	// (по DB-state в той же сервис-БД) остаются — они race-free относительно peer'ов.
	// Existence parent Network через CQRS Reader.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if _, gerr := rd.Networks().Get(ctx, rt.NetworkID); gerr != nil {
		_ = rd.Close()
		if errors.Is(gerr, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", rt.NetworkID)
		}
		return nil, serviceerr.MapRepoErr(gerr)
	}
	// Uniqueness (project_id, name) — partial UNIQUE WHERE name<>'' покрывает на
	// DB-уровне. Sync-precheck для fast-fail UX.
	name := string(rt.Name)
	if name != "" {
		existing, _, lerr := rd.RouteTables().List(ctx, RouteTableFilter{ProjectID: rt.ProjectID, Name: name}, Pagination{})
		if lerr != nil {
			_ = rd.Close()
			return nil, serviceerr.MapRepoErr(lerr)
		}
		if len(existing) > 0 {
			_ = rd.Close()
			return nil, status.Errorf(codes.AlreadyExists, "RouteTable with name %s already exists", name)
		}
	}
	_ = rd.Close()

	rtID := ids.NewID(ids.PrefixRouteTable)
	op, err := operations.NewFromContext(
		ctx,
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
//   - project-exists peer-API
//   - Writer-TX: Insert(RouteTable) + outbox-emit RouteTable.CREATED
//
// Auto-association trigger внутри Postgres сразу после INSERT route_tables
// перебирает `subnets WHERE network_id = NEW.network_id AND route_table_id IS NULL`
// и проставляет им `route_table_id = NEW.id`; сопутствующие `Subnet.UPDATED`
// события записываются в outbox триггером — все в одной БД-TX, commit'ится
// атомарно с нашим Insert + outbox-emit.
func (u *CreateRouteTableUseCase) doCreate(ctx context.Context, rtID string, rt domain.RouteTable) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, rt.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", rt.ProjectID)
	}

	rt.ID = rtID

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// Parent Network existence — повторная проверка внутри writer-TX (FK ниже —
	// атомарный backstop). FK route_tables.network_id → networks(id) дает
	// 23503 если parent исчез между sync-check и Insert.
	if _, gerr := w.Networks().Get(ctx, rt.NetworkID); gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", rt.NetworkID)
		}
		return nil, serviceerr.MapRepoErr(gerr)
	}

	created, err := w.RouteTables().Insert(ctx, &rt)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "RouteTable", created.ID, "CREATED", helpers.RouteTablePayload(created)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Записываем INTENT на owner-hierarchy-tuple vpc_route_table→project в той же
	// writer-TX — at-least-once через transactional-outbox, без best-effort-потери
	// при ошибке. В mirror-feed несем labels RouteTable + parent_project_id
	// (ProjectHierarchyItem), а не голый tuple — иначе resource_mirror в kacho-iam
	// остается без labels и ARM_LABELS-селектор не матчит даже свежесозданную
	// RouteTable. Симметрично network/subnet/securitygroup create.
	items := []fgaregister.Item{
		fgaregister.ProjectHierarchyItem(string(rt.ProjectID), "vpc_route_table", created.ID,
			domain.LabelsToMap(created.Labels)),
	}
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(items...)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	// Sync-primary owner-tuple registration (после durable commit); fail-closed.
	if u.registrar != nil {
		if err := u.registrar.Register(ctx, items); err != nil {
			return nil, err
		}
	}
	return marshalRouteTableRecord(created)
}
