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
)

// DeleteSecurityGroupUseCase — удаление SG. Default SG (DefaultForNetwork=true)
// нельзя удалить — sync FAILED_PRECONDITION.
type DeleteSecurityGroupUseCase struct {
	repo    SecurityGroupRepo
	opsRepo operations.Repo
}

// NewDeleteSecurityGroupUseCase создаёт DeleteSecurityGroupUseCase.
func NewDeleteSecurityGroupUseCase(repo SecurityGroupRepo, opsRepo operations.Repo) *DeleteSecurityGroupUseCase {
	return &DeleteSecurityGroupUseCase{repo: repo, opsRepo: opsRepo}
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
	existing, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if existing.DefaultForNetwork {
		return nil, status.Errorf(codes.FailedPrecondition, "default security group cannot be deleted")
	}

	op, err := operations.New(
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
		if err := u.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}
