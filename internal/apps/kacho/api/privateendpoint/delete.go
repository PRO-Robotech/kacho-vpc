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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeletePrivateEndpointUseCase — async-delete.
//
// Wave 5 replicate (KAC-94): writer-TX явный, DML + outbox-emit DELETED в одной
// pgx.Tx (parity с Network/SG).
type DeletePrivateEndpointUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeletePrivateEndpointUseCase создаёт DeletePrivateEndpointUseCase.
func NewDeletePrivateEndpointUseCase(r Repo, opsRepo operations.Repo) *DeletePrivateEndpointUseCase {
	return &DeletePrivateEndpointUseCase{repo: r, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeletePrivateEndpointUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("private endpoint", ids.PrefixPrivateEndpoint, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	op, err := operations.NewFromContext(
		ctx,
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
		return u.doDelete(ctx, id)
	})
	return &op, nil
}

// doDelete — async-часть Delete. Wave 5 replicate (KAC-94): Delete + outbox-emit
// DELETED атомарны в одной writer-TX (skill evgeniy §6 G.5).
func (u *DeletePrivateEndpointUseCase) doDelete(ctx context.Context, id string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	if err := w.PrivateEndpoints().Delete(ctx, id); err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "PrivateEndpoint", id, "DELETED", map[string]any{"id": id}); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	// proto-options: response = google.protobuf.Empty (verbatim YC).
	return anypb.New(&emptypb.Empty{})
}
