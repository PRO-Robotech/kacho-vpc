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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// CreateGatewayUseCase инициирует создание Gateway. Sync-проверки (folder
// exists) выполняются ДО создания Operation — клиент получает fast-fail
// gRPC-status, а не «200 + операция, упавшая через секунду» (см. kacho-vpc#8).
// Async-часть (`doCreate`) — атомарный backstop через FK.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5 / §2 B.1): worker открывает
// одну Writer-TX, делает Insert(Gateway) + outbox emit и Commit. Либо всё
// видно, либо ничего — orphan-Gateway / forgotten outbox-event window закрыт.
type CreateGatewayUseCase struct {
	repo         Repo
	projectClient ProjectClient
	opsRepo      operations.Repo
}

// NewCreateGatewayUseCase создаёт CreateGatewayUseCase.
func NewCreateGatewayUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo) *CreateGatewayUseCase {
	return &CreateGatewayUseCase{repo: r, projectClient: projectClient, opsRepo: opsRepo}
}

// Execute — sync-валидация + create Operation + запуск worker'а. Возвращает
// созданный Operation указателем (caller'у нужен он для `OperationService.Get`).
//
// Принимает `domain.Gateway` напрямую (KAC-94, skill evgeniy §2 B.3 / §7 I.1):
// тривиальная `CreateInput{Gateway: …}`-обёртка удалена — она лишь
// перепаковывала domain.X без дополнительного контекста. Поле `g.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixGateway)`.
func (u *CreateGatewayUseCase) Execute(ctx context.Context, g domain.Gateway) (*operations.Operation, error) {
	if g.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	name := string(g.Name)
	// Gateway.Name — strict regex (lowercase, без uppercase/underscore — verbatim YC).
	// Wave 2 batch B держал это в service-слое до появления RcNameGateway newtype.
	if err := corevalidate.NameGateway("name", name); err != nil {
		return nil, err
	}
	// Domain self-validation для description/labels (skill evgeniy §4 D.5 / AP-1).
	if err := g.Validate(); err != nil {
		return nil, err
	}
	// Verbatim YC (probe 2026-05-11): gateway-type oneof обязателен.
	// Сейчас единственный тип — shared_egress (SharedEgressGatewaySpec).
	if g.GatewayType != domain.GatewayTypeSharedEgress {
		return nil, status.Error(codes.InvalidArgument, "Illegal argument gateway")
	}

	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	// NB: имена Gateway в YC НЕ уникальны (probe 2026-05-11) — name-uniqueness тут
	// НЕ проверяем (в отличие от Network/Subnet/RT/SG).

	gwID := ids.NewID(ids.PrefixGateway)
	op, err := operations.New(
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
// folder-exists + Insert (FK / UNIQUE-нарушения).
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): ВСЁ в одной writer-TX.
// Insert(Gateway) + outbox emit Gateway.CREATED — оба ходят через ту же pgx.Tx
// writer'а, поэтому либо оба видны (Commit), либо ни один (Abort/crash).
func (u *CreateGatewayUseCase) doCreate(ctx context.Context, gwID string, g domain.Gateway) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, g.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", g.ProjectID)
	}

	gtype := g.GatewayType
	if gtype == "" {
		gtype = domain.GatewayTypeSharedEgress
	}
	g.ID = gwID
	g.GatewayType = gtype

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.Gateways().Insert(ctx, &g)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "Gateway", created.ID, "CREATED", gatewayPayloadMap(created)); oerr != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalGatewayRecord(created)
}
