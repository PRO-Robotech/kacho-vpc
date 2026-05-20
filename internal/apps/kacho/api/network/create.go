package network

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgawrite"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

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
//
// Inline default-SG creation вынесена в отдельный `CreateDefaultSGUseCase`
// (см. `default_sg.go`) — skill evgeniy §7 I.9 (residual): explicit
// composition use-case'ов, а не fat inline. Atomic-семантика сохранена:
// composing use-case вызывается ВНУТРИ writer-TX `doCreate`, перед `Commit()`.
type CreateNetworkUseCase struct {
	repo            Repo
	projectClient   ProjectClient
	opsRepo         operations.Repo
	defaultSGInline bool // KACHO_VPC_DEFAULT_SG_INLINE
	createDefaultSG *CreateDefaultSGUseCase

	// fgaWriter / logger — KAC-127 issue #22: after the Network row is
	// committed, publish `vpc_network:<id>#project@project:<project_id>` so a
	// per-resource Get/Update/Delete Check resolves. nil → no-op.
	fgaWriter fgawrite.HierarchyTupleWriter
	logger    *slog.Logger
}

// NewCreateNetworkUseCase создаёт CreateNetworkUseCase. defaultSGInline берётся
// из конфига (`cfg.Network.DefaultSGInline`) — при true в одной writer-TX
// создаётся default SG (через композицию с `CreateDefaultSGUseCase`) и
// `Network.default_security_group_id` заполняется атомарно с Insert(Network).
func NewCreateNetworkUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo, defaultSGInline bool) *CreateNetworkUseCase {
	return &CreateNetworkUseCase{
		repo:            r,
		projectClient:   projectClient,
		opsRepo:         opsRepo,
		defaultSGInline: defaultSGInline,
		createDefaultSG: NewCreateDefaultSGUseCase(),
	}
}

// WithFGAWriter wires the OpenFGA hierarchy-tuple writer (KAC-127 issue #22).
// Without it a created Network has no `vpc_network:<id>#project@project` tuple
// and every per-resource Check is FGA `no path`.
func (u *CreateNetworkUseCase) WithFGAWriter(w fgawrite.HierarchyTupleWriter, logger *slog.Logger) *CreateNetworkUseCase {
	u.fgaWriter = w
	u.logger = logger
	return u
}

// Execute — sync-валидация + create Operation + запуск worker'а. Возвращает
// созданный Operation указателем (caller'у нужен он для `OperationService.Get`).
//
// Принимает `domain.Network` напрямую (KAC-94, skill evgeniy §2 B.3 / §7 I.1):
// тривиальная `CreateInput{Network: …}`-обёртка удалена — она лишь
// перепаковывала domain.X без дополнительного контекста. Поле `n.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixNetwork)`.
func (u *CreateNetworkUseCase) Execute(ctx context.Context, n domain.Network) (*operations.Operation, error) {
	if n.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	name := string(n.Name)
	if name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		existing, _, lerr := rd.Networks().List(ctx, NetworkFilter{ProjectID: n.ProjectID, Name: name}, Pagination{})
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

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (res *anypb.Any, derr error) {
		// KAC-127 Bug-2: surface async-worker failures. operations.Run masks
		// any non-gRPC-status error (and a panic) as Operation `INTERNAL
		// "internal worker error"` and does NOT log it — so a failing
		// Network.Create silently produces a Network with no FGA hierarchy
		// tuple (the fgawrite.Emit line is never reached), leaving every
		// per-resource Check `no path` with zero diagnostic trail. Recover +
		// log the real cause before the op-worker masks it (parity with the
		// kacho-iam AccessBinding.Create recover+log wrapper).
		defer func() {
			if r := recover(); r != nil {
				derr = fmt.Errorf("panic in Network.Create doCreate: %v", r)
				if u.logger != nil {
					u.logger.Error("network create operation panicked (KAC-127 Bug-2)",
						"op", op.ID, "network_id", netID, "project_id", string(n.ProjectID),
						"panic", fmt.Sprint(r))
				}
			}
		}()
		res, derr = u.doCreate(ctx, netID, n)
		if derr != nil && u.logger != nil {
			u.logger.Error("network create operation failed (KAC-127 Bug-2)",
				"op", op.ID, "network_id", netID, "project_id", string(n.ProjectID),
				"err", derr.Error())
		}
		return res, derr
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
//	(if inline) u.createDefaultSG.Execute(ctx, w, created.Network)
//	            // → w.SGs().Insert + SG.CREATED outbox
//	            //   + w.Networks().SetDefaultSGID + Network.UPDATED outbox
//	w.Commit()                         // либо всё, либо ничего (Abort/crash)
//
// Default-SG composition вынесена в `CreateDefaultSGUseCase.Execute` (skill
// evgeniy I.9-residual) — атомарность сохранена тем, что use-case работает
// в УЖЕ открытой нами `Writer`-TX (`w`), сам её не открывает и не commit'ит.
//
// FK Network.default_security_group_id → security_groups(id) `ON DELETE SET NULL`
// (см. squashed initial migration). SG-FK на network_id — RESTRICT, но в одной
// TX это нормально: Insert(SG) ссылается на только что вставленный Network в
// той же tx (видимость G.2 + Postgres deferred constraint check на коммите для
// non-deferrable — INSERT(child) после INSERT(parent) в одной TX проходит).
func (u *CreateNetworkUseCase) doCreate(ctx context.Context, netID string, n domain.Network) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, n.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", n.ProjectID)
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
		// Композиция use-case'ов в одной writer-TX (skill evgeniy I.9-residual):
		// CreateDefaultSGUseCase работает в нашей `w` — Abort/Commit делает caller.
		upd, sgErr := u.createDefaultSG.Execute(ctx, w, created.Network)
		if sgErr != nil {
			return nil, sgErr
		}
		finalRec = upd
	}

	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}

	// KAC-127 issue #22: publish the vpc_network→project hierarchy tuple so a
	// per-resource Get/Update/Delete/ListSubnets Check resolves through the
	// `<rel> from project` cascade. Best-effort + non-fatal (row committed).
	// fgawrite.Emit logs the result (`vpc fga hierarchy-tuple written` /
	// `... write failed`); a Debug pre-line keeps the writer-state visible
	// when tuple emission is investigated without adding INFO-level noise.
	if u.logger != nil {
		u.logger.Debug("network committed — emitting FGA hierarchy tuple",
			"network_id", finalRec.ID, "project_id", string(n.ProjectID),
			"fga_writer_nil", u.fgaWriter == nil)
	}
	fgawrite.Emit(ctx, u.fgaWriter, u.logger, "vpc_network", finalRec.ID, string(n.ProjectID))

	return marshalNetworkRecord(finalRec)
}
