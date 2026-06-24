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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// AddCidrBlocksUseCase — атомарное добавление CIDR-блоков к подсети.
// Возвращает Operation; внутри worker'а:
//   - Get subnet → если не найден → NotFound.
//   - Validate каждого CIDR (host-bits=0).
//   - Проверка overlap внутри новой объединенной коллекции (v4 + v6).
//   - SetCidrBlocks (DB UPDATE). EXCLUDE constraint subnets_no_overlap_v4
//     проверяет primary CIDR на overlap с другими подсетями этой сети.
//
// Ограничение: EXCLUDE проверяет только array[1]. Если v4_cidr_primary
// неизменен (добавляем не в начало), overlap с соседними подсетями по
// добавляемым CIDR на DB-уровне не ловится — покрываем service-level
// проверкой через repo.List.
//
// Get + SetCidrBlocks + outbox-emit UPDATED атомарны в одной writer-TX.
type AddCidrBlocksUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewAddCidrBlocksUseCase создает AddCidrBlocksUseCase.
func NewAddCidrBlocksUseCase(r Repo, opsRepo operations.Repo) *AddCidrBlocksUseCase {
	return &AddCidrBlocksUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-валидация id/CIDR-формата + Operation + async-merge в worker'е.
func (u *AddCidrBlocksUseCase) Execute(ctx context.Context, id string, v4, v6 []string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if len(v4) == 0 && len(v6) == 0 {
		return nil, serviceerr.InvalidArg("v4_cidr_blocks", "v4_cidr_blocks or v6_cidr_blocks is required")
	}
	for i, c := range v4 {
		if err := validateSubnetV4CIDR(fmt.Sprintf("v4_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}
	for i, c := range v6 {
		if err := validateSubnetV6CIDR(fmt.Sprintf("v6_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}
	// Disjointness внутри переданного v6-списка (sync; mirror v4 — для v4 это
	// проверяется ниже на merged-наборе, что покрывает и intra-request).
	if err := checkCIDRDisjoint("v6_cidr_blocks", v6); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Add CIDR blocks to subnet %s", id),
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
		// подсети — закрывает lost-update.
		sub, gerr := w.Subnets().GetForUpdate(ctx, id)
		if gerr != nil {
			return nil, serviceerr.MapRepoErr(gerr)
		}
		mergedV4 := append([]string{}, sub.V4CidrBlocks...)
		mergedV4 = append(mergedV4, v4...)
		// Проверка пересечений внутри объединенного набора (sync, host-bits уже OK).
		// Покрывает overlap нового блока с уже существующим в этой же подсети.
		if err := checkCIDRDisjoint("v4_cidr_blocks", mergedV4); err != nil {
			return nil, err
		}
		// v6: то же самое.
		mergedV6 := append([]string{}, sub.V6CidrBlocks...)
		mergedV6 = appendDedup(mergedV6, v6)
		if err := checkCIDRDisjoint("v6_cidr_blocks", mergedV6); err != nil {
			return nil, err
		}
		updated, uerr := w.Subnets().SetCidrBlocks(ctx, id, mergedV4, mergedV6)
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
