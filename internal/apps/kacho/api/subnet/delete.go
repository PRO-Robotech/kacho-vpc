// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

import (
	"context"
	"fmt"
	"strings"

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

// DeleteSubnetUseCase — Delete с двойной precondition-проверкой:
//
//  1. Подсеть не содержит internal Address'ов (sync FAILED_PRECONDITION
//     "Subnet has allocated internal addresses"). Async backstop — FK
//     RESTRICT через generated-колонку `addresses.internal_subnet_id`
//     (обе family v4 и v6).
//  2. Подсеть не содержит NetworkInterface (NIC→Subnet FK = ON DELETE
//     RESTRICT). Sync FAILED_PRECONDITION со списком NIC-id; FK RESTRICT в
//     worker'е — атомарный backstop. Порядок удаления снизу вверх:
//     NIC → Address → Subnet → Network.
//
// `nicRepo == nil` → проверка пропускается (для тестов без NIC-wiring; FK
// RESTRICT все равно подберет address-bearing NIC через цепочку NIC → Address →
// Subnet).
//
// Delete + outbox-emit DELETED атомарны в writer-TX.
type DeleteSubnetUseCase struct {
	repo    Repo
	nicRepo NetworkInterfaceRepo // optional
	opsRepo operations.Repo
}

// NewDeleteSubnetUseCase создает DeleteSubnetUseCase. `nicRepo` опционален
// (nil → NIC-precondition пропускается).
func NewDeleteSubnetUseCase(r Repo, nicRepo NetworkInterfaceRepo, opsRepo operations.Repo) *DeleteSubnetUseCase {
	return &DeleteSubnetUseCase{repo: r, nicRepo: nicRepo, opsRepo: opsRepo}
}

// Execute — sync precondition checks → Operation → worker.
func (u *DeleteSubnetUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// Подсеть с internal Address-детьми удалить нельзя — sync FAILED_PRECONDITION.
	// Async-путь FK RESTRICT остается атомарным backstop'ом в worker'е.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	addrs, _, aerr := rd.Subnets().AddressesBySubnet(ctx, id, Pagination{})
	_ = rd.Close()
	if aerr != nil {
		return nil, serviceerr.MapRepoErr(aerr)
	}
	if len(addrs) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "Subnet has allocated internal addresses")
	}
	// NIC→Subnet FK = ON DELETE RESTRICT. NIC жестко блокирует свою подсеть.
	// Отдаем дружелюбный sync FAILED_PRECONDITION; FK RESTRICT в worker'е
	// остается атомарным backstop'ом.
	if u.nicRepo != nil {
		nics, nerr := u.nicRepo.ListBySubnet(ctx, id)
		if nerr != nil {
			return nil, serviceerr.MapRepoErr(nerr)
		}
		if len(nics) > 0 {
			nicIDs := make([]string, 0, len(nics))
			for _, n := range nics {
				nicIDs = append(nicIDs, n.ID)
			}
			return nil, status.Errorf(codes.FailedPrecondition,
				"subnet %s has %d network interface(s) (%s); delete them first", id, len(nics), strings.Join(nicIDs, ", "))
		}
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete subnet %s", id),
		&vpcv1.DeleteSubnetMetadata{SubnetId: id},
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

// doDelete — Subnet.Delete + outbox-emit DELETED атомарны в одной CQRS-TX.
func (u *DeleteSubnetUseCase) doDelete(ctx context.Context, id string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// Читаем projectID до удаления — он нужен как subject в unregister-tuple.
	var unreg []fgaregister.Tuple
	if cur, gerr := w.Subnets().Get(ctx, id); gerr == nil {
		unreg = append(unreg, fgaregister.ProjectHierarchy(string(cur.ProjectID), "vpc_subnet", id))
	}

	if err := w.Subnets().Delete(ctx, id); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Subnet", id, "DELETED", map[string]any{"id": id}); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if len(unreg) > 0 {
		if err := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(unreg...)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return anypb.New(&emptypb.Empty{})
}
