package routetable

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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteRouteTableUseCase — async-delete; sync-проверка ID, async — repo.Delete.
//
// Wave 5 replicate (KAC-94): writer-TX явный, Delete + outbox DELETED атомарны.
// FK `subnets.route_table_id → route_tables(id) ON DELETE SET NULL` (миграция
// 0019) обнуляет route_table_id у привязанных Subnet'ов в той же tx-области
// — триггер AFTER UPDATE OF route_table_id ON subnets эмитит `Subnet.UPDATED`
// в outbox автоматически.
type DeleteRouteTableUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteRouteTableUseCase создаёт DeleteRouteTableUseCase.
func NewDeleteRouteTableUseCase(r Repo, opsRepo operations.Repo) *DeleteRouteTableUseCase {
	return &DeleteRouteTableUseCase{repo: r, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteRouteTableUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete route table %s", id),
		&vpcv1.DeleteRouteTableMetadata{RouteTableId: id},
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
			return nil, mapRepoErr(werr)
		}
		defer w.Abort()

		if err := w.RouteTables().Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		if err := w.Outbox().Emit(ctx, "RouteTable", id, "DELETED", map[string]any{"id": id}); err != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
		}
		if err := w.Commit(); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}
