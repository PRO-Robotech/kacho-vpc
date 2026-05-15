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
type DeleteSubnetUseCase struct {
	repo    SubnetRepo
	nicRepo NetworkInterfaceRepo // optional
	opsRepo operations.Repo
}

// NewDeleteSubnetUseCase создаёт DeleteSubnetUseCase. `nicRepo` опционален
// (nil → NIC-precondition пропускается).
func NewDeleteSubnetUseCase(repo SubnetRepo, nicRepo NetworkInterfaceRepo, opsRepo operations.Repo) *DeleteSubnetUseCase {
	return &DeleteSubnetUseCase{repo: repo, nicRepo: nicRepo, opsRepo: opsRepo}
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
	addrs, _, aerr := u.repo.AddressesBySubnet(ctx, id, Pagination{})
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

	op, err := operations.New(
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
		if err := u.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}
