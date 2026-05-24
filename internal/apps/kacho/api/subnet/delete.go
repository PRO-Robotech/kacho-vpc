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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteSubnetUseCase — Delete с двойной precondition-проверкой:
//
//  1. Подсеть не содержит internal Address'ов (sync FAILED_PRECONDITION
//     "Subnet has allocated internal addresses"). Async backstop — FK
//     RESTRICT через generated-колонку `addresses.internal_subnet_id`
//     (миграция 0013, KAC-34 — оба v4 и v6 family).
//  2. Подсеть не содержит NetworkInterface (KAC-33 reverts KAC-31: NIC→Subnet
//     FK = ON DELETE RESTRICT). Sync FAILED_PRECONDITION со списком NIC-id;
//     FK RESTRICT в worker'е — атомарный backstop. Порядок удаления —
//     снизу вверх: NIC → Address → Subnet → Network.
//
// `nicRepo == nil` → проверка пропускается (для тестов без NIC-wiring; FK
// RESTRICT всё равно подберёт address-bearing NIC через цепочку NIC → Address →
// Subnet).
//
// Wave 5 replicate (KAC-94): Delete + outbox-emit DELETED атомарны в writer-TX.
type DeleteSubnetUseCase struct {
	repo    Repo
	nicRepo NetworkInterfaceRepo // optional
	opsRepo operations.Repo
}

// NewDeleteSubnetUseCase создаёт DeleteSubnetUseCase. `nicRepo` опционален
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
	// Verbatim YC: a Subnet with internal Address children can not be deleted —
	// sync FAILED_PRECONDITION. The async FK RESTRICT path stays as the atomic
	// backstop in the worker. См. kacho-vpc#8.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	addrs, _, aerr := rd.Subnets().AddressesBySubnet(ctx, id, Pagination{})
	_ = rd.Close()
	if aerr != nil {
		return nil, mapRepoErr(aerr)
	}
	if len(addrs) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "Subnet has allocated internal addresses")
	}
	// KAC-33: NIC→Subnet FK = ON DELETE RESTRICT (migration 0012). NIC жёстко
	// блокирует свою подсеть. Surface a friendly sync FAILED_PRECONDITION; the
	// FK RESTRICT in the worker stays as the atomic backstop.
	if u.nicRepo != nil {
		nics, nerr := u.nicRepo.ListBySubnet(ctx, id)
		if nerr != nil {
			return nil, mapRepoErr(nerr)
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
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	if err := w.Subnets().Delete(ctx, id); err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Subnet", id, "DELETED", map[string]any{"id": id}); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(&emptypb.Empty{})
}
