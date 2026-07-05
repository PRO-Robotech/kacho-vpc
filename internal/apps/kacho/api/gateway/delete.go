// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// DeleteGatewayUseCase — async-delete; sync-проверка ID, async — repo.Delete +
// outbox emit Gateway.DELETED в той же writer-TX (атомарно).
type DeleteGatewayUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteGatewayUseCase создает DeleteGatewayUseCase.
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
			return nil, serviceerr.MapRepoErr(werr)
		}
		defer w.Abort()

		// Читаем projectID до удаления — он нужен как subject unregister-tuple'а.
		var unreg []fgaregister.Tuple
		if cur, gerr := w.Gateways().Get(ctx, id); gerr == nil {
			unreg = append(unreg, fgaregister.ProjectHierarchy(cur.ProjectID, "vpc_gateway", id))
		}

		if derr := w.Gateways().Delete(ctx, id); derr != nil {
			return nil, serviceerr.MapRepoErr(derr)
		}
		if oerr := w.Outbox().Emit(ctx, "Gateway", id, "DELETED", map[string]any{"id": id}); oerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if len(unreg) > 0 {
			if rerr := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(unreg...)); rerr != nil {
				return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, rerr))
			}
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, serviceerr.MapRepoErr(cerr)
		}
		// Ответ Delete — google.protobuf.Empty.
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}
