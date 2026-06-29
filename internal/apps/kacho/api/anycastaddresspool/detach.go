// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// DetachNetworkUseCase — async detach пула от сети. Живые anycast-аллокации пула
// в сети → Operation done с error FailedPrecondition. Иначе — снятие claim'ов +
// pivot в одной writer-TX. Detach непривязанного пула — no-op (идемпотентно).
type DetachNetworkUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDetachNetworkUseCase создаёт use-case.
func NewDetachNetworkUseCase(r Repo, opsRepo operations.Repo) *DetachNetworkUseCase {
	return &DetachNetworkUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync id-валидация + create Operation + worker.
func (u *DetachNetworkUseCase) Execute(ctx context.Context, poolID, networkID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("anycast address pool", ids.PrefixAnycastPool, poolID); err != nil {
		return nil, err
	}
	if networkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, err
	}
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Detach network %s from anycast address pool %s", networkID, poolID),
		&vpcv1.DetachNetworkMetadata{AnycastAddressPoolId: poolID, NetworkId: networkID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doDetach(ctx, poolID, networkID)
	})
	return &op, nil
}

// doDetach — async-часть: live-alloc guard + снятие claim'ов и pivot в writer-TX.
func (u *DetachNetworkUseCase) doDetach(ctx context.Context, poolID, networkID string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	pool, err := w.AnycastAddressPools().Get(ctx, poolID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	n, err := w.AnycastAddressPools().CountAllocationsInNetwork(ctx, poolID, networkID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if n > 0 {
		return nil, status.Error(codes.FailedPrecondition, "anycast address pool has allocated addresses in network")
	}
	if derr := w.AnycastAddressPools().DetachNetwork(ctx, poolID, networkID); derr != nil {
		return nil, serviceerr.MapRepoErr(derr)
	}
	if oerr := w.Outbox().Emit(ctx, "AnycastAddressPool", poolID, "NETWORK_DETACHED",
		map[string]any{"pool_id": poolID, "network_id": networkID}); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, serviceerr.MapRepoErr(cerr)
	}
	return anypb.New(toProto(pool))
}
