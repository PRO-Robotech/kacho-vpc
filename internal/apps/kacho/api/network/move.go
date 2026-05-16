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
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// MoveNetworkUseCase — перенос Network в другой folder. Sync: dest required +
// different + existence. Async: повторная folder-existence-проверка +
// SetFolderID.
//
// Wave 5 pilot (KAC-94): Move + outbox-emit UPDATED атомарны в writer-TX.
type MoveNetworkUseCase struct {
	repo         Repo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewMoveNetworkUseCase создаёт MoveNetworkUseCase.
func NewMoveNetworkUseCase(r Repo, folderClient FolderClient, opsRepo operations.Repo) *MoveNetworkUseCase {
	return &MoveNetworkUseCase{repo: r, folderClient: folderClient, opsRepo: opsRepo}
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
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	cur, err := rd.Networks().Get(ctx, id)
	_ = rd.Close()
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
		return u.doMove(ctx, id, destFolderID)
	})

	return &op, nil
}

func (u *MoveNetworkUseCase) doMove(ctx context.Context, id, destFolderID string) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, destFolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destFolderID)
	}

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	updated, err := w.Networks().SetFolderID(ctx, id, destFolderID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", updated.ID, "UPDATED", networkPayloadMap(updated)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", ports.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalNetworkRecord(updated)
}
