package privateendpoint

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
	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
)

// DeletePrivateEndpointUseCase — async-delete.
type DeletePrivateEndpointUseCase struct {
	repo    PrivateEndpointRepo
	opsRepo operations.Repo
}

// NewDeletePrivateEndpointUseCase создаёт DeletePrivateEndpointUseCase.
func NewDeletePrivateEndpointUseCase(repo PrivateEndpointRepo, opsRepo operations.Repo) *DeletePrivateEndpointUseCase {
	return &DeletePrivateEndpointUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeletePrivateEndpointUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("private endpoint", ids.PrefixPrivateEndpoint, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete private endpoint %s", id),
		&pe.DeletePrivateEndpointMetadata{PrivateEndpointId: id},
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
		// proto-options: response = google.protobuf.Empty (verbatim YC).
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}
