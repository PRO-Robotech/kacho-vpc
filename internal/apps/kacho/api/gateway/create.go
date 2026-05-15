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
)

// CreateInput — параметры для CreateGatewayUseCase.Execute. Использует
// `domain.Gateway` как «несущий» носитель данных, как требует skill evgeniy §2
// B.3 (не плодить параллельные XxxReq, дублирующие domain). Поле `g.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixGateway)`.
type CreateInput struct {
	Gateway domain.Gateway
}

// CreateGatewayUseCase инициирует создание Gateway. Sync-проверки (folder
// exists, name validation) выполняются ДО создания Operation — клиент получает
// fast-fail gRPC-status, а не «200 + операция, упавшая через секунду» (см.
// kacho-vpc#8). Async-часть (`doCreate`) — атомарный backstop через FK.
//
// Wave 3b (skill evgeniy §2 B.1): бывший `GatewayService.Create` +
// `GatewayService.doCreate` переехал сюда.
type CreateGatewayUseCase struct {
	repo         GatewayRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewCreateGatewayUseCase создаёт CreateGatewayUseCase.
func NewCreateGatewayUseCase(repo GatewayRepo, folderClient FolderClient, opsRepo operations.Repo) *CreateGatewayUseCase {
	return &CreateGatewayUseCase{repo: repo, folderClient: folderClient, opsRepo: opsRepo}
}

// Execute — sync-валидация + create Operation + запуск worker'а. Возвращает
// созданный Operation указателем (caller'у нужен он для `OperationService.Get`).
func (u *CreateGatewayUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	g := in.Gateway
	if g.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
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

	// Verbatim YC: folder existence — sync precondition до Operation (async-проверка
	// в doCreate остаётся backstop'ом). NB: имена Gateway в YC НЕ уникальны (probe
	// 2026-05-11) — name-uniqueness тут НЕ проверяем (в отличие от Network/Subnet/RT/SG).
	if err := checkFolderExists(ctx, u.folderClient, g.FolderID); err != nil {
		return nil, err
	}

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
// folder-exists + Insert.
func (u *CreateGatewayUseCase) doCreate(ctx context.Context, gwID string, g domain.Gateway) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, g.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", g.FolderID)
	}

	gtype := g.GatewayType
	if gtype == "" {
		gtype = domain.GatewayTypeSharedEgress
	}
	g.ID = gwID
	g.GatewayType = gtype
	created, err := u.repo.Insert(ctx, &g)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalGatewayRecord(created)
}
