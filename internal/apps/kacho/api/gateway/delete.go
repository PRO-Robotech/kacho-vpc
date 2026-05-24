package gateway

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

// DeleteGatewayUseCase — async-delete; sync-проверка ID, async — repo.Delete +
// outbox emit Gateway.DELETED в той же writer-TX.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5).
type DeleteGatewayUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteGatewayUseCase создаёт DeleteGatewayUseCase.
func NewDeleteGatewayUseCase(r Repo, opsRepo operations.Repo) *DeleteGatewayUseCase {
	return &DeleteGatewayUseCase{repo: r, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteGatewayUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete gateway %s", id),
		&vpcv1.DeleteGatewayMetadata{GatewayId: id},
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

		if derr := w.Gateways().Delete(ctx, id); derr != nil {
			return nil, mapRepoErr(derr)
		}
		if oerr := w.Outbox().Emit(ctx, "Gateway", id, "DELETED", map[string]any{"id": id}); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, mapRepoErr(cerr)
		}
		// proto-options: response = google.protobuf.Empty (verbatim YC).
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}
