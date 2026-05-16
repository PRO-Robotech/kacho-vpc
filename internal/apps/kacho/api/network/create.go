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
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
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
// Wave 5 pilot (KAC-94, skill evgeniy §6 G.5): worker открывает Writer-TX
// явно через `repo.Writer(ctx)`. Insert + outbox-emit идут в одной TX
// (атомарность гарантирована pgx-уровнем) — никакого dual-write.
//
// Default-SG creation остаётся inline в worker'е через явный builder
// `domain.NewDefaultSecurityGroup` (§4 D.7, §7 I.9). KAC-94 caveat: SG-repo
// пока не CQRS, поэтому inline default-SG creation идёт через legacy
// `SecurityGroupRepo.Insert` ВНЕ writer-TX. После Insert(Network) writer
// коммитится; затем второй writer (закрытый TX-scope) пишет SG через legacy
// repo (свой TX внутри). Финальный link (`UPDATE networks SET default_sg_id`)
// — третий writer. Это **известное расхождение** с целью CQRS (atomic emit) —
// будет решено в replicate-фазе после переноса SG-repo на CQRS.
type CreateNetworkUseCase struct {
	repo         Repo
	folderClient FolderClient
	opsRepo      operations.Repo
	// sgRepo: nil → default-SG inline creation отключена (флаг
	// `KACHO_VPC_DEFAULT_SG_INLINE=false`). См. composition root в cmd/vpc/main.go.
	sgRepo SecurityGroupRepo
}

// NewCreateNetworkUseCase создаёт CreateNetworkUseCase.
func NewCreateNetworkUseCase(r Repo, folderClient FolderClient, opsRepo operations.Repo, sgRepo SecurityGroupRepo) *CreateNetworkUseCase {
	return &CreateNetworkUseCase{
		repo:         r,
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
	if err := n.Validate(); err != nil {
		return nil, err
	}
	if err := checkFolderExists(ctx, u.folderClient, n.FolderID); err != nil {
		return nil, err
	}
	name := string(n.Name)
	if name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		existing, _, lerr := rd.Networks().List(ctx, NetworkFilter{FolderID: n.FolderID, Name: name}, Pagination{})
		_ = rd.Close()
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
//
// CQRS Wave 5: Insert + outbox-emit идут в одной TX через writer.
func (u *CreateNetworkUseCase) doCreate(ctx context.Context, netID string, n domain.Network) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, n.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", n.FolderID)
	}

	n.ID = netID

	// CQRS write-TX: Insert + outbox-emit атомарны.
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.Networks().Insert(ctx, &n)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", created.ID, "CREATED", networkPayloadMap(created)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", ports.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}

	// Default-SG creation. KAC-94 caveat (см. doc-комментарий на UseCase): SG-repo
	// — пока legacy non-CQRS, поэтому inline default-SG creation идёт ВНЕ
	// writer-TX выше. SG-Insert использует свой TX через legacy repo, эмитит
	// SecurityGroup.CREATED outbox-event сам. Финальный link `UPDATE
	// networks.default_sg_id` — второй writer-TX ниже. Это **известное
	// расхождение** с целью atomic emit; решается в replicate-фазе (CQRS для SG).
	if u.sgRepo != nil {
		sg := domain.NewDefaultSecurityGroup(created.Network)
		createdSG, sgErr := u.sgRepo.Insert(ctx, &sg)
		if sgErr != nil {
			// SG creation failed — Network уже создан. Log warn, не падаем
			// (admin может создать default SG руками через public API).
			return marshalNetworkRecord(created)
		}
		// Bind SG как default. Второй CQRS-writer (Update + outbox-emit
		// атомарны).
		created.DefaultSecurityGroupID = createdSG.ID
		w2, werr := u.repo.Writer(ctx)
		if werr != nil {
			return marshalNetworkRecord(created)
		}
		defer w2.Abort()
		updated, uerr := w2.Networks().Update(ctx, &created.Network)
		if uerr != nil {
			return marshalNetworkRecord(created)
		}
		if oerr := w2.Outbox().Emit(ctx, "Network", updated.ID, "UPDATED", networkPayloadMap(updated)); oerr != nil {
			return marshalNetworkRecord(created)
		}
		if cerr := w2.Commit(); cerr != nil {
			return marshalNetworkRecord(created)
		}
		return marshalNetworkRecord(updated)
	}
	return marshalNetworkRecord(created)
}
