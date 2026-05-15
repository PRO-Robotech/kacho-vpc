package securitygroup

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GetSecurityGroupUseCase — простой read; единственная «логика» — id-валидация
// и перевод repo-sentinel в gRPC status. Skill evgeniy §2 B.3: use-case можно
// было бы вообще опустить, но handler-у удобнее единый шов через use-case'ы.
type GetSecurityGroupUseCase struct {
	repo SecurityGroupRepo
}

// NewGetSecurityGroupUseCase создаёт GetSecurityGroupUseCase.
func NewGetSecurityGroupUseCase(repo SecurityGroupRepo) *GetSecurityGroupUseCase {
	return &GetSecurityGroupUseCase{repo: repo}
}

// Execute возвращает repo-entity SG. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetSecurityGroupUseCase) Execute(ctx context.Context, id string) (*domain.SecurityGroupRecord, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	sg, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return sg, nil
}
