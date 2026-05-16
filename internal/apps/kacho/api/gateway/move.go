package gateway

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

// MoveGatewayUseCase — перенос Gateway в другой folder. Sync: dest required +
// different + existence. Async: повторная folder-existence-проверка +
// SetFolderID + outbox emit в одной writer-TX.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5).
type MoveGatewayUseCase struct {
	repo         Repo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewMoveGatewayUseCase создаёт MoveGatewayUseCase.
func NewMoveGatewayUseCase(r Repo, folderClient FolderClient, opsRepo operations.Repo) *MoveGatewayUseCase {
	return &MoveGatewayUseCase{repo: r, folderClient: folderClient, opsRepo: opsRepo}
}

// Execute — sync-валидация и старт worker'а.
func (u *MoveGatewayUseCase) Execute(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	cur, gerr := rd.Gateways().Get(ctx, id)
	_ = rd.Close()
	if gerr != nil {
		return nil, mapRepoErr(gerr)
	}
	if err := checkMoveDestination(ctx, u.folderClient, cur.FolderID, destFolderID); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Move gateway %s", id),
		&vpcv1.MoveGatewayMetadata{GatewayId: id},
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
		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			return nil, mapRepoErr(werr)
		}
		defer w.Abort()

		updated, uerr := w.Gateways().SetFolderID(ctx, id, destFolderID)
		if uerr != nil {
			return nil, mapRepoErr(uerr)
		}
		if oerr := w.Outbox().Emit(ctx, "Gateway", updated.ID, "UPDATED", gatewayPayloadMap(updated)); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, mapRepoErr(cerr)
		}
		return marshalGatewayRecord(updated)
	})

	return &op, nil
}
