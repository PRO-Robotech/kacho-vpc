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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteSecurityGroupUseCase — удаление SG. Default SG (DefaultForNetwork=true)
// нельзя удалить — sync FAILED_PRECONDITION.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): worker открывает Writer-TX,
// делает Delete + outbox-DELETED в одной TX, Commit.
type DeleteSecurityGroupUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteSecurityGroupUseCase создаёт DeleteSecurityGroupUseCase.
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
	// pre-flight check: default SG защищён.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	sgr := rd.SecurityGroups()
	existing, err := sgr.Get(ctx, id)
	if err != nil {
		_ = rd.Close()
		return nil, mapRepoErr(err)
	}
	if existing.DefaultForNetwork {
		_ = rd.Close()
		return nil, status.Errorf(codes.FailedPrecondition, "default security group cannot be deleted")
	}
	// KAC-239 S2: safe-delete — SG нельзя удалить, пока к ней кто-то подключён
	// (used_by непуст). guard на публичном Delete; cascade Network.Delete idёт
	// своим путём (worker удаляет default-SG до Network безусловно).
	used, uerr := sgr.UsedBy(ctx, id)
	_ = rd.Close()
	if uerr != nil {
		return nil, mapRepoErr(uerr)
	}
	if len(used) > 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "security group is in use")
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
			return nil, mapRepoErr(werr)
		}
		defer w.Abort()
		if derr := w.SecurityGroups().Delete(ctx, id); derr != nil {
			return nil, mapRepoErr(derr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", id, "DELETED", map[string]any{"id": id}); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, mapRepoErr(cerr)
		}
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}
