// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
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
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// DeleteAnycastAddressPoolUseCase — async Delete через Operation. Непустой пул
// (есть приаттаченные сети) → Operation done с error FailedPrecondition. Пустой —
// снятие anycast-claim'ов + DELETE пула (cascade child-блоки и pivot) + outbox +
// FGA unregister-intent атомарно в writer-TX (применяется async drainer'ом).
type DeleteAnycastAddressPoolUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteAnycastAddressPoolUseCase создаёт use-case.
func NewDeleteAnycastAddressPoolUseCase(r Repo, opsRepo operations.Repo) *DeleteAnycastAddressPoolUseCase {
	return &DeleteAnycastAddressPoolUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync id-валидация + create Operation + worker.
func (u *DeleteAnycastAddressPoolUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("anycast address pool", ids.PrefixAnycastPool, id); err != nil {
		return nil, err
	}
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete anycast address pool %s", id),
		&vpcv1.DeleteAnycastAddressPoolMetadata{AnycastAddressPoolId: id},
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

// doDelete — async-часть: not-empty guard + Delete + outbox + FGA unregister.
func (u *DeleteAnycastAddressPoolUseCase) doDelete(ctx context.Context, id string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	rec, err := w.AnycastAddressPools().Get(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	// Непустой пул (есть приаттаченные сети / живые аллокации) — удалять нельзя.
	// Живая anycast-аллокация невозможна без attach, поэтому достаточно
	// проверить число pivot-строк.
	n, err := w.AnycastAddressPools().CountAttachments(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if n > 0 {
		return nil, status.Error(codes.FailedPrecondition, "anycast address pool is not empty")
	}
	if derr := w.AnycastAddressPools().Delete(ctx, id); derr != nil {
		return nil, serviceerr.MapRepoErr(derr)
	}
	if oerr := w.Outbox().Emit(ctx, "AnycastAddressPool", id, "DELETED", map[string]any{"id": id}); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if rerr := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(
		fgaregister.ProjectHierarchy(rec.ProjectID, "vpc_anycast_address_pool", id),
	)); rerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, rerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, serviceerr.MapRepoErr(cerr)
	}
	return anypb.New(&emptypb.Empty{})
}
