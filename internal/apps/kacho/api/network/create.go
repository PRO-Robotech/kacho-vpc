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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
// Wave 5 pilot (KAC-94, skill evgeniy §6 G.5 / §7 I.9 / I.10): worker открывает
// ОДНУ Writer-TX и делает в ней Insert(Network) → Insert(SG, default) →
// SetDefaultSGID(Network, sg.ID) с тремя outbox-emit'ами. Либо все три DML
// видны (Commit), либо ни один (Abort/crash) — orphan-SG window прежней
// three-TX-схемы закрыт.
//
// Default-SG creation управляется флагом `defaultSGInline` (раньше — через
// `if sgRepo != nil`-shim; теперь явный bool, видный в композиции). При
// `defaultSGInline=false` worker создаёт только Network — admin может
// досоздать default SG через public API.
type CreateNetworkUseCase struct {
	repo            Repo
	folderClient    FolderClient
	opsRepo         operations.Repo
	defaultSGInline bool // KACHO_VPC_DEFAULT_SG_INLINE
}

// NewCreateNetworkUseCase создаёт CreateNetworkUseCase. defaultSGInline берётся
// из конфига (`cfg.Network.DefaultSGInline`) — при true в одной writer-TX
// создаётся default SG и Network.default_security_group_id заполняется.
func NewCreateNetworkUseCase(r Repo, folderClient FolderClient, opsRepo operations.Repo, defaultSGInline bool) *CreateNetworkUseCase {
	return &CreateNetworkUseCase{
		repo:            r,
		folderClient:    folderClient,
		opsRepo:         opsRepo,
		defaultSGInline: defaultSGInline,
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
// creation (builder из domain), затем link через SetDefaultSGID(Network, sg.ID).
//
// Wave 5 batch 33/34 (KAC-94, skill evgeniy I.9 / I.10): ВСЁ в одной writer-TX.
// Liver предыдущая three-TX-схема (Network commit → SG commit → Network UPDATE
// commit) ломалась на crash между шагами: либо orphan SG, либо Network без
// default_sg_id, либо забытый outbox-event. Теперь:
//
//	w := u.repo.Writer(ctx)            // открыли единую TX
//	created := w.Networks().Insert     // Network.CREATED outbox
//	(if inline) sgRec := w.SGs().Insert + w.Networks().SetDefaultSGID
//	            + SG.CREATED outbox + Network.UPDATED outbox
//	w.Commit()                         // либо всё, либо ничего (Abort/crash)
//
// FK Network.default_security_group_id → security_groups(id) `ON DELETE SET NULL`
// (см. squashed initial migration). SG-FK на network_id — RESTRICT, но в одной
// TX это нормально: Insert(SG) ссылается на только что вставленный Network в
// той же tx (видимость G.2 + Postgres deferred constraint check на коммите для
// non-deferrable — INSERT(child) после INSERT(parent) в одной TX проходит).
func (u *CreateNetworkUseCase) doCreate(ctx context.Context, netID string, n domain.Network) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, n.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", n.FolderID)
	}

	n.ID = netID

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
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}

	finalRec := created
	if u.defaultSGInline {
		sg := domain.NewDefaultSecurityGroup(created.Network)
		sgRec, sgErr := w.SecurityGroups().Insert(ctx, &sg)
		if sgErr != nil {
			return nil, mapRepoErr(sgErr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", sgRec.ID, "CREATED", securityGroupPayloadMap(sgRec)); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		upd, uerr := w.Networks().SetDefaultSGID(ctx, created.ID, sgRec.ID)
		if uerr != nil {
			return nil, mapRepoErr(uerr)
		}
		if oerr := w.Outbox().Emit(ctx, "Network", upd.ID, "UPDATED", networkPayloadMap(upd)); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		finalRec = upd
	}

	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalNetworkRecord(finalRec)
}
