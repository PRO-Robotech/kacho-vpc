// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// CreateNetworkUseCase инициирует создание Network. Sync-проверки (name unique)
// выполняются ДО создания Operation — клиент получает fast-fail gRPC-status, а не
// «200 + операция, упавшая через секунду». Async-часть (`doCreate`) — атомарный
// backstop через FK/UNIQUE.
//
// Worker открывает ОДНУ Writer-TX и делает в ней Insert(Network) →
// Insert(SG, default) → SetDefaultSGID(Network, sg.ID) с тремя outbox-emit'ами.
// Либо все три DML видны (Commit), либо ни один (Abort/crash) — orphan-SG window
// исключен.
//
// Default-SG creation управляется флагом `defaultSGInline`. При
// `defaultSGInline=false` worker создает только Network — admin может досоздать
// default SG через public API. Сама inline-логика вынесена в отдельный
// `CreateDefaultSGUseCase` (см. `default_sg.go`) и вызывается ВНУТРИ writer-TX
// `doCreate` перед `Commit()`, чем и сохраняется atomic-семантика.
type CreateNetworkUseCase struct {
	repo            Repo
	projectClient   ProjectClient
	opsRepo         operations.Repo
	defaultSGInline bool // KACHO_VPC_DEFAULT_SG_INLINE
	createDefaultSG *CreateDefaultSGUseCase

	// registrar — синхронная регистрация owner-tuple'а в kacho-iam после commit
	// (sync-primary; outbox-intent остается at-least-once backstop'ом). nil →
	// sync-путь пропускается (dev/no-iam), регистрация только через drainer.
	registrar fgaregister.Registrar

	// logger — диагностический trail async-worker'а (panic-recover до того, как
	// op-worker замаскирует причину). FGA owner-tuple эмитится как intent в
	// writer-TX, а не пишется напрямую отсюда.
	logger *slog.Logger
}

// NewCreateNetworkUseCase создает CreateNetworkUseCase. defaultSGInline берется
// из конфига (`cfg.Network.DefaultSGInline`) — при true в одной writer-TX
// создается default SG (через композицию с `CreateDefaultSGUseCase`) и
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

// WithLogger подключает диагностический логгер для async Create-worker'а. FGA
// owner-tuple эмитится как outbox-intent в writer-TX, а не пишется напрямую.
// Nil logger → диагностический trail отключен.
func (u *CreateNetworkUseCase) WithLogger(logger *slog.Logger) *CreateNetworkUseCase {
	u.logger = logger
	return u
}

// WithRegistrar подключает синхронный owner-tuple registrar (Decision 2). После
// коммита Network (+ inline default-SG) те же Item'ы, что эмитятся в
// outbox-intent, синхронно регистрируются в kacho-iam — owner-grant доступен
// сразу. Nil registrar → sync-путь пропускается (только async drainer).
func (u *CreateNetworkUseCase) WithRegistrar(r fgaregister.Registrar) *CreateNetworkUseCase {
	u.registrar = r
	return u
}

// Execute — sync-валидация + create Operation + запуск worker'а. Возвращает
// созданный Operation указателем (caller'у нужен он для `OperationService.Get`).
// Принимает `domain.Network` напрямую; поле `n.ID` на входе пустое — назначаем
// внутри use-case'а через `ids.NewID(ids.PrefixNetwork)`.
func (u *CreateNetworkUseCase) Execute(ctx context.Context, n domain.Network) (*operations.Operation, error) {
	if n.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if err := serviceerr.FromValidation(n.Validate()); err != nil {
		return nil, err
	}
	// Sync project.Exists precheck не делаем — он race-prone: между sync-проверкой
	// и async-частью project может быть удален peer-сервисом, и second-writer-wins
	// безусловно создавал бы ресурс. NotFound возвращается через `operation.error`
	// из async `doCreate`.
	name := string(n.Name)
	if name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		existing, _, lerr := rd.Networks().List(ctx, NetworkFilter{ProjectID: n.ProjectID, Name: name}, Pagination{})
		_ = rd.Close()
		if lerr != nil {
			return nil, serviceerr.MapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Network with name %s already exists", name)
		}
	}

	netID := ids.NewID(ids.PrefixNetwork)
	op, err := operations.NewFromContext(
		ctx,
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
		// Поднимаем наружу диагностику падений async-worker'а. operations.Run
		// маскирует любую не-gRPC-status ошибку (и panic) как Operation `INTERNAL
		// "internal worker error"` и НЕ логирует ее — упавший Network.Create
		// молча оставил бы Network без FGA register-intent (writer-TX
		// откатилась), и каждый per-resource Check возвращал бы `no path` без
		// единого следа. Recover + лог реальной причины ДО того, как op-worker
		// ее замаскирует.
		defer func() {
			if r := recover(); r != nil {
				derr = fmt.Errorf("panic in Network.Create doCreate: %v", r)
				if u.logger != nil {
					u.logger.Error("network create operation panicked",
						"op", op.ID, "network_id", netID, "project_id", string(n.ProjectID),
						"panic", fmt.Sprint(r))
				}
			}
		}()
		res, derr = u.doCreate(ctx, netID, n)
		if derr != nil && u.logger != nil {
			u.logger.Error("network create operation failed",
				"op", op.ID, "network_id", netID, "project_id", string(n.ProjectID),
				"err", derr.Error())
		}
		return res, derr
	})

	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// project-exists + Insert (FK ограничения / UNIQUE-нарушения); inline default-SG
