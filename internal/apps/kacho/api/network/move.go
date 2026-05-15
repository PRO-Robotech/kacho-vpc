package network

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

// MoveNetworkUseCase — перенос Network в другой folder. Sync: dest required +
// different + existence. Async: повторная folder-existence-проверка +
// SetFolderID.
type MoveNetworkUseCase struct {
	repo         NetworkRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewMoveNetworkUseCase создаёт MoveNetworkUseCase.
func NewMoveNetworkUseCase(repo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo) *MoveNetworkUseCase {
	return &MoveNetworkUseCase{repo: repo, folderClient: folderClient, opsRepo: opsRepo}
}

// Execute — sync-валидация и старт worker'а.
func (u *MoveNetworkUseCase) Execute(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
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
		fmt.Sprintf("Move network %s", id),
		&vpcv1.MoveNetworkMetadata{NetworkId: id},
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
		return marshalNetworkRecord(updated)
	})

	return &op, nil
}
