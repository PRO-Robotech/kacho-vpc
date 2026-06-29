// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

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
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// DeleteSecurityGroupUseCase — удаление SG. Default SG (DefaultForNetwork=true)
// нельзя удалить — sync FAILED_PRECONDITION. Worker открывает Writer-TX, делает
// Delete + outbox-DELETED в одной TX, Commit.
type DeleteSecurityGroupUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteSecurityGroupUseCase создает DeleteSecurityGroupUseCase.
func NewDeleteSecurityGroupUseCase(r Repo, opsRepo operations.Repo) *DeleteSecurityGroupUseCase {
	return &DeleteSecurityGroupUseCase{repo: r, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteSecurityGroupUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	// pre-flight check: default SG защищен.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	existing, err := rd.SecurityGroups().Get(ctx, id)
	_ = rd.Close()
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if existing.DefaultForNetwork {
		return nil, status.Errorf(codes.FailedPrecondition, "default security group cannot be deleted")
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete security group %s", id),
		&vpcv1.DeleteSecurityGroupMetadata{SecurityGroupId: id},
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
		if derr := w.SecurityGroups().Delete(ctx, id); derr != nil {
			return nil, serviceerr.MapRepoErr(derr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", id, "DELETED", map[string]any{"id": id}); oerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		// Снимаем vpc_security_group→project hierarchy-tuple в той же writer-TX
		// (projectID — из чтения на sync-фазе).
		if rerr := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(
			fgaregister.ProjectHierarchy(existing.ProjectID, "vpc_security_group", id),
		)); rerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, rerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, serviceerr.MapRepoErr(cerr)
		}
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}
