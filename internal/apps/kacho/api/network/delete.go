// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// DeleteNetworkUseCase — sync FAILED_PRECONDITION если в Network есть subnets /
// route tables / non-default SG. Async-часть (worker): default-SG cleanup +
// Network.Delete + оба outbox-emit'а — все в одной writer-TX (atomic). FK
// RESTRICT — DB-уровневый backstop.
type DeleteNetworkUseCase struct {
	repo           Repo
	subnetReader   SubnetReader      // may be nil → skip child class
	routeTableRead RouteTableReader  // may be nil
	sgRepo         SecurityGroupRepo // may be nil → skip default-SG cleanup
	opsRepo        operations.Repo
}

// NewDeleteNetworkUseCase создает DeleteNetworkUseCase. Все child-reader'ы
// необязательны: nil → пропускаем соответствующий child-класс (для unit-тестов
// со scoped wiring).
func NewDeleteNetworkUseCase(r Repo, subnetReader SubnetReader, routeTableRead RouteTableReader, sgRepo SecurityGroupRepo, opsRepo operations.Repo) *DeleteNetworkUseCase {
	return &DeleteNetworkUseCase{
		repo:           r,
		subnetReader:   subnetReader,
		routeTableRead: routeTableRead,
		sgRepo:         sgRepo,
		opsRepo:        opsRepo,
	}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteNetworkUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := u.checkNetworkEmpty(ctx, id); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete network %s", id),
		&vpcv1.DeleteNetworkMetadata{NetworkId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doDelete(ctx, id)
	})

	return &op, nil
}

// doDelete — async-часть Delete. Default-SG cleanup + Network.Delete + оба
// outbox-emit'а идут в ОДНОЙ writer-TX: либо все применяется, либо ничего
// (atomic, нет orphan-window сети без default-SG).
func (u *DeleteNetworkUseCase) doDelete(ctx context.Context, id string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// Собираем FGA owner-tuples для unregister в ТОЙ ЖЕ writer-TX, что и Delete:
	// ресурс исчезает — его место в hierarchy тоже. projectID нужен как subject
	// tuple'а; читаем его из строки до удаления.
	var unregTuples []fgaregister.Tuple

	// Default-SG cleanup в той же writer-TX. Не-default SG — preserve, FK
	// RESTRICT не даст удалить Network ⇒ FAILED_PRECONDITION "network is not
	// empty". sgRepo == nil → default-SG-inline выключен, чистить нечего.
	if u.sgRepo != nil {
		n, gerr := w.Networks().Get(ctx, id)
		switch {
		case gerr == nil && n.DefaultSecurityGroupID != "":
			if derr := w.SecurityGroups().Delete(ctx, n.DefaultSecurityGroupID); derr != nil && !errors.Is(derr, repo.ErrNotFound) {
				return nil, serviceerr.MapRepoErr(derr)
			}
			if oerr := w.Outbox().Emit(ctx, "SecurityGroup", n.DefaultSecurityGroupID, "DELETED", map[string]any{"id": n.DefaultSecurityGroupID}); oerr != nil {
				return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
			}
			unregTuples = append(unregTuples,
				fgaregister.ProjectHierarchy(n.ProjectID, "vpc_security_group", n.DefaultSecurityGroupID))
		case errors.Is(gerr, repo.ErrNotFound):
			// Сеть уже исчезла — пусть Networks().Delete ниже вернет каноничный NotFound.
		case gerr != nil:
			return nil, serviceerr.MapRepoErr(gerr)
		}
	}

	// Читаем projectID для network-unregister-tuple (best-effort: если строка уже
	// исчезла, Networks().Delete ниже вернет каноничный NotFound).
	if n, gerr := w.Networks().Get(ctx, id); gerr == nil {
		unregTuples = append(unregTuples,
			fgaregister.ProjectHierarchy(n.ProjectID, "vpc_network", id))
	}

	if err := w.Networks().Delete(ctx, id); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", id, "DELETED", map[string]any{"id": id}); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if len(unregTuples) > 0 {
		if err := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(unregTuples...)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return anypb.New(&emptypb.Empty{})
}

// checkNetworkEmpty — sync FAILED_PRECONDITION, если в сети еще есть subnets /
// route tables / non-default security groups (текст контракта:
// "Network <id> is not empty"). Reader'ы могут быть nil — тогда соответствующий
// child-класс не проверяется.
func (u *DeleteNetworkUseCase) checkNetworkEmpty(ctx context.Context, networkID string) error {
	notEmpty := func() error {
		return status.Errorf(codes.FailedPrecondition, "Network %s is not empty", networkID)
	}
	if u.subnetReader != nil {
		subs, _, err := u.subnetReader.List(ctx, SubnetFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return serviceerr.MapRepoErr(err)
		}
		if len(subs) > 0 {
			return notEmpty()
		}
	}
	if u.routeTableRead != nil {
		rts, _, err := u.routeTableRead.List(ctx, RouteTableFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return serviceerr.MapRepoErr(err)
		}
		if len(rts) > 0 {
			return notEmpty()
		}
	}
	if u.sgRepo != nil {
		sgs, _, err := u.sgRepo.List(ctx, SecurityGroupFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return serviceerr.MapRepoErr(err)
		}
		for _, sg := range sgs {
			if !sg.DefaultForNetwork {
				return notEmpty()
			}
		}
	}
	return nil
}