// creation (builder из domain), затем link через SetDefaultSGID(Network, sg.ID).
//
// ВСЕ идет в одной writer-TX:
//
//	w := u.repo.Writer(ctx)            // открыли единую TX
//	created := w.Networks().Insert     // Network.CREATED outbox
//	(if inline) u.createDefaultSG.Execute(ctx, w, created.Network)
//	            // → w.SGs().Insert + SG.CREATED outbox
//	            //   + w.Networks().SetDefaultSGID + Network.UPDATED outbox
//	w.Commit()                         // либо все, либо ничего (Abort/crash)
//
// Так исключены частичные результаты на crash между шагами (orphan SG, Network
// без default_sg_id или забытый outbox-event). Default-SG composition вынесена в
// `CreateDefaultSGUseCase.Execute`; атомарность сохранена тем, что use-case
// работает в УЖЕ открытой нами `Writer`-TX (`w`), сам ее не открывает и не
// commit'ит.
//
// FK Network.default_security_group_id → security_groups(id) `ON DELETE SET NULL`.
// SG-FK на network_id — RESTRICT, но в одной TX это нормально: Insert(SG)
// ссылается на только что вставленный Network в той же tx (видимость + Postgres
// constraint check на коммите — INSERT(child) после INSERT(parent) в одной TX
// проходит).
func (u *CreateNetworkUseCase) doCreate(ctx context.Context, netID string, n domain.Network) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, n.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", n.ProjectID)
	}

	n.ID = netID

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.Networks().Insert(ctx, &n)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", created.ID, "CREATED", helpers.DomainToMap(created)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}

	finalRec := created
	if u.defaultSGInline {
		// Композиция use-case'ов в одной writer-TX: CreateDefaultSGUseCase
		// работает в нашей `w` — Abort/Commit делает caller.
		upd, sgErr := u.createDefaultSG.Execute(ctx, w, created.Network)
		if sgErr != nil {
			return nil, sgErr
		}
		finalRec = upd
	}

	// Публикуем INTENT на hierarchy-tuple vpc_network→project в ТОЙ ЖЕ writer-TX,
	// что и Insert (один commit, без dual-write). Register-drainer позже применит
	// его через kacho-iam InternalIAMService.RegisterResource (idempotent, retry
	// на Unavailable, tuple durable) — так tuple не теряется при transient
	// FGA-сбое. Inline default-SG tuple — часть ТОГО ЖЕ intent. Network-tuple
	// несет labels сети + parent_project_id для selector-mirror feed; auto
	// default-SG несет пустой feed (он не tenant-labelled selector-target).
	items := []fgaregister.Item{
		fgaregister.ProjectHierarchyItem(string(n.ProjectID), "vpc_network", finalRec.ID,
			domain.LabelsToMap(finalRec.Labels)),
	}
	if finalRec.DefaultSecurityGroupID != "" {
		items = append(items,
			fgaregister.ProjectHierarchyItem(string(n.ProjectID), "vpc_security_group", finalRec.DefaultSecurityGroupID, nil))
	}
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(items...)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}

	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}

	// Sync-primary owner-tuple registration (после durable commit ресурса +
	// outbox-intent). Грант доступен сразу — без гонки с async drainer'ом.
	// Fail-closed: сбой регистрации → Operation error (ресурс закоммичен,
	// intent durable → backstop drainer дорегистрирует при восстановлении iam).
	if u.registrar != nil {
		if err := u.registrar.Register(ctx, items); err != nil {
			return nil, err
		}
	}

	return marshalNetworkRecord(finalRec)
}
