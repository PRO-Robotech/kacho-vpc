// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// RemoveCidrBlocksUseCase — атомарное удаление CIDR-блоков из подсети.
//
// Правила:
//   - CIDR не присутствует в подсети → FailedPrecondition.
//   - удаление последнего CIDR → FailedPrecondition (subnet не может быть пустой).
//   - проверки занятости CIDR Address'ами сейчас нет (потребовала бы отдельного
//     запроса по addresses).
//
// Get + SetCidrBlocks + outbox-emit UPDATED атомарны в одной writer-TX.
type RemoveCidrBlocksUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewRemoveCidrBlocksUseCase создает RemoveCidrBlocksUseCase.
func NewRemoveCidrBlocksUseCase(r Repo, opsRepo operations.Repo) *RemoveCidrBlocksUseCase {
	return &RemoveCidrBlocksUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-валидация id + Operation + async-вычитание в worker'е.
func (u *RemoveCidrBlocksUseCase) Execute(ctx context.Context, id string, v4, v6 []string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if len(v4) == 0 && len(v6) == 0 {
		return nil, serviceerr.InvalidArg("v4_cidr_blocks", "v4_cidr_blocks or v6_cidr_blocks is required")
	}
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Remove CIDR blocks from subnet %s", id),
		&vpcv1.UpdateSubnetMetadata{SubnetId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			return nil, serviceerr.MapRepoErr(werr)
		}
		defer w.Abort()

		// FOR UPDATE: сериализует конкурентные Add/RemoveCidrBlocks на этой
		// подсети — lost-update исключен.
		sub, gerr := w.Subnets().GetForUpdate(ctx, id)
		if gerr != nil {
			return nil, serviceerr.MapRepoErr(gerr)
		}
		remainingV4, removedV4 := subtractCIDRs(sub.V4CidrBlocks, v4)
		remainingV6, removedV6 := subtractCIDRs(sub.V6CidrBlocks, v6)
		if removedV4 != len(v4) || removedV6 != len(v6) {
			return nil, status.Errorf(codes.FailedPrecondition, "one or more CIDR blocks not found in subnet")
		}
		if len(remainingV4) == 0 && len(remainingV6) == 0 {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot remove last CIDR block from subnet")
		}
		updated, uerr := w.Subnets().SetCidrBlocks(ctx, id, remainingV4, remainingV6)
		if uerr != nil {
			return nil, serviceerr.MapRepoErr(uerr)
		}
		if err := w.Outbox().Emit(ctx, "Subnet", updated.ID, "UPDATED", helpers.DomainToMap(updated)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
		}
		if err := w.Commit(); err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		return marshalSubnetRecord(updated)
	})
	return &op, nil
}
