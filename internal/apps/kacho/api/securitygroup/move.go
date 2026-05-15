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
)

// MoveSecurityGroupUseCase — перенос SG в другой folder. Sync: dest required +
// different + existence. Async: повторная folder-existence-проверка +
// SetFolderID.
type MoveSecurityGroupUseCase struct {
	repo         SecurityGroupRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewMoveSecurityGroupUseCase создаёт MoveSecurityGroupUseCase.
func NewMoveSecurityGroupUseCase(repo SecurityGroupRepo, folderClient FolderClient, opsRepo operations.Repo) *MoveSecurityGroupUseCase {
	return &MoveSecurityGroupUseCase{repo: repo, folderClient: folderClient, opsRepo: opsRepo}
}

// Execute — sync-валидация и старт worker'а.
func (u *MoveSecurityGroupUseCase) Execute(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	cur, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := checkMoveDestination(ctx, u.folderClient, cur.FolderID, destFolderID); err != nil {
		return nil, err
	}

	op, err := operations.New(
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
		exists, err := u.folderClient.Exists(ctx, destFolderID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destFolderID)
		}
		updated, err := u.repo.SetFolderID(ctx, id, destFolderID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return marshalSecurityGroupRecord(updated)
	})

	return &op, nil
}
