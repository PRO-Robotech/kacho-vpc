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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// MoveSubnetUseCase — перенос Subnet в другой folder. Sync: dest required +
// different + existence. Async: повторная folder-existence-проверка +
// SetProjectID.
//
// Wave 5 replicate (KAC-94): Move + outbox-emit UPDATED атомарны в writer-TX.
type MoveSubnetUseCase struct {
	repo          Repo
	projectClient ProjectClient
	opsRepo       operations.Repo
}

// NewMoveSubnetUseCase создаёт MoveSubnetUseCase.
func NewMoveSubnetUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo) *MoveSubnetUseCase {
	return &MoveSubnetUseCase{repo: r, projectClient: projectClient, opsRepo: opsRepo}
}

// Execute — sync-валидация и старт worker'а.
func (u *MoveSubnetUseCase) Execute(ctx context.Context, id, destProjectID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if destProjectID == "" {
		return nil, invalidArg("destination_project_id", "destination_project_id is required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	cur, err := rd.Subnets().Get(ctx, id)
	_ = rd.Close()
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := checkMoveDestination(ctx, u.projectClient, cur.ProjectID, destProjectID); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Move subnet %s", id),
		&vpcv1.MoveSubnetMetadata{SubnetId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doMove(ctx, id, destProjectID)
	})

	return &op, nil
}

// doMove — async worker-тело Subnet.Move: проверяет destination-project и
// переносит Subnet в новый project.
func (u *MoveSubnetUseCase) doMove(ctx context.Context, id, destProjectID string) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, destProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destProjectID)
	}

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	updated, err := w.Subnets().SetProjectID(ctx, id, destProjectID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Subnet", updated.ID, "UPDATED", subnetPayloadMap(updated)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalSubnetRecord(updated)
}
