// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// CreateGatewayUseCase инициирует создание Gateway. Sync-проверки формата входа
// выполняются ДО создания Operation — клиент получает fast-fail gRPC-status, а не
// «200 + операция, упавшая через секунду». Async-часть (`doCreate`) — атомарный
// backstop через FK.
//
// Worker открывает одну Writer-TX, делает Insert(Gateway) + outbox emit и Commit:
// либо все видно, либо ничего — окно orphan-Gateway / потерянного outbox-event'а
// закрыто.
type CreateGatewayUseCase struct {
	repo          Repo
	projectClient ProjectClient
	opsRepo       operations.Repo
}

// NewCreateGatewayUseCase создает CreateGatewayUseCase.
func NewCreateGatewayUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo) *CreateGatewayUseCase {
	return &CreateGatewayUseCase{repo: r, projectClient: projectClient, opsRepo: opsRepo}
}

// Execute — sync-валидация + create Operation + запуск worker'а. Возвращает
// созданный Operation указателем (caller'у нужен он для `OperationService.Get`).
//
// Принимает `domain.Gateway` напрямую, без обертки-DTO. Поле `g.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixGateway)`.
func (u *CreateGatewayUseCase) Execute(ctx context.Context, g domain.Gateway) (*operations.Operation, error) {
	if g.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	name := string(g.Name)
	// Gateway.Name — строгий regex (lowercase, без uppercase/underscore).
	if err := corevalidate.NameGateway("name", name); err != nil {
		return nil, err
	}
	// Domain self-validation для description/labels.
	if err := serviceerr.FromValidation(g.Validate()); err != nil {
		return nil, err
	}
	// gateway-type oneof обязателен. Сейчас единственный тип — shared_egress
	// (SharedEgressGatewaySpec).
	if g.GatewayType != domain.GatewayTypeSharedEgress {
		return nil, status.Error(codes.InvalidArgument, "Illegal argument gateway")
	}

	// Sync project.Exists precheck тут не делаем — он race-prone: между sync-проверкой
	// и async-частью project может быть удален peer-сервисом, и second-writer-wins
	// безусловно создал бы ресурс. NotFound возвращается через `operation.error` из
	// async `doCreate`. Имена Gateway НЕ уникальны — name-uniqueness тут не проверяем
	// (в отличие от Network/Subnet/RouteTable/SecurityGroup).

	gwID := ids.NewID(ids.PrefixGateway)
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create gateway %s", name),
		&vpcv1.CreateGatewayMetadata{GatewayId: gwID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, gwID, g)
	})

	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// project-exists + Insert (FK / UNIQUE-нарушения).
//
// ВСЕ в одной writer-TX: Insert(Gateway) + outbox emit Gateway.CREATED ходят через
// ту же pgx.Tx writer'а, поэтому либо оба видны (Commit), либо ни один (Abort/crash).
func (u *CreateGatewayUseCase) doCreate(ctx context.Context, gwID string, g domain.Gateway) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, g.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", g.ProjectID)
	}

	gtype := g.GatewayType
	if gtype == "" {
		gtype = domain.GatewayTypeSharedEgress
	}
	g.ID = gwID
	g.GatewayType = gtype

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.Gateways().Insert(ctx, &g)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "Gateway", created.ID, "CREATED", helpers.DomainToMap(created)); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	// Записываем INTENT hierarchy-tuple vpc_gateway→project в той же writer-TX,
	// чтобы register-намерение было атомарно с Insert и не терялось при ошибке.
	// В mirror-feed несем labels Gateway + parent_project_id (ProjectHierarchyItem),
	// а не голый tuple — иначе resource_mirror в kacho-iam остается без labels и
	// ARM_LABELS-селектор не матчит даже свежесозданный Gateway. Симметрично
	// network/subnet/securitygroup create.
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
		fgaregister.ProjectHierarchyItem(string(g.ProjectID), "vpc_gateway", created.ID,
			domain.LabelsToMap(created.Labels)),
	)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalGatewayRecord(created)
}
