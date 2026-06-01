package securitygroup

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

// MoveSecurityGroupUseCase — перенос SG в другой folder. Sync: dest required +
// different + existence. Async: повторная folder-existence-проверка +
// SetProjectID в writer-TX + outbox-emit UPDATED.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): SetProjectID + outbox в одной
// CQRS writer-TX.
type MoveSecurityGroupUseCase struct {
	repo          Repo
	projectClient ProjectClient
	opsRepo       operations.Repo
}

// NewMoveSecurityGroupUseCase создаёт MoveSecurityGroupUseCase.
func NewMoveSecurityGroupUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo) *MoveSecurityGroupUseCase {
	return &MoveSecurityGroupUseCase{repo: r, projectClient: projectClient, opsRepo: opsRepo}
}

// Execute — sync-валидация и старт worker'а.
func (u *MoveSecurityGroupUseCase) Execute(ctx context.Context, id, destProjectID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if destProjectID == "" {
		return nil, invalidArg("destination_project_id", "destination_project_id is required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	cur, err := rd.SecurityGroups().Get(ctx, id)
	_ = rd.Close()
	if err != nil {
		return nil, mapRepoErr(err)
	}
	// Move-guard (KAC-243 §G / D5): network_id mandatory+immutable, а Network
	// привязана к своему проекту → cross-project Move сделал бы network_id
	// dangling. Поэтому network-bound SG нельзя двигать между проектами. В новой
	// модели все SG network-bound, поэтому Move практически запрещён — но причина
	// формулируется через «bound to a network». Sync fast-fail (до Operation).
	if cur.NetworkID != "" {
		return nil, status.Error(codes.FailedPrecondition,
			"security group cannot be moved between projects while bound to a network")
	}
	if err := checkMoveDestination(ctx, u.projectClient, cur.ProjectID, destProjectID); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Move security group %s", id),
		&vpcv1.MoveSecurityGroupMetadata{SecurityGroupId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, ferr := u.projectClient.Exists(ctx, destProjectID)
		if ferr != nil {
			return nil, status.Errorf(codes.Unavailable, "folder check: %v", ferr)
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destProjectID)
		}
		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			return nil, mapRepoErr(werr)
		}
		defer w.Abort()
		updated, uerr := w.SecurityGroups().SetProjectID(ctx, id, destProjectID)
		if uerr != nil {
			return nil, mapRepoErr(uerr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", updated.ID, "UPDATED", securityGroupPayloadMap(updated)); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, mapRepoErr(cerr)
		}
		return marshalSecurityGroupRecord(updated)
	})

	return &op, nil
}
