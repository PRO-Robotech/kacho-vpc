// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// AttachNetworkUseCase — async M:N attach пула к сети. Sync-проверки (id-формат,
// существование пула/сети, same-project) — fast-fail. Async-часть: INSERT pivot +
// claim на каждый блок пула в той же writer-TX; claim-overlap (23P01) → Operation
// done с error FailedPrecondition. Идемпотентность повторного attach — ON CONFLICT
// DO NOTHING на pivot и claim'ах.
type AttachNetworkUseCase struct {
	repo          Repo
	networkReader NetworkReader
	opsRepo       operations.Repo
}

// NewAttachNetworkUseCase создаёт use-case.
func NewAttachNetworkUseCase(r Repo, networkReader NetworkReader, opsRepo operations.Repo) *AttachNetworkUseCase {
	return &AttachNetworkUseCase{repo: r, networkReader: networkReader, opsRepo: opsRepo}
}

// Execute — sync-валидация + create Operation + worker.
func (u *AttachNetworkUseCase) Execute(ctx context.Context, poolID, networkID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("anycast address pool", ids.PrefixAnycastPool, poolID); err != nil {
		return nil, err
	}
	if networkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, err
	}
	// Sync-precheck: пул и сеть существуют и принадлежат одному проекту.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	pool, perr := rd.AnycastAddressPools().Get(ctx, poolID)
	if perr != nil {
		_ = rd.Close()
		return nil, serviceerr.MapRepoErr(perr)
	}
	_ = rd.Close()
	if u.networkReader != nil {
		net, nerr := u.networkReader.Get(ctx, networkID)
		if nerr != nil {
			if errors.Is(nerr, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Network %s not found", networkID)
			}
			return nil, serviceerr.MapRepoErr(nerr)
		}
		if net.ProjectID != pool.ProjectID {
			return nil, status.Error(codes.InvalidArgument,
				"Illegal argument networkId: network and anycast address pool must belong to the same project")
		}
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Attach network %s to anycast address pool %s", networkID, poolID),
		&vpcv1.AttachNetworkMetadata{AnycastAddressPoolId: poolID, NetworkId: networkID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doAttach(ctx, poolID, networkID)
	})
	return &op, nil
}

// doAttach — async-часть: INSERT pivot + per-block claim в одной writer-TX.
func (u *AttachNetworkUseCase) doAttach(ctx context.Context, poolID, networkID string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	pool, err := w.AnycastAddressPools().Get(ctx, poolID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if aerr := w.AnycastAddressPools().AttachNetwork(ctx, poolID, networkID, pool.CIDRBlocks); aerr != nil {
		return nil, serviceerr.MapRepoErr(aerr)
	}
	if oerr := w.Outbox().Emit(ctx, "AnycastAddressPool", poolID, "NETWORK_ATTACHED",
		map[string]any{"pool_id": poolID, "network_id": networkID}); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, serviceerr.MapRepoErr(cerr)
	}
	return anypb.New(toProto(pool))
}
