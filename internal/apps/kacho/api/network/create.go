package network

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// CreateInput — параметры для CreateNetworkUseCase.Execute. Использует
// `domain.Network` как «несущий» носитель данных, как требует skill evgeniy §2
// B.3 (не плодить параллельные XxxReq, дублирующие domain). Поле `n.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixNetwork)`.
type CreateInput struct {
	Network domain.Network
}

// CreateNetworkUseCase инициирует создание Network. Sync-проверки (folder
// exists, name unique) выполняются ДО создания Operation — клиент получает
// fast-fail gRPC-status, а не «200 + операция, упавшая через секунду» (см.
// kacho-vpc#8). Async-часть (`doCreate`) — атомарный backstop через FK/UNIQUE.
//
// Wave 3a pilot (skill evgeniy §2 B.1): бывший `NetworkService.Create` +
// `NetworkService.doCreate` переехал сюда. Default-SG creation остаётся inline
// в worker'е через явный builder `domain.NewDefaultSecurityGroup` (§4 D.7, §7
// I.9 — отдельная builder-функция, а не magic-литерал «default-sg-» + hardcoded
// status «ACTIVE»).
type CreateNetworkUseCase struct {
	repo         NetworkRepo
	folderClient FolderClient
	opsRepo      operations.Repo
	// sgRepo: nil → default-SG inline creation отключена (флаг
	// `KACHO_VPC_DEFAULT_SG_INLINE=false`). См. composition root в cmd/vpc/main.go.
	sgRepo SecurityGroupRepo
}

// NewCreateNetworkUseCase создаёт CreateNetworkUseCase.
func NewCreateNetworkUseCase(repo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo, sgRepo SecurityGroupRepo) *CreateNetworkUseCase {
	return &CreateNetworkUseCase{
		repo:         repo,
		folderClient: folderClient,
		opsRepo:      opsRepo,
		sgRepo:       sgRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а. Возвращает
// созданный Operation указателем (caller'у нужен он для `OperationService.Get`).
func (u *CreateNetworkUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	n := in.Network
	if n.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	// Domain-self-validation (skill evgeniy §4 D.5 / AP-1): NameVPC / Description /
	// Labels валидируются через newtypes — service-слой больше НЕ зовёт corevalidate.*.
	if err := n.Validate(); err != nil {
		return nil, err
	}
	if err := checkFolderExists(ctx, u.folderClient, n.FolderID); err != nil {
		return nil, err
	}
	name := string(n.Name)
	if name != "" {
		existing, _, lerr := u.repo.List(ctx, NetworkFilter{FolderID: n.FolderID, Name: name}, Pagination{})
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Network with name %s already exists", name)
		}
	}

	netID := ids.NewID(ids.PrefixNetwork)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create network %s", name),
		&vpcv1.CreateNetworkMetadata{NetworkId: netID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, netID, n)
	})

	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// folder-exists + Insert (FK ограничения / UNIQUE-нарушения); inline default-SG
// creation (builder из domain), затем link через UPDATE networks.default_sg_id.
func (u *CreateNetworkUseCase) doCreate(ctx context.Context, netID string, n domain.Network) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, n.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", n.FolderID)
	}

	n.ID = netID
	created, err := u.repo.Insert(ctx, &n)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	// Inline default-SG. Skill evgeniy §4 D.7 / AP-2 — domain-builder вместо
	// magic-литерала с hardcoded status «ACTIVE» / name «default-sg-{id[:8]}».
	if u.sgRepo != nil {
		sg := domain.NewDefaultSecurityGroup(created.Network)
		createdSG, sgErr := u.sgRepo.Insert(ctx, &sg)
		if sgErr != nil {
			// SG creation failed — Network уже создан. Log warn, не падаем
			// (admin может создать default SG руками через public API).
			return marshalNetworkRecord(created)
		}
		// Bind SG как default через NetworkRepo.Update.
		created.DefaultSecurityGroupID = createdSG.ID
		updated, uerr := u.repo.Update(ctx, &created.Network)
		if uerr == nil {
			return marshalNetworkRecord(updated)
		}
		// Update failed — возвращаем без bind'а (orphan SG, admin зачистит).
		return marshalNetworkRecord(created)
	}
	return marshalNetworkRecord(created)
}
