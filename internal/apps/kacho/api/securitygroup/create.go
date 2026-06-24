// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

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

// CreateSecurityGroupUseCase инициирует создание SG. Sync-проверки (project
// exists, name unique, network exists) выполняются ДО создания Operation —
// клиент получает fast-fail gRPC-status, а не «200 + операция, упавшая через
// секунду». Async-часть (`doCreate`) — атомарный backstop через FK/UNIQUE:
// worker открывает ОДНУ Writer-TX, делает Insert(SG) + outbox-emit в ней, Commit.
//
// Default-SG для Network создается НЕ здесь: она inline в `CreateNetworkUseCase`
// через `domain.NewDefaultSecurityGroup`. Этот use-case — обычный явный Create
// от клиента.
type CreateSecurityGroupUseCase struct {
	repo          Repo
	networkReader NetworkReader
	sgReader      SecurityGroupReader
	projectClient ProjectClient
	opsRepo       operations.Repo
}

// WithSGReader подключает порт SecurityGroupReader для проверки SG-target-правил
// против сети, которой принадлежит создаваемая SG. Composition-root инжектит его
// (cqrsadapter.SecurityGroupAdapter); nil = проверка пропускается.
func (u *CreateSecurityGroupUseCase) WithSGReader(r SecurityGroupReader) *CreateSecurityGroupUseCase {
	u.sgReader = r
	return u
}

// NewCreateSecurityGroupUseCase создает CreateSecurityGroupUseCase.
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
// Принимает `domain.SecurityGroup` напрямую, без обертки-DTO. Поле `sg.ID` на
// входе пустое — назначаем внутри use-case'а через `ids.NewID(ids.PrefixSecurityGroup)`.
func (u *CreateSecurityGroupUseCase) Execute(ctx context.Context, sg domain.SecurityGroup) (*operations.Operation, error) {
	if sg.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	// network_id ОБЯЗАТЕЛЕН: SG обязана принадлежать ровно одной Network своего
	// проекта. Sync required-check — до создания Operation, в одном ряду с
	// `project_id required`.
	if sg.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, sg.NetworkID); err != nil {
		return nil, err
	}

	// Domain self-validation: имя/описание/labels через newtype.Validate() +
	// каждое правило через r.Validate() (description/labels). Cross-cutting
	// rule-валидация (direction, CIDR, protocol) — отдельно через validateSGRule
	// ниже (это не newtype-level).
	if err := serviceerr.FromValidation(sg.Validate()); err != nil {
		return nil, err
	}
	for i, r := range sg.Rules {
		if err := validateSGRule(fmt.Sprintf("rule_specs[%d]", i), r); err != nil {
			return nil, err
		}
	}

	// Sync project.Exists precheck намеренно отсутствует: он race-prone — между
	// sync-проверкой и async-частью project может быть удален peer-сервисом, и
	// second-writer-wins безусловно создавал бы ресурс. NotFound по project
	// возвращается через `operation.error` из async `doCreate`. Sync-проверки
	// network-existence/uniqueness (по DB-state в той же сервис-БД) остаются —
	// они race-free относительно peer-сервисов.
	if u.networkReader != nil {
		if _, err := u.networkReader.Get(ctx, sg.NetworkID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Network %s not found", sg.NetworkID)
			}
			return nil, serviceerr.MapRepoErr(err)
		}
	}

	// Same-network-валидация SG-target-правил: каждое правило с
	// `security_group_id` обязано ссылаться на SG из той же Network, что и
	// создаваемая SG. Sync fast-fail; async backstop — в doCreate.
	if err := validateSGTargetSameNetwork(ctx, u.sgReader, sg.NetworkID, sg.Rules,
		func(i int) string { return fmt.Sprintf("rule_specs[%d].security_group_id", i) }); err != nil {
		return nil, err
	}
	name := string(sg.Name)
	if name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		existing, _, lerr := rd.SecurityGroups().List(ctx, SecurityGroupFilter{ProjectID: sg.ProjectID, Name: name}, Pagination{})
		_ = rd.Close()
		if lerr != nil {
			return nil, serviceerr.MapRepoErr(lerr)
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

// doCreate — async-часть Create (внутри Operation worker'а). Project-exists +
// network-exists повторяются как defensive backstop; затем Insert через CQRS
// writer-TX + outbox-emit в той же TX.
func (u *CreateSecurityGroupUseCase) doCreate(ctx context.Context, sgID string, sg domain.SecurityGroup) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, sg.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", sg.ProjectID)
	}
	if u.networkReader != nil {
		if _, gerr := u.networkReader.Get(ctx, sg.NetworkID); gerr != nil {
			return nil, serviceerr.MapRepoErr(gerr)
		}
	}
	// Async backstop для same-network SG-target-правил: ловит гонку «target-SG
	// удалена / создана в другой сети после sync-precheck».
	if err := validateSGTargetSameNetwork(ctx, u.sgReader, sg.NetworkID, sg.Rules,
		func(i int) string { return fmt.Sprintf("rule_specs[%d].security_group_id", i) }); err != nil {
		return nil, err
	}

	sg.ID = sgID
	sg.Rules = assignRuleIDs(sg.Rules)

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.SecurityGroups().Insert(ctx, &sg)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "SecurityGroup", created.ID, "CREATED", helpers.DomainToMap(created)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Публикуем INTENT на vpc_security_group→project hierarchy-tuple в той же
	// writer-TX (at-least-once через transactional-outbox, не теряется на ошибке).
	// В mirror-feed несем labels SG + parent_project_id (ProjectHierarchyItem), а
	// не голый tuple — иначе resource_mirror в kacho-iam остается без labels и
	// ARM_LABELS-селектор не матчит даже только что созданную SG. Симметрично
	// network/create.go и subnet/create.go.
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
		fgaregister.ProjectHierarchyItem(string(sg.ProjectID), "vpc_security_group", created.ID,
			domain.LabelsToMap(created.Labels)),
	)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalSecurityGroupRecord(created)
}
