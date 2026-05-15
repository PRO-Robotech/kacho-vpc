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
)

// RemoveCidrBlocksUseCase — атомарное удаление CIDR-блоков из подсети.
//
// YC verbatim:
//   - Если CIDR не присутствует → FailedPrecondition.
//   - Если будет удалён последний CIDR → FailedPrecondition (subnet не может быть пустой).
//   - Если внутри CIDR есть Address — на текущей фазе пропускаем (доп. проверка
//     потребует JSON-запрос по addresses; в будущем добавится).
type RemoveCidrBlocksUseCase struct {
	repo    SubnetRepo
	opsRepo operations.Repo
}

// NewRemoveCidrBlocksUseCase создаёт RemoveCidrBlocksUseCase.
func NewRemoveCidrBlocksUseCase(repo SubnetRepo, opsRepo operations.Repo) *RemoveCidrBlocksUseCase {
	return &RemoveCidrBlocksUseCase{repo: repo, opsRepo: opsRepo}
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
		return nil, invalidArg("v4_cidr_blocks", "v4_cidr_blocks or v6_cidr_blocks is required")
	}
	op, err := operations.New(
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
		sub, err := u.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		remainingV4, removedV4 := subtractCIDRs(sub.V4CidrBlocks, v4)
		remainingV6, removedV6 := subtractCIDRs(sub.V6CidrBlocks, v6)
		if removedV4 != len(v4) || removedV6 != len(v6) {
			return nil, status.Errorf(codes.FailedPrecondition, "one or more CIDR blocks not found in subnet")
		}
		if len(remainingV4) == 0 && len(remainingV6) == 0 {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot remove last CIDR block from subnet")
		}
		updated, err := u.repo.SetCidrBlocks(ctx, id, remainingV4, remainingV6)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return marshalSubnetRecord(updated)
	})
	return &op, nil
}
