package securitygroup

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"log/slog"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgawrite"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// CreateSecurityGroupUseCase инициирует создание SG. Sync-проверки (folder
// exists, name unique, network exists) выполняются ДО создания Operation —
// клиент получает fast-fail gRPC-status, а не «200 + операция, упавшая через
// секунду» (см. kacho-vpc#8). Async-часть (`doCreate`) — атомарный backstop
// через FK/UNIQUE.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5 / §7 I.9 / I.10): worker
// открывает ОДНУ Writer-TX, делает Insert(SG) + outbox-emit в ней, Commit.
//
// Default-SG creation для Network — НЕ здесь: она inline в
// `CreateNetworkUseCase` через `domain.NewDefaultSecurityGroup`. Этот use-case —
// обычный явный Create от клиента.
type CreateSecurityGroupUseCase struct {
	repo          Repo
	networkReader NetworkReader
	sgReader      SecurityGroupReader
	projectClient ProjectClient
	opsRepo       operations.Repo

	// fgaWriter / logger — KAC-127 issue #22: publish
	// `vpc_security_group:<id>#project@project:<project_id>` after commit.
	fgaWriter fgawrite.HierarchyTupleWriter
	logger    *slog.Logger
}

// WithFGAWriter wires the OpenFGA hierarchy-tuple writer (KAC-127 issue #22).
func (u *CreateSecurityGroupUseCase) WithFGAWriter(w fgawrite.HierarchyTupleWriter, logger *slog.Logger) *CreateSecurityGroupUseCase {
	u.fgaWriter = w
	u.logger = logger
	return u
}

// WithSGReader wires the SecurityGroupReader port used to validate SG-target
// rules against the owning SG's network (KAC-243 §C). Composition-root injects
// it (cqrsadapter.SecurityGroupAdapter); nil = validation skipped.
func (u *CreateSecurityGroupUseCase) WithSGReader(r SecurityGroupReader) *CreateSecurityGroupUseCase {
	u.sgReader = r
	return u
}

// NewCreateSecurityGroupUseCase создаёт CreateSecurityGroupUseCase.
func NewCreateSecurityGroupUseCase(r Repo, networkReader NetworkReader, projectClient ProjectClient, opsRepo operations.Repo) *CreateSecurityGroupUseCase {
	return &CreateSecurityGroupUseCase{
		repo:          r,
		networkReader: networkReader,
		projectClient: projectClient,
		opsRepo:       opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
//
// Принимает `domain.SecurityGroup` напрямую (KAC-94, skill evgeniy §2 B.3 / §7 I.1):
// тривиальная `CreateInput{SecurityGroup: …}`-обёртка удалена — она лишь
// перепаковывала domain.X без дополнительного контекста. Поле `sg.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixSecurityGroup)`.
func (u *CreateSecurityGroupUseCase) Execute(ctx context.Context, sg domain.SecurityGroup) (*operations.Operation, error) {
	if sg.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	// network_id ОБЯЗАТЕЛЕН (KAC-243 §A / D2): SG обязана принадлежать ровно
	// одной Network своего проекта. Reverts «optional network_id» (kacho-proto#8).
	// Sync required-check — до создания Operation, в одном ряду с
	// `project_id required` (D2: lower-case, без «is»).
	if sg.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, sg.NetworkID); err != nil {
		return nil, err
	}

	// Status — выставляем явно, если caller не задал (он не должен — это
	// internal-only field). Skill evgeniy §4 D.8 / AP-2.
	if sg.Status == "" {
		sg.Status = domain.SecurityGroupStatusActive
	}

	// Domain self-validation: имя/описание/labels через newtype.Validate() +
	// каждое правило через r.Validate() (description/labels). Cross-cutting
	// rule-валидация (direction, CIDR, protocol) — отдельно через validateSGRule
	// ниже (это не newtype-level). Skill evgeniy §4 D.5 / AP-1.
	if err := sg.Validate(); err != nil {
		return nil, err
	}
	for i, r := range sg.Rules {
		if err := validateSGRule(fmt.Sprintf("rule_specs[%d]", i), r); err != nil {
			return nil, err
		}
	}

	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	// Sync network-existence/uniqueness-проверки (через DB-state в той же сервис-БД)
	// остаются — они race-free относительно peer-сервисов.
	if u.networkReader != nil {
		if _, err := u.networkReader.Get(ctx, sg.NetworkID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Network %s not found", sg.NetworkID)
			}
			return nil, mapRepoErr(err)
		}
	}

	// Same-network-валидация SG-target-правил (KAC-243 §C, D3/D4): каждое
	// правило с `security_group_id` обязано ссылаться на SG из той же Network,
	// что и создаваемая SG. Sync fast-fail; async backstop — в doCreate.
	if err := validateSGTargetSameNetwork(ctx, u.sgReader, sg.NetworkID, sg.Rules,
		func(i int) string { return fmt.Sprintf("rule_specs[%d].security_group_id", i) }); err != nil {
		return nil, err
	}
	name := string(sg.Name)
	if name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		existing, _, lerr := rd.SecurityGroups().List(ctx, SecurityGroupFilter{ProjectID: sg.ProjectID, Name: name}, Pagination{})
		_ = rd.Close()
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "SecurityGroup with name %s already exists", name)
		}
	}

	sgID := ids.NewID(ids.PrefixSecurityGroup)
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create security group %s", name),
		&vpcv1.CreateSecurityGroupMetadata{SecurityGroupId: sgID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, sgID, sg)
	})

	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Folder-exists +
// network-exists повторяются как defensive backstop; затем Insert через CQRS
// writer-TX + outbox-emit в той же TX (skill evgeniy §6 G.5).
func (u *CreateSecurityGroupUseCase) doCreate(ctx context.Context, sgID string, sg domain.SecurityGroup) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, sg.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", sg.ProjectID)
	}
	if u.networkReader != nil {
		if _, gerr := u.networkReader.Get(ctx, sg.NetworkID); gerr != nil {
			return nil, mapRepoErr(gerr)
		}
	}
	// Async backstop для same-network SG-target-правил (KAC-243 §C, D4): ловит
	// гонку «target-SG удалена / создана в другой сети после sync-precheck».
	if err := validateSGTargetSameNetwork(ctx, u.sgReader, sg.NetworkID, sg.Rules,
		func(i int) string { return fmt.Sprintf("rule_specs[%d].security_group_id", i) }); err != nil {
		return nil, err
	}

	sg.ID = sgID
	sg.Rules = assignRuleIDs(sg.Rules)

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.SecurityGroups().Insert(ctx, &sg)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "SecurityGroup", created.ID, "CREATED", securityGroupPayloadMap(created)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	// KAC-127 issue #22: publish vpc_security_group→project hierarchy tuple.
	fgawrite.Emit(ctx, u.fgaWriter, u.logger, "vpc_security_group", created.ID, string(sg.ProjectID))
	return marshalSecurityGroupRecord(created)
}
