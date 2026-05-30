package securitygroup

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// GetSecurityGroupUseCase — простой read; единственная «логика» — id-валидация
// и перевод repo-sentinel в gRPC status. Skill evgeniy §2 B.3: use-case можно
// было бы вообще опустить, но handler-у удобнее единый шов через use-case'ы.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1): открывает CQRS Reader, читает,
// закрывает. Read-only TX — параллельный writer не блокируется.
type GetSecurityGroupUseCase struct {
	repo Repo
}

// NewGetSecurityGroupUseCase создаёт GetSecurityGroupUseCase.
func NewGetSecurityGroupUseCase(r Repo) *GetSecurityGroupUseCase {
	return &GetSecurityGroupUseCase{repo: r}
}

// Execute возвращает repo-entity SG. NotFound → mapRepoErr → gRPC NotFound.
func (u *GetSecurityGroupUseCase) Execute(ctx context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	sgr := rd.SecurityGroups()
	sg, err := sgr.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	// KAC-239 S2: derived-on-read used_by (потребители SG). Best-effort —
	// ошибка скана не должна валить Get самого SG.
	if used, uerr := sgr.UsedBy(ctx, id); uerr == nil {
		sg.UsedBy = used
	}
	return sg, nil
}
